package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	dir := t.TempDir()
	if n := tokenNotice(dir, ".env"); n != "" {
		t.Errorf("no file: %q", n)
	}
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GITLAB_TOKEN=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	// Windows has no such permission bits: the check is Unix only.
	if n := tokenNotice(dir, ".env"); runtime.GOOS != "windows" && !strings.Contains(n, "readable by others (chmod 600") {
		t.Errorf("world-readable: %q", n)
	} else if runtime.GOOS == "windows" && n != "" {
		t.Errorf("Windows has no permission bits to warn about: %q", n)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if n := tokenNotice(dir, ".env"); n != "" {
		t.Errorf("private, not a git folder: %q", n)
	}
	// A config folder shared in git (it has a .git) must ignore the token
	// file; no git command is run to tell.
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if n := tokenNotice(dir, ".env"); !strings.Contains(n, "not git-ignored") {
		t.Errorf("not ignored: %q", n)
	}
	for _, gitignore := range []string{".env\n", "/.env\n", "# secrets\n*.env\n", ".env*\n", "*\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatal(err)
		}
		if n := tokenNotice(dir, ".env"); n != "" {
			t.Errorf(".gitignore %q: %q", gitignore, n)
		}
	}
	for _, gitignore := range []string{"", "*.log\n", ".env\n!.env\n", "sub/.env\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatal(err)
		}
		if n := tokenNotice(dir, ".env"); !strings.Contains(n, "not git-ignored") {
			t.Errorf(".gitignore %q does not ignore .env: %q", gitignore, n)
		}
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

func TestTokenReaderSeesARenewalOfTheSameSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("GITLAB_TOKEN=aaaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := tokenReader(path, ".env", "GITLAB_TOKEN")
	if tok, err := read(context.Background()); err != nil || tok.Reveal() != "aaaa" {
		t.Fatalf("token = %q, %v", tok.Reveal(), err)
	}
	if err := os.WriteFile(path, []byte("GITLAB_TOKEN=bbbb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if tok, err := read(context.Background()); err != nil || tok.Reveal() != "bbbb" {
		t.Errorf("renewed token = %q, %v", tok.Reveal(), err)
	}
}
