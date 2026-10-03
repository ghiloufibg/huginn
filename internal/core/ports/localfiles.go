package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// LocalFiles reads the files of the user's workstation that a Kafka
// profile names: its dotenv sources, its truststore, and the files whose
// existence decides whether the profile applies. Paths are absolute.
type LocalFiles interface {
	// Glob returns the existing regular files matching pattern, sorted.
	// "**" matches any number of folders (hidden folders excepted); a
	// pattern without meta characters returns the file if it exists.
	Glob(ctx context.Context, pattern string) ([]string, error)
	// ReadEnv reads a dotenv file, decrypted with sops first when
	// encrypted is set.
	ReadEnv(ctx context.Context, path string, encrypted bool) (map[string]domain.Secret, error)
	// ReadTrustStore returns the DER certificates of a PEM or PKCS12
	// truststore.
	ReadTrustStore(ctx context.Context, path string, password domain.Secret) ([][]byte, error)
}
