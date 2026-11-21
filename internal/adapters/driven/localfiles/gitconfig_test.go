package localfiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const sampleConfig = `[core]
	repositoryformatversion = 0
	bare = false
[remote "upstream"]
	url = git@gitlab.example.test:fork/payment-service.git
[Remote "origin"]
	url = "git@gitlab.example.test:payments/payment-service.git" ; the main one
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "short"]
	url = gl:payments/short.git
[url "git@gitlab.example.test:"]
	insteadOf = gl:
[branch "main"]
	remote = origin
`

func TestGitRemote(t *testing.T) {
	root := t.TempDir()
	clone := filepath.Join(root, "payment-service")
	writeFile(t, filepath.Join(clone, ".git", "config"), sampleConfig)
	f := &Files{}
	ctx := context.Background()
	for remote, want := range map[string]string{
		"origin":   "git@gitlab.example.test:payments/payment-service.git",
		"upstream": "git@gitlab.example.test:fork/payment-service.git",
		"short":    "git@gitlab.example.test:payments/short.git",
	} {
		if got, err := f.GitRemote(ctx, clone, remote); err != nil || got != want {
			t.Errorf("GitRemote(%s) = %q, %v; want %q", remote, got, err, want)
		}
	}
	if _, err := f.GitRemote(ctx, clone, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing remote: %v", err)
	}
	if _, err := f.GitRemote(ctx, filepath.Join(root, "not-a-clone"), "origin"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("not a clone: %v", err)
	}
}

func TestGitRemoteWorktreeAndSubmodule(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	writeFile(t, filepath.Join(main, ".git", "config"), sampleConfig)
	// A worktree: .git is a file naming .git/worktrees/<name>, whose
	// commondir leads back to the main .git, which holds the config.
	wt := filepath.Join(root, "feature")
	writeFile(t, filepath.Join(main, ".git", "worktrees", "feature", "commondir"), "../..\n")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "feature")+"\n")
	// A submodule: .git is a file with a relative gitdir holding its config.
	sub := filepath.Join(main, "libs", "common")
	writeFile(t, filepath.Join(main, ".git", "modules", "common", "config"), "[remote \"origin\"]\n\turl = https://gitlab.example.test/libs/common.git\n")
	writeFile(t, filepath.Join(sub, ".git"), "gitdir: ../../.git/modules/common\n")
	f := &Files{}
	if got, err := f.GitRemote(context.Background(), wt, "origin"); err != nil || got != "git@gitlab.example.test:payments/payment-service.git" {
		t.Errorf("worktree: %q, %v", got, err)
	}
	if got, err := f.GitRemote(context.Background(), sub, "origin"); err != nil || got != "https://gitlab.example.test/libs/common.git" {
		t.Errorf("submodule: %q, %v", got, err)
	}
}
