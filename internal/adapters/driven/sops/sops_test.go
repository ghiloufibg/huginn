package sops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func TestGetDecryptsOncePerFile(t *testing.T) {
	calls := 0
	var got []string
	p := &Provider{Dir: "/cfg", Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		got = append([]string{name}, args...)
		return []byte("# comment\nK8S_NAMESPACE=shop-prd\n\nDB_URL=postgres://u:p@h/db?x=1\n"), nil
	}}
	ctx := context.Background()
	ns, err := p.Get(ctx, ports.SecretRef{Source: "overlays/prd/config.env", Key: "K8S_NAMESPACE"})
	if err != nil || ns.Reveal() != "shop-prd" {
		t.Fatalf("got %q, %v", ns.Reveal(), err)
	}
	db, _ := p.Get(ctx, ports.SecretRef{Source: "overlays/prd/config.env", Key: "DB_URL"})
	if db.Reveal() != "postgres://u:p@h/db?x=1" || calls != 1 {
		t.Fatalf("db %q, %d sops runs", db.Reveal(), calls)
	}
	if want := filepath.Join("/cfg", "overlays/prd/config.env"); got[len(got)-1] != want || !strings.Contains(strings.Join(got, " "), "--decrypt") {
		t.Errorf("command %v", got)
	}
	if _, err := p.Get(ctx, ports.SecretRef{Source: "overlays/prd/config.env", Key: "MISSING"}); !errors.Is(err, domain.ErrSecretsAccess) {
		t.Errorf("missing key: %v", err)
	}
}

func TestDecryptionFailure(t *testing.T) {
	p := &Provider{Run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("failed to get the data key")
	}}
	file := filepath.Join(t.TempDir(), "config.env") // absolute on every OS
	_, err := p.Get(context.Background(), ports.SecretRef{Source: file, Key: "K"})
	if !errors.Is(err, domain.ErrSecretsAccess) || !strings.Contains(err.Error(), "data key") || !strings.Contains(err.Error(), file) {
		t.Fatalf("err = %v", err)
	}
}

func TestSopsNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (&Provider{}).Get(context.Background(), ports.SecretRef{Source: filepath.Join(t.TempDir(), "x.env"), Key: "K"})
	if !errors.Is(err, domain.ErrSecretsAccess) || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestSummary(t *testing.T) {
	report := `Failed to get the data key required to decrypt the SOPS file.

Group 0: FAILED
  age14rm26: FAILED
    - | failed to load age identities: failed to open
      | SOPS_AGE_KEY_FILE file: open /nonexistent: no such file or
      | directory

Recovery failed because no master key was able to decrypt the file.`
	want := "Failed to get the data key required to decrypt the SOPS file. · failed to load age identities: failed to open SOPS_AGE_KEY_FILE file: open /nonexistent: no such file or directory"
	if got := summary(report); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := summary("error loading config: no matching creation rules found"); got != "error loading config: no matching creation rules found" {
		t.Fatalf("one line: %q", got)
	}
}
