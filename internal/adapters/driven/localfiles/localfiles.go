// Package localfiles implements ports.LocalFiles: it reads the dotenv
// sources and truststores a Kafka profile names on the user's workstation
// (docs/plan/M11-kafka.md). Decrypted content and certificates stay in
// memory; reads are bounded in size, globs in the entries they visit.
package localfiles

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Defaults of the limits.
const (
	DefaultMaxFileBytes   = 4 << 20
	DefaultMaxGlobEntries = 100_000
)

// Files reads local files for Kafka profiles.
type Files struct {
	// Secrets decrypts the encrypted sources (sops).
	Secrets ports.SecretFiles
	// MaxFileBytes bounds the size of a file read. Default 4 MiB.
	MaxFileBytes int64
	// MaxGlobEntries bounds the folder entries one glob visits, so a
	// pattern such as /** fails fast instead of walking the disk. Default
	// 100 000.
	MaxGlobEntries int
}

var _ ports.LocalFiles = (*Files)(nil)

// ReadEnv reads a dotenv file, decrypted with sops first when encrypted is
// set. The plaintext is cleared once parsed.
func (f *Files) ReadEnv(ctx context.Context, path string, encrypted bool) (map[string]domain.Secret, error) {
	var (
		b   []byte
		err error
	)
	if encrypted {
		if f.Secrets == nil {
			return nil, fmt.Errorf("%s: no decryption available: %w", path, domain.ErrSecretsAccess)
		}
		b, err = f.Secrets.Decrypt(ctx, path, "dotenv")
	} else {
		b, err = f.read(path)
	}
	if err != nil {
		return nil, err
	}
	defer clear(b)
	return domain.ParseDotenv(b), nil
}

// ReadTrustStore returns the DER certificates of a truststore, by its
// extension: PEM (.pem, .crt, .cer) or PKCS12 (.p12, .pfx). A PKCS12 file
// holding a key and its chain instead of trusted entries gives its
// certificates.
func (f *Files) ReadTrustStore(_ context.Context, path string, password domain.Secret) ([][]byte, error) {
	b, err := f.read(path)
	if err != nil {
		return nil, err
	}
	var certs [][]byte
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".pem", ".crt", ".cer":
		certs, err = pemCerts(b)
	case ".p12", ".pfx":
		certs, err = pkcs12Certs(b, password.Reveal())
	default:
		err = fmt.Errorf("unknown truststore type %q: use .pem, .crt, .cer, .p12 or .pfx: %w", ext, domain.ErrConfig)
	}
	if err != nil {
		return nil, fmt.Errorf("truststore %s: %w", path, err)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("truststore %s: no certificate found: %w", path, domain.ErrConfig)
	}
	return certs, nil
}

func pemCerts(b []byte) ([][]byte, error) {
	var out [][]byte
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			return out, nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, fmt.Errorf("invalid certificate: %v: %w", err, domain.ErrConfig)
		}
		out = append(out, block.Bytes)
	}
}

func pkcs12Certs(b []byte, password string) ([][]byte, error) {
	certs, err := pkcs12.DecodeTrustStore(b, password)
	if errors.Is(err, pkcs12.ErrIncorrectPassword) {
		return nil, fmt.Errorf("wrong password: %w", domain.ErrConfig)
	}
	if err != nil {
		// A keystore (a key and its chain) also lists its certificates.
		key, leaf, chain, kerr := pkcs12.DecodeChain(b, password)
		if kerr != nil {
			return nil, fmt.Errorf("not a Java truststore nor a keystore (%v); a PKCS12 file made by openssl without the Java trust attribute is not read: convert it with `openssl pkcs12 -in <file> -nokeys -out ca.pem` and use the PEM file: %w", err, domain.ErrConfig)
		}
		_ = key // the private key of a keystore is not needed
		certs = append([]*x509.Certificate{leaf}, chain...)
	}
	out := make([][]byte, 0, len(certs))
	for _, c := range certs {
		out = append(out, c.Raw)
	}
	return out, nil
}

// read returns the content of a regular file of at most MaxFileBytes.
func (f *Files) read(path string) ([]byte, error) {
	limit := f.MaxFileBytes
	if limit <= 0 {
		limit = DefaultMaxFileBytes
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s not found: %w", path, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file: %w", path, domain.ErrConfig)
	}
	b, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if int64(len(b)) > limit {
		clear(b)
		return nil, fmt.Errorf("%s is larger than %d bytes: %w", path, limit, domain.ErrConfig)
	}
	return b, nil
}

// Glob returns the existing regular files matching pattern, sorted. Each
// path element is matched with filepath.Match; "**" matches zero or more
// folders, never descending into hidden folders or through symbolic links.
func (f *Files) Glob(ctx context.Context, pattern string) ([]string, error) {
	pattern = filepath.Clean(pattern)
	if !hasMeta(pattern) {
		if info, err := os.Stat(pattern); err == nil && info.Mode().IsRegular() {
			return []string{pattern}, nil
		}
		return nil, nil
	}
	root, segs := splitPattern(pattern)
	for _, s := range segs {
		if _, err := filepath.Match(s, ""); err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, domain.ErrConfig)
		}
	}
	limit := f.MaxGlobEntries
	if limit <= 0 {
		limit = DefaultMaxGlobEntries
	}
	g := &globber{ctx: ctx, budget: limit, found: map[string]bool{}}
	if err := g.walk(root, segs); err != nil {
		if errors.Is(err, errTooBroad) {
			return nil, fmt.Errorf("glob %q visits more than %d entries; make it more precise: %w", pattern, limit, domain.ErrConfig)
		}
		return nil, err
	}
	out := make([]string, 0, len(g.found))
	for p := range g.found {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}

func hasMeta(s string) bool { return strings.ContainsAny(s, `*?[`) }

// splitPattern returns the longest leading folder without meta
// characters and the remaining path elements.
func splitPattern(pattern string) (string, []string) {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	i := 0
	for i < len(parts)-1 && !hasMeta(parts[i]) {
		i++
	}
	root := filepath.FromSlash(strings.Join(parts[:i], "/"))
	switch {
	case root == "" && strings.HasPrefix(pattern, string(filepath.Separator)):
		root = string(filepath.Separator)
	case root == "":
		root = "."
	case strings.HasSuffix(root, ":"): // a Windows volume
		root += string(filepath.Separator)
	}
	return root, parts[i:]
}

var errTooBroad = errors.New("glob too broad")

type globber struct {
	ctx    context.Context
	budget int
	found  map[string]bool
}

func (g *globber) walk(dir string, segs []string) error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if segs[0] == "**" {
		if len(segs) == 1 {
			return nil // "**" names folders, not files
		}
		if err := g.walk(dir, segs[1:]); err != nil {
			return err
		}
		return g.each(dir, func(e fs.DirEntry, p string) error {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				return g.walk(p, segs)
			}
			return nil
		})
	}
	return g.each(dir, func(e fs.DirEntry, p string) error {
		if ok, _ := filepath.Match(segs[0], e.Name()); !ok {
			return nil
		}
		info, err := os.Stat(p) // follows a link named explicitly
		if err != nil {
			return nil
		}
		switch {
		case len(segs) == 1 && info.Mode().IsRegular():
			g.found[p] = true
		case len(segs) > 1 && info.IsDir():
			return g.walk(p, segs[1:])
		}
		return nil
	})
}

func (g *globber) each(dir string, fn func(fs.DirEntry, string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // a missing or unreadable folder matches nothing
	}
	for _, e := range entries {
		g.budget--
		if g.budget < 0 {
			return errTooBroad
		}
		if err := fn(e, filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
