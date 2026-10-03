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

// SecretFiles decrypts whole encrypted files in memory, for callers that
// need every key of a file (the sources of a Kafka profile).
// Implementations must never write the plaintext to disk or logs; callers
// clear it once parsed.
type SecretFiles interface {
	// Decrypt returns the plaintext of the file at path, whose format is
	// given ("dotenv").
	Decrypt(ctx context.Context, path, format string) ([]byte, error)
}
