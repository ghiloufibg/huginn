// Package sops implements ports.SecretsProvider with the sops command:
// the user's existing sops setup (age, GCP KMS, …) decrypts, Huginn only
// reads the output in memory (docs/DECISIONS.md D-003). Decrypted content
// is never written to disk nor logged.
package sops

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Provider decrypts sops-encrypted dotenv files, once per file.
type Provider struct {
	// Dir resolves relative sources (the config folder).
	Dir string
	// Run runs a command and returns its standard output (tests replace
	// it). Default: exec.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)

	mu    sync.Mutex
	files map[string]map[string]domain.Secret
}

// Get returns the value of ref.Key in the file ref.Source.
func (p *Provider) Get(ctx context.Context, ref ports.SecretRef) (domain.Secret, error) {
	path := ref.Source
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Dir, path)
	}
	values, err := p.file(ctx, path)
	if err != nil {
		return domain.Secret{}, err
	}
	v, ok := values[ref.Key]
	if !ok {
		return domain.Secret{}, fmt.Errorf("%s has no key %s: %w", ref.Source, ref.Key, domain.ErrSecretsAccess)
	}
	return v, nil
}

func (p *Provider) file(ctx context.Context, path string) (map[string]domain.Secret, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.files[path]; ok {
		return v, nil
	}
	run := p.Run
	if run == nil {
		run = execute
	}
	out, err := run(ctx, "sops", "--decrypt", "--input-type", "dotenv", "--output-type", "dotenv", path)
	if err != nil {
		return nil, fmt.Errorf("sops cannot decrypt %s: %v: %w", path, err, domain.ErrSecretsAccess)
	}
	values := parseDotenv(out)
	clear(out)
	if p.files == nil {
		p.files = map[string]map[string]domain.Secret{}
	}
	p.files[path] = values
	return values, nil
}

// execute runs the command, keeping its output in memory. Its standard
// error explains a failure (no key, file not found) without secrets.
func execute(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("the sops command is not installed (https://github.com/getsops/sops)")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(summary(msg))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// summary reduces sops' error report to one line: its first line and the
// cause of each failed key ("- | failed to load age identities: …").
func summary(report string) string {
	lines := strings.Split(report, "\n")
	out := strings.TrimSpace(lines[0])
	var cause []string
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(l, "- |"); ok {
			cause = append(cause, "·"+rest)
		} else if rest, ok := strings.CutPrefix(l, "|"); ok {
			cause = append(cause, rest)
		}
	}
	if len(cause) > 0 {
		out += " " + strings.Join(strings.Fields(strings.Join(cause, " ")), " ")
	}
	return out
}

// parseDotenv reads KEY=VALUE lines (sops' dotenv output: no quoting, no
// export); comments and blank lines are skipped.
func parseDotenv(b []byte) map[string]domain.Secret {
	out := map[string]domain.Secret{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = domain.NewSecret(v)
	}
	return out
}
