// Package gitcli implements ports.CommitLog with the git command, read
// only on the remote and on the user's clone (docs/plan/M16-deployed-commit.md
// §6). It keeps its own copy of each remote, holding commits only (or
// commits and trees when paths must be counted), so the user's clone is
// never written to; when the remote cannot be reached it reads the clone
// instead and says the answer may be stale.
package gitcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Git runs git.
type Git struct {
	// CacheDir holds Huginn's copies of the remotes; deleting it is safe.
	CacheDir string
	// Binary is the git command; empty means "git" from PATH.
	Binary string
	// Getenv reads the environment (GIT_SSH_COMMAND, GIT_SSH); nil means os.Getenv.
	Getenv func(string) string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Timeouts of one git command.
const (
	networkTimeout = 90 * time.Second
	localTimeout   = 10 * time.Second
)

var (
	remoteName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]*$`)
	tagName    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+/-]*$`)
)

// RemoteURL implements ports.CommitLog.
func (g *Git) RemoteURL(ctx context.Context, r ports.GitRepo) (string, error) {
	if !remoteName.MatchString(r.Remote) {
		return "", domain.KindError(domain.ErrConfig, "invalid remote name "+strconv.Quote(r.Remote))
	}
	if st, err := os.Stat(r.Dir); err != nil || !st.IsDir() {
		return "", domain.KindError(domain.ErrNotFound, "no folder "+r.Dir)
	}
	out, err := g.run(ctx, localTimeout, nil, r.Dir, "remote", "get-url", r.Remote)
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)
	if url == "" || strings.HasPrefix(url, "-") {
		return "", domain.KindError(domain.ErrConfig, "invalid remote URL")
	}
	return url, nil
}

// Refresh implements ports.CommitLog: it creates the copy of the remote
// the first time, then fetches what is new. Only Huginn's copy is written.
func (g *Git) Refresh(ctx context.Context, r ports.GitRepo, trees bool) error {
	url, err := g.RemoteURL(ctx, r)
	if err != nil {
		return err
	}
	lock := g.lock(url)
	lock.Lock()
	defer lock.Unlock()
	cache := g.cachePath(url)
	filter := "tree:0" // commits only: enough for messages and counts
	if trees {
		filter = "blob:none" // and trees: needed to count by paths
	}
	if current, err := g.run(ctx, localTimeout, nil, cache, "config", "--get", "remote.origin.partialclonefilter"); err == nil &&
		strings.TrimSpace(current) == "tree:0" && trees {
		if err := os.RemoveAll(cache); err != nil { // a commits-only copy cannot count by paths: start again
			return fmt.Errorf("replace the git copy %s: %w", cache, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cache, "HEAD")); errors.Is(err, fs.ErrNotExist) {
		if err := g.create(ctx, cache, url, filter); err != nil {
			_ = os.RemoveAll(cache)
			return err
		}
	}
	env := g.sshEnv(ctx, r.Dir)
	if _, err := g.run(ctx, networkTimeout, env, cache, "fetch", "--quiet", "--prune", "--no-tags", "--filter="+filter, "origin"); err != nil {
		return err
	}
	_, err = g.run(ctx, networkTimeout, env, cache, "remote", "set-head", "origin", "--auto")
	return err
}

func (g *Git) create(ctx context.Context, cache, url, filter string) error {
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(cache), err)
	}
	if _, err := g.run(ctx, localTimeout, nil, "", "init", "--quiet", "--bare", cache); err != nil {
		return err
	}
	for _, kv := range [][]string{
		{"remote.origin.url", url},
		{"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
		{"--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*"},
		{"remote.origin.promisor", "true"},
		{"remote.origin.partialclonefilter", filter},
		{"gc.auto", "0"},
	} {
		if _, err := g.run(ctx, localTimeout, nil, cache, append([]string{"config"}, kv...)...); err != nil {
			return err
		}
	}
	return nil
}

// Commit implements ports.CommitLog.
func (g *Git) Commit(ctx context.Context, r ports.GitRepo, sha string) (ports.Commit, bool, error) {
	if !domain.IsCommitID(sha) {
		return ports.Commit{}, false, domain.KindError(domain.ErrNotFound, "not a commit id")
	}
	var out string
	stale, err := g.read(ctx, r, func(dir string) error {
		var err error
		out, err = g.run(ctx, localTimeout, nil, dir, "log", "-1", "--no-walk", "--format=%H%x00%an%x00%aI%x00%s", "--end-of-options", sha+"^{commit}")
		return err
	})
	if err != nil {
		return ports.Commit{}, stale, err
	}
	f := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 4)
	if len(f) != 4 {
		return ports.Commit{}, stale, domain.KindError(domain.ErrNotFound, "unknown commit "+sha)
	}
	at, _ := time.Parse(time.RFC3339, f[2])
	return ports.Commit{SHA: f[0], Author: f[1], At: at, Subject: f[3]}, stale, nil
}

// Tag implements ports.CommitLog.
func (g *Git) Tag(ctx context.Context, r ports.GitRepo, tag string) (string, bool, error) {
	if !tagName.MatchString(tag) || strings.Contains(tag, "..") {
		return "", false, domain.KindError(domain.ErrNotFound, "not a tag name")
	}
	var out string
	stale, err := g.read(ctx, r, func(dir string) error {
		var err error
		out, err = g.run(ctx, localTimeout, nil, dir, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}")
		return err
	})
	return strings.TrimSpace(out), stale, err
}

// Count implements ports.CommitLog.
func (g *Git) Count(ctx context.Context, r ports.GitRepo, sha, ref string, paths []string) (int, bool, bool, error) {
	if !domain.IsCommitID(sha) || !remoteName.MatchString(ref) || strings.Contains(ref, "..") {
		return 0, false, false, domain.KindError(domain.ErrConfig, "invalid commit or reference")
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "-") {
			return 0, false, false, domain.KindError(domain.ErrConfig, "invalid path "+strconv.Quote(p))
		}
	}
	var behind int
	var onRef bool
	stale, err := g.read(ctx, r, func(dir string) error {
		args := append([]string{"rev-list", "--count", "--end-of-options", sha + ".." + ref, "--"}, paths...)
		out, err := g.run(ctx, localTimeout, nil, dir, args...)
		if err != nil {
			return err
		}
		if behind, err = strconv.Atoi(strings.TrimSpace(out)); err != nil {
			return fmt.Errorf("rev-list answered %q", out)
		}
		_, err = g.run(ctx, localTimeout, nil, dir, "merge-base", "--is-ancestor", sha, ref)
		var ee *exitError
		switch {
		case err == nil:
			onRef = true
		case errors.As(err, &ee) && ee.code == 1:
			onRef = false
		default:
			return err
		}
		return nil
	})
	return behind, onRef, stale, err
}

// read runs fn in Huginn's copy of the remote, else in the user's clone,
// and reports stale when the clone answered.
func (g *Git) read(ctx context.Context, r ports.GitRepo, fn func(dir string) error) (stale bool, err error) {
	if url, err := g.RemoteURL(ctx, r); err == nil {
		cache := g.cachePath(url)
		if _, serr := os.Stat(filepath.Join(cache, "HEAD")); serr == nil {
			if err := fn(cache); err == nil {
				return false, nil
			}
		}
	}
	if err := fn(r.Dir); err != nil {
		return true, err
	}
	return true, nil
}

// cachePath is the folder of the copy of url.
func (g *Git) cachePath(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(g.CacheDir, hex.EncodeToString(sum[:8])+".git")
}

func (g *Git) lock(key string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locks == nil {
		g.locks = map[string]*sync.Mutex{}
	}
	l, ok := g.locks[key]
	if !ok {
		l = &sync.Mutex{}
		g.locks[key] = l
	}
	return l
}

// sshEnv makes ssh fail instead of asking for a passphrase or a host key,
// which would break the terminal UI, unless the user chose their own ssh
// command.
func (g *Git) sshEnv(ctx context.Context, dir string) []string {
	getenv := g.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("GIT_SSH_COMMAND") != "" || getenv("GIT_SSH") != "" {
		return nil
	}
	if out, err := g.run(ctx, localTimeout, nil, dir, "config", "--get", "core.sshCommand"); err == nil && strings.TrimSpace(out) != "" {
		return nil
	}
	return []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes"}
}

type exitError struct {
	code   int
	stderr string
}

func (e *exitError) Error() string { return fmt.Sprintf("git exited with %d: %s", e.code, e.stderr) }

// run runs git in dir (no -C when empty) with no input, so it can never
// prompt, and classifies its failure.
func (g *Git) run(ctx context.Context, timeout time.Duration, env []string, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin := g.Binary
	if bin == "" {
		bin = "git"
	}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var ee *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", domain.KindError(domain.ErrConfig, "git not found")
	case ctx.Err() != nil:
		return "", domain.KindError(domain.ErrUnreachable, "git timed out")
	case errors.As(err, &ee):
		return "", classify(&exitError{code: ee.ExitCode(), stderr: strings.TrimSpace(stderr.String())})
	}
	return "", err
}

// classify turns git's message into a domain error kind, keeping the exit
// error for callers that read the code.
func classify(e *exitError) error {
	msg := strings.ToLower(e.stderr)
	kind := error(nil)
	switch {
	case strings.Contains(msg, "could not resolve host"), strings.Contains(msg, "unable to access"),
		strings.Contains(msg, "connection timed out"), strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "network is unreachable"), strings.Contains(msg, "could not read from remote"):
		kind = domain.ErrUnreachable
	case strings.Contains(msg, "authentication failed"), strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "could not read username"), strings.Contains(msg, "host key verification failed"):
		kind = domain.ErrUnauthorized
	case strings.Contains(msg, "not a git repository"), strings.Contains(msg, "no such remote"),
		strings.Contains(msg, "unknown revision"), strings.Contains(msg, "bad revision"),
		strings.Contains(msg, "ambiguous"), strings.Contains(msg, "bad object"), strings.Contains(msg, "not a valid"),
		e.stderr == "" && e.code == 1:
		kind = domain.ErrNotFound
	}
	if kind == nil {
		return e
	}
	return fmt.Errorf("%w: %w", kind, e)
}

// Ignored reports whether file, in folder dir, is in a git work tree and,
// if so, whether git ignores it (bootstrap checks that the token's .env
// cannot be committed with a shared config folder).
func (g *Git) Ignored(ctx context.Context, dir, file string) (inRepo, ignored bool) {
	if _, err := g.run(ctx, localTimeout, nil, dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return false, false
	}
	_, err := g.run(ctx, localTimeout, nil, dir, "check-ignore", "--quiet", "--", file)
	return true, err == nil
}
