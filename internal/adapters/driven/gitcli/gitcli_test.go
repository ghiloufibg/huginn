package gitcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// sh runs git in dir and returns its trimmed output.
func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Jane", "GIT_AUTHOR_EMAIL=j@example.test",
		"GIT_COMMITTER_NAME=Jane", "GIT_COMMITTER_EMAIL=j@example.test", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes file in work and commits it with subject; returns the id.
func commit(t *testing.T, work, file, subject string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(work, filepath.Dir(file)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, file), []byte(subject), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, work, "add", "-A")
	sh(t, work, "commit", "-q", "-m", subject+"\n\nbody that is not read")
	return sh(t, work, "rev-parse", "HEAD")
}

// fixture is a "GitLab" (a bare repository), a developer's work tree that
// pushes to it, and the user's clone made before the last pushes.
type fixture struct {
	remote, work, clone string
	first, second       string // in the clone
	third, fourth       string // pushed after the clone was made
	git                 *Git
	repo                ports.GitRepo
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	f := fixture{remote: filepath.Join(root, "remote.git"), work: filepath.Join(root, "work"), clone: filepath.Join(root, "repos", "payment-service")}
	sh(t, root, "init", "-q", "--bare", "-b", "main", f.remote)
	sh(t, f.remote, "config", "uploadpack.allowFilter", "true")
	sh(t, root, "init", "-q", "-b", "main", f.work)
	sh(t, f.work, "remote", "add", "origin", "file://"+f.remote)
	f.first = commit(t, f.work, "services/payment/a.txt", "feat(payment): first")
	f.second = commit(t, f.work, "services/ledger/b.txt", "feat(ledger): second")
	sh(t, f.work, "tag", "-a", "v2.14.3", "-m", "release")
	sh(t, f.work, "push", "-q", "origin", "main", "--tags")
	sh(t, root, "clone", "-q", "file://"+f.remote, f.clone)
	f.third = commit(t, f.work, "services/payment/a.txt", "fix(payment): retry 3DS callback on timeout")
	f.fourth = commit(t, f.work, "services/ledger/b.txt", "chore(ledger): fourth")
	sh(t, f.work, "push", "-q", "origin", "main")
	f.git = &Git{CacheDir: filepath.Join(root, "cache"), Getenv: func(string) string { return "" }}
	f.repo = ports.GitRepo{Dir: f.clone, Remote: "origin"}
	return f
}

// state is everything git could change in the user's clone.
func state(t *testing.T, dir string) string {
	t.Helper()
	refs := sh(t, dir, "for-each-ref")
	objects := sh(t, dir, "count-objects", "-v")
	_, err := os.Stat(filepath.Join(dir, ".git", "FETCH_HEAD"))
	return refs + "\n" + objects + "\nFETCH_HEAD exists: " + map[bool]string{true: "yes", false: "no"}[err == nil]
}

func TestReadsTheRemoteAndNeverWritesTheClone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := state(t, f.clone)

	url, err := f.git.RemoteURL(ctx, f.repo)
	if err != nil || url != "file://"+f.remote {
		t.Fatalf("RemoteURL = %q, %v", url, err)
	}
	// Before any refresh, the clone answers, and says it may be stale.
	if c, stale, err := f.git.Commit(ctx, f.repo, f.second[:9]); err != nil || !stale || c.SHA != f.second {
		t.Errorf("Commit from the clone = %+v, %v, %v", c, stale, err)
	}
	if _, _, err := f.git.Commit(ctx, f.repo, f.third); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a commit pushed after the clone was found before refreshing: %v", err)
	}
	if err := f.git.Refresh(ctx, f.repo, false); err != nil {
		t.Fatal(err)
	}
	c, stale, err := f.git.Commit(ctx, f.repo, f.third[:8])
	if err != nil || stale || c.SHA != f.third || c.Subject != "fix(payment): retry 3DS callback on timeout" || c.Author != "Jane" || c.At.IsZero() {
		t.Errorf("Commit from the copy = %+v, stale %v, %v", c, stale, err)
	}
	behind, onRef, stale, err := f.git.Count(ctx, f.repo, f.first, "origin/HEAD", nil)
	if err != nil || behind != 3 || !onRef || stale {
		t.Errorf("Count = %d, %v, %v, %v; want 3 commits on origin/HEAD", behind, onRef, stale, err)
	}
	if sha, _, err := f.git.Tag(ctx, f.repo, "v2.14.3"); err != nil || sha != f.second {
		t.Errorf("Tag = %q, %v", sha, err)
	}
	if _, _, err := f.git.Tag(ctx, f.repo, "v9.9.9"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown tag: %v", err)
	}
	if after := state(t, f.clone); after != before {
		t.Errorf("the user's clone changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestCountByPaths(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.git.Refresh(ctx, f.repo, false); err != nil {
		t.Fatal(err)
	}
	// The commits-only copy is replaced by one with trees.
	if err := f.git.Refresh(ctx, f.repo, true); err != nil {
		t.Fatal(err)
	}
	behind, _, stale, err := f.git.Count(ctx, f.repo, f.first, "origin/HEAD", []string{"services/payment/"})
	if err != nil || behind != 1 || stale {
		t.Errorf("Count by paths = %d, stale %v, %v; want 1 (only the third touches payment)", behind, stale, err)
	}
}

func TestNotOnTrunk(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sh(t, f.work, "checkout", "-q", "-b", "hotfix", f.second)
	hotfix := commit(t, f.work, "hot.txt", "hotfix")
	sh(t, f.work, "push", "-q", "origin", "hotfix")
	if err := f.git.Refresh(ctx, f.repo, false); err != nil {
		t.Fatal(err)
	}
	behind, onRef, _, err := f.git.Count(ctx, f.repo, hotfix, "origin/HEAD", nil)
	if err != nil || onRef || behind != 2 {
		t.Errorf("Count = %d, onRef %v, %v; want 2 and not on the trunk", behind, onRef, err)
	}
}

func TestRemoteUnreachableFallsBackToTheClone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := os.RemoveAll(f.remote); err != nil {
		t.Fatal(err)
	}
	if err := f.git.Refresh(ctx, f.repo, false); err == nil {
		t.Fatal("refresh of a missing remote succeeded")
	}
	behind, _, stale, err := f.git.Count(ctx, f.repo, f.first, "origin/HEAD", nil)
	if err != nil || !stale || behind != 1 {
		t.Errorf("Count = %d, stale %v, %v; want 1 from the clone, stale", behind, stale, err)
	}
}

func TestRejectsOptionsAndBadInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.git.RemoteURL(ctx, ports.GitRepo{Dir: f.clone, Remote: "--upload-pack=touch"}); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("remote option: %v", err)
	}
	if _, _, err := f.git.Commit(ctx, f.repo, "--all"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("commit option: %v", err)
	}
	if _, _, _, err := f.git.Count(ctx, f.repo, f.first, "--all", nil); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("ref option: %v", err)
	}
	if _, _, _, err := f.git.Count(ctx, f.repo, f.first, "origin/HEAD", []string{"--output=x"}); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("path option: %v", err)
	}
	if _, err := f.git.RemoteURL(ctx, ports.GitRepo{Dir: filepath.Join(f.clone, "missing"), Remote: "origin"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing clone: %v", err)
	}
	if _, err := f.git.RemoteURL(ctx, ports.GitRepo{Dir: f.clone, Remote: "upstream"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing remote: %v", err)
	}
	if _, err := (&Git{Binary: "git-does-not-exist"}).RemoteURL(ctx, f.repo); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("missing git: %v", err)
	}
}

func TestBatchModeUnlessTheUserChoseAnSSHCommand(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if env := f.git.sshEnv(ctx, f.clone); len(env) != 1 || !strings.Contains(env[0], "BatchMode=yes") {
		t.Errorf("env = %v", env)
	}
	sh(t, f.clone, "config", "core.sshCommand", "ssh -i key")
	if env := f.git.sshEnv(ctx, f.clone); env != nil {
		t.Errorf("env = %v: the user's core.sshCommand must win", env)
	}
	g := &Git{Getenv: func(k string) string { return map[string]string{"GIT_SSH_COMMAND": "x"}[k] }}
	if env := g.sshEnv(ctx, f.work); env != nil {
		t.Errorf("env = %v: GIT_SSH_COMMAND must win", env)
	}
}

// TestProcessesPerLookup counts the git processes a minute of lookups
// costs: they are the adapter's whole cost.
func TestProcessesPerLookup(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls")
	wrapper := filepath.Join(dir, "git")
	script := "#!/bin/sh\necho \"$*\" >> " + logFile + "\nexec git \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.git.Binary = wrapper
	calls := func() []string {
		b, _ := os.ReadFile(logFile)
		_ = os.Remove(logFile)
		return strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' })
	}
	ctx := context.Background()
	if err := f.git.Refresh(ctx, f.repo, false); err != nil {
		t.Fatal(err)
	}
	first := calls()
	// A refresh within the hour: the fetch only (plus the ssh check), no set-head.
	if err := f.git.Refresh(ctx, f.repo, false); err != nil {
		t.Fatal(err)
	}
	again := calls()
	if slices.ContainsFunc(again, func(c string) bool { return strings.Contains(c, "set-head") }) || len(again) > 2 {
		t.Errorf("second refresh ran %d processes: %q", len(again), again)
	}
	// A lookup: the subject and the count, with the remote URL cached.
	if _, _, err := f.git.Commit(ctx, f.repo, f.third); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.git.Count(ctx, f.repo, f.third, "origin/HEAD", nil); err != nil {
		t.Fatal(err)
	}
	if lookup := calls(); len(lookup) != 2 {
		t.Errorf("a lookup ran %d processes, want 2 (log, rev-list): %q", len(lookup), lookup)
	}
	t.Logf("first refresh: %d processes", len(first))
}
