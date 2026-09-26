package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// SecretRef points at one value in a secret store, for example the key
// K8S_NAMESPACE in the sops-encrypted file overlays/rec/config.env.
type SecretRef struct {
	Source string
	Key    string
}

// SecretsProvider reads secret values in memory only. Implementations must
// never write decrypted content to disk or logs.
type SecretsProvider interface {
	Get(ctx context.Context, ref SecretRef) (domain.Secret, error)
}
