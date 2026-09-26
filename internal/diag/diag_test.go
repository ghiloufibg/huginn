package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisabledByDefault(t *testing.T) {
	log, c, path, err := Open(Options{Dir: t.TempDir()})
	if err != nil || path != "" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	log.Info("nothing")
	_ = c.Close()
}

func TestDebugEnvWritesRedactedFile(t *testing.T) {
	dir := t.TempDir()
	log, c, path, err := Open(Options{Debug: "1", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("connect", "context", "gke_x", "token", "abc123", "header", "Bearer xyz")
	_ = c.Close()
	if path != filepath.Join(dir, "huginn.log") {
		t.Fatalf("path %q", path)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, "context=gke_x") || strings.Contains(s, "abc123") || strings.Contains(s, "xyz") {
		t.Fatalf("log content: %s", s)
	}
}

func TestInvalidLevel(t *testing.T) {
	if _, _, _, err := Open(Options{Level: "loud", Dir: t.TempDir()}); err == nil {
		t.Fatal("expected error")
	}
}
