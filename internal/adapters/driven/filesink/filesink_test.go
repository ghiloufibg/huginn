package filesink

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func text(s string) func(io.Writer) error {
	return func(w io.Writer) error { _, err := io.WriteString(w, s); return err }
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	d := Dir{Path: dir}
	p1, err := d.Save(context.Background(), "repo-rec-20260930-194122.log", text("a line\n"))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := d.Save(context.Background(), "repo-rec-20260930-194122.log", text("another\n"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "repo-rec-20260930-194122.log" || filepath.Base(p2) != "repo-rec-20260930-194122-1.log" || !filepath.IsAbs(p1) {
		t.Fatalf("paths %s %s", p1, p2)
	}
	if b, _ := os.ReadFile(p1); string(b) != "a line\n" {
		t.Errorf("never overwritten: %q", b)
	}
	if info, _ := os.Stat(p1); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
}

func TestSaveFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := (Dir{Path: filepath.Join(dir, "missing")}).Save(context.Background(), "x.log", text("")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing directory: %v", err)
	}
	for _, name := range []string{"", "../x.log", "a/b.log", ".hidden"} {
		if _, err := (Dir{Path: dir}).Save(context.Background(), name, text("")); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	boom := errors.New("boom")
	_, err := (Dir{Path: dir}).Save(context.Background(), "half.log", func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("write error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "half.log")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed save leaves no file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Dir{Path: dir}).Save(ctx, "late.log", text("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}
