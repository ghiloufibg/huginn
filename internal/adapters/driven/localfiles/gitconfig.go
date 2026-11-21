package localfiles

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// GitRemote implements ports.GitRemotes: the URL of remote in the clone at
// dir, read from its git config file. No git command runs
// (docs/plan/M16.1-commits-from-gitlab-api.md §4.2). The ".git" entry may
// be a folder or, for a worktree or a submodule, a file naming the folder
// ("gitdir: …"); a worktree's config is the main repository's
// ("commondir"). "url.<base>.insteadOf" rewrites of the same file are
// applied; include and includeIf are not followed.
func (f *Files) GitRemote(_ context.Context, dir, remote string) (string, error) {
	gitDir, err := f.gitDir(dir)
	if err != nil {
		return "", err
	}
	b, err := f.read(filepath.Join(gitDir, "config"))
	if err != nil {
		return "", err
	}
	url, rewrites := parseGitConfig(b, remote)
	if url == "" {
		return "", domain.KindError(domain.ErrNotFound, fmt.Sprintf("the clone %s has no remote %q", dir, remote))
	}
	return rewrite(url, rewrites), nil
}

// gitDir returns the folder holding the clone's config.
func (f *Files) gitDir(dir string) (string, error) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", domain.KindError(domain.ErrNotFound, dir+" is not a clone")
	case err != nil:
		return "", fmt.Errorf("cannot read %s: %w", dotGit, err)
	case info.IsDir():
		return dotGit, nil
	}
	b, err := f.read(dotGit)
	if err != nil {
		return "", err
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", domain.KindError(domain.ErrNotFound, dotGit+" names no git folder")
	}
	gitDir := strings.TrimSpace(target)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil { // a worktree
		common := strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		return common, nil
	}
	return gitDir, nil
}

// parseGitConfig returns the first url of [remote "<remote>"] and the
// insteadOf rewrites (prefix → base) of the file.
func parseGitConfig(b []byte, remote string) (url string, rewrites map[string]string) {
	rewrites = map[string]string{}
	var section, sub string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			section, sub = parseSection(line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), configValue(value)
		switch {
		case section == "remote" && sub == remote && key == "url" && url == "":
			url = value
		case section == "url" && key == "insteadof" && value != "":
			rewrites[value] = sub
		}
	}
	return url, rewrites
}

// parseSection reads `[name "sub"]` (or `[name]`); the name is not case
// sensitive, the subsection is.
func parseSection(line string) (name, sub string) {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	name, rest, _ := strings.Cut(inner, " ")
	if s, err := strconv.Unquote(strings.TrimSpace(rest)); err == nil {
		sub = s
	}
	return strings.ToLower(strings.TrimSpace(name)), sub
}

// configValue reads a value as git does: a comment (# or ;) outside
// double quotes ends it, the quotes themselves are removed, and \" and \\
// are unescaped.
func configValue(v string) string {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' && i+1 < len(v):
			i++
			b.WriteByte(v[i])
		case c == '"':
			quoted = !quoted
		case !quoted && (c == '#' || c == ';'):
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}

// rewrite applies the longest matching insteadOf prefix, as git does.
func rewrite(url string, rewrites map[string]string) string {
	best := ""
	for prefix := range rewrites {
		if strings.HasPrefix(url, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return url
	}
	return rewrites[best] + strings.TrimPrefix(url, best)
}
