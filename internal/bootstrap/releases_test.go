package bootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/gitcli"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/diag"
)

func TestTokenReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	read := tokenReader(path, ".env", "GITLAB_TOKEN")
	if _, err := read(context.Background()); !errors.Is(err, domain.ErrUnauthorized) || err.Error() != "no GitLab token (.env GITLAB_TOKEN)" {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("# token\nOTHER=x\nGITLAB_TOKEN=\"glpat-abc\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := read(context.Background())
	if err != nil || tok.Reveal() != "glpat-abc" {
		t.Fatalf("token = %q, %v", tok.Reveal(), err)
	}
	// Read on each use: a token fixed while Huginn runs is used at once.
	if err := os.WriteFile(path, []byte("GITLAB_TOKEN=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(context.Background()); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("empty token: %v", err)
	}
}

func TestTokenNotice(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := &gitcli.Git{}
	if n := tokenNotice(git, dir, ".env"); n != "" {
		t.Errorf("no file: %q", n)
	}
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GITLAB_TOKEN=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := tokenNotice(git, dir, ".env"); !strings.Contains(n, "readable by others (chmod 600") {
		t.Errorf("world-readable: %q", n)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if n := tokenNotice(git, dir, ".env"); n != "" {
		t.Errorf("private, no git: %q", n)
	}
	// A config folder shared in git must ignore the token file.
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if n := tokenNotice(git, dir, ".env"); !strings.Contains(n, "not git-ignored") {
		t.Errorf("not ignored: %q", n)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := tokenNotice(git, dir, ".env"); n != "" {
		t.Errorf("ignored: %q", n)
	}
}

func TestDemoWiresReleases(t *testing.T) {
	app, err := Build(cli.Options{EnvArg: "rec", Demo: true}, noFiles(), diag.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if app.UI.Releases == nil || app.UI.Notice != "" {
		t.Fatalf("releases %v, notice %q: the demo folder has releases.yaml", app.UI.Releases, app.UI.Notice)
	}
	// Through the whole chain: the demo cluster's running digest is the
	// one the demo promote job printed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := app.UI.Catalog.Watch(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	snap := <-ch
	for _, svc := range snap.Services {
		if svc.Repo != "payment-service" {
			continue
		}
		var imgs []domain.WorkloadImage
		for _, w := range svc.WorkloadStates {
			cur, _ := domain.RunningImages(w, svc.Pods, app.UI.Filter)
			imgs = append(imgs, cur)
		}
		res, err := app.UI.Releases.Running(context.Background(), ports.ReleaseQuery{Env: "rec", Repo: svc.Repo, Images: imgs})
		if err != nil || len(res) != 2 {
			t.Fatalf("releases = %+v, %v", res, err)
		}
		for dg, r := range res {
			if r.State != domain.ReleaseFound || r.Release.Subject == "" || !r.Count.Known {
				t.Errorf("%s: %+v", dg, r)
			}
		}
		return
	}
	t.Fatal("no payment-service in the demo")
}
