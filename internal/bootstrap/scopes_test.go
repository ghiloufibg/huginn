package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

type fakeSecrets struct {
	values map[string]string
	err    error
	calls  int
}

func (f *fakeSecrets) Get(_ context.Context, ref ports.SecretRef) (domain.Secret, error) {
	f.calls++
	if f.err != nil {
		return domain.Secret{}, f.err
	}
	v, ok := f.values[ref.Source+"#"+ref.Key]
	if !ok {
		return domain.Secret{}, domain.ErrSecretsAccess
	}
	return domain.NewSecret(v), nil
}

func TestScopesResolveNamespaceFrom(t *testing.T) {
	c := &config.Config{Environments: config.Environments{Names: []string{"rec", "prd"}, ByName: map[string]config.Environment{
		"rec": {Context: "ctx", Namespaces: []string{"shop-rec"}},
		"prd": {Context: "ctx", NamespaceFrom: "sops:overlays/prd/config.env#K8S_NAMESPACE"},
	}}}
	secrets := &fakeSecrets{values: map[string]string{"overlays/prd/config.env#K8S_NAMESPACE": " shop-prd\n"}}
	sc := scopes(c, secrets)
	ctx := context.Background()
	for range 2 {
		s, err := sc(ctx, "prd")
		if err != nil || len(s.Namespaces) != 1 || s.Namespaces[0] != "shop-prd" || s.Context != "ctx" {
			t.Fatalf("prd scope %+v, err %v", s, err)
		}
	}
	if s, _ := sc(ctx, "rec"); s.Namespaces[0] != "shop-rec" || secrets.calls != 1 {
		t.Fatalf("rec scope %+v, %d decryptions", s, secrets.calls)
	}
	if _, err := sc(ctx, "dev"); err == nil {
		t.Fatal("unknown environment")
	}

	failing := scopes(c, &fakeSecrets{err: errors.New("sops cannot decrypt: no key: " + domain.ErrSecretsAccess.Error())})
	_, err := failing(ctx, "prd")
	if err == nil || !strings.Contains(err.Error(), "environments.yaml: environments.prd.namespace_from") {
		t.Fatalf("err = %v", err)
	}
}
