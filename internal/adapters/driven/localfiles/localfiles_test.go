package localfiles

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func write(t *testing.T, path, data string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGlob(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"repo/deploy/overlays/rec/secrets/kafka.env",
		"repo/deploy/overlays/prd/secrets/kafka.env",
		"repo/src/main/resources/truststore.p12",
		"repo/.git/hooks/truststore.p12",
		"repo/a/b/c/truststore.p12",
	} {
		write(t, filepath.Join(root, p), "x")
	}
	f := &Files{}
	ctx := context.Background()
	rel := func(ps []string) []string {
		out := []string{}
		for _, p := range ps {
			r, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(r))
		}
		return out
	}
	cases := []struct {
		pattern string
		want    []string
	}{
		{"repo/deploy/overlays/rec/secrets/kafka.env", []string{"repo/deploy/overlays/rec/secrets/kafka.env"}},
		{"repo/deploy/overlays/qa/secrets/kafka.env", []string{}},
		{"repo/deploy/overlays/*/secrets/*.env", []string{"repo/deploy/overlays/prd/secrets/kafka.env", "repo/deploy/overlays/rec/secrets/kafka.env"}},
		{"repo/**/truststore.p12", []string{"repo/a/b/c/truststore.p12", "repo/src/main/resources/truststore.p12"}},
		{"repo/**/resources/*.p12", []string{"repo/src/main/resources/truststore.p12"}},
		{"repo/.git/*/truststore.p12", []string{"repo/.git/hooks/truststore.p12"}},
		{"repo/deploy/overlays", []string{}}, // a folder is not a file
		{"repo/**", []string{}},
	}
	for _, c := range cases {
		got, err := f.Glob(ctx, filepath.Join(root, filepath.FromSlash(c.pattern)))
		if err != nil || !slices.Equal(rel(got), c.want) {
			t.Errorf("%s: got %v %v, want %v", c.pattern, rel(got), err, c.want)
		}
	}
	if _, err := f.Glob(ctx, filepath.Join(root, "[x/*")); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("invalid glob: %v", err)
	}
	small := &Files{MaxGlobEntries: 3}
	if _, err := small.Glob(ctx, filepath.Join(root, "**", "*.env")); !errors.Is(err, domain.ErrConfig) || !strings.Contains(err.Error(), "more precise") {
		t.Errorf("too broad: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.Glob(cancelled, filepath.Join(root, "**", "*.env")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestGlobDoesNotFollowLinkLoops(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "x.env"), "x")
	if err := os.Symlink(root, filepath.Join(root, "a", "loop")); err != nil {
		t.Skip("symbolic links unavailable:", err)
	}
	got, err := (&Files{}).Glob(context.Background(), filepath.Join(root, "**", "x.env"))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v %v", got, err)
	}
}

type fakeSops struct {
	plain []byte
	err   error
	calls []string
}

func (s *fakeSops) Decrypt(_ context.Context, path, format string) ([]byte, error) {
	s.calls = append(s.calls, path+" "+format)
	return s.plain, s.err
}

func TestReadEnv(t *testing.T) {
	dir := t.TempDir()
	plain := write(t, filepath.Join(dir, "base.env"), "export BROKERS=b1:9093,b2:9093\nPASS='p#1'\n")
	sops := &fakeSops{plain: []byte("PASS=secret\n")}
	f := &Files{Secrets: sops}
	ctx := context.Background()
	got, err := f.ReadEnv(ctx, plain, false)
	if err != nil || got["BROKERS"].Reveal() != "b1:9093,b2:9093" || got["PASS"].Reveal() != "p#1" {
		t.Fatalf("plain: %v %v", got, err)
	}
	got, err = f.ReadEnv(ctx, "/x/secrets.env", true)
	if err != nil || got["PASS"].Reveal() != "secret" || sops.calls[0] != "/x/secrets.env dotenv" {
		t.Fatalf("encrypted: %v %v %v", got, err, sops.calls)
	}
	if string(sops.plain) != strings.Repeat("\x00", len("PASS=secret\n")) {
		t.Errorf("the plaintext is cleared after parsing: %q", sops.plain)
	}
	if _, err := f.ReadEnv(ctx, filepath.Join(dir, "missing.env"), false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := (&Files{}).ReadEnv(ctx, "/x.env", true); !errors.Is(err, domain.ErrSecretsAccess) {
		t.Errorf("no sops: %v", err)
	}
	sops.err = errors.New("sops cannot decrypt: " + domain.ErrSecretsAccess.Error())
	if _, err := f.ReadEnv(ctx, "/x.env", true); err == nil {
		t.Error("decryption failure is returned")
	}
	big := write(t, filepath.Join(dir, "big.env"), strings.Repeat("A=1\n", 100))
	if _, err := (&Files{MaxFileBytes: 64}).ReadEnv(ctx, big, false); !errors.Is(err, domain.ErrConfig) || !strings.Contains(err.Error(), "larger than 64 bytes") {
		t.Errorf("too large: %v", err)
	}
	if _, err := f.ReadEnv(ctx, dir, false); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("a folder: %v", err)
	}
}

func selfSigned(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

func TestReadTrustStore(t *testing.T) {
	dir := t.TempDir()
	ca1, key := selfSigned(t, "ca-1")
	ca2, _ := selfSigned(t, "ca-2")
	ctx := context.Background()
	f := &Files{}
	pw := domain.NewSecret("s3cret")

	javaStore, err := pkcs12.Modern.EncodeTrustStore([]*x509.Certificate{ca1, ca2}, pw.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	p12 := write(t, filepath.Join(dir, "truststore.p12"), string(javaStore))
	if got, err := f.ReadTrustStore(ctx, p12, pw); err != nil || len(got) != 2 {
		t.Fatalf("java truststore: %d %v", len(got), err)
	}
	if _, err := f.ReadTrustStore(ctx, p12, domain.NewSecret("wrong")); !errors.Is(err, domain.ErrConfig) || !strings.Contains(err.Error(), "wrong password") {
		t.Errorf("wrong password: %v", err)
	}

	keyStore, err := pkcs12.Modern.Encode(key, ca1, []*x509.Certificate{ca2}, pw.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	pfx := write(t, filepath.Join(dir, "keystore.PFX"), string(keyStore))
	if got, err := f.ReadTrustStore(ctx, pfx, pw); err != nil || len(got) != 2 {
		t.Fatalf("keystore: %d %v", len(got), err)
	}

	pemData := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca1.Raw})) + "junk\n" + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca2.Raw}))
	if got, err := f.ReadTrustStore(ctx, write(t, filepath.Join(dir, "ca.pem"), pemData), domain.Secret{}); err != nil || len(got) != 2 {
		t.Fatalf("pem: %d %v", len(got), err)
	}
	for name, data := range map[string]string{"empty.crt": "no certificate here", "junk.p12": "not pkcs12", "ca.jks": "x"} {
		if _, err := f.ReadTrustStore(ctx, write(t, filepath.Join(dir, name), data), pw); !errors.Is(err, domain.ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.ReadTrustStore(ctx, filepath.Join(dir, "none.p12"), pw); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestReadTrustStoreWithoutJavaAttribute(t *testing.T) {
	// Made with: openssl pkcs12 -export -nokeys -in ca.pem -out
	// openssl-truststore.p12 -passout pass:test. Certificate bags without
	// the Java trust attribute cannot be decoded: the message says how to
	// convert the file.
	_, err := (&Files{}).ReadTrustStore(context.Background(), filepath.Join("testdata", "openssl-truststore.p12"), domain.NewSecret("test"))
	if !errors.Is(err, domain.ErrConfig) || !strings.Contains(err.Error(), "-nokeys -out ca.pem") {
		t.Fatalf("err = %v", err)
	}
}
