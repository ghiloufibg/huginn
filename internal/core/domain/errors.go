package domain

import "errors"

// Error kinds that adapters map their failures to, so the UI can show an
// actionable message without inspecting library errors. Use errors.Is.
var (
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
	ErrUnreachable    = errors.New("unreachable")
	ErrNotFound       = errors.New("not found")
	ErrNotImplemented = errors.New("not implemented")
	ErrSecretsAccess  = errors.New("secrets unavailable")
	// ErrNotStarted: a container has not started yet (image pull,
	// creation), so it has no logs; it may start later.
	ErrNotStarted = errors.New("not started")
	// ErrConfig: the local setup is wrong (an unknown kube context, an
	// unreadable kubeconfig). Retrying cannot help; the user must fix it.
	ErrConfig = errors.New("configuration")
)

// Permanent reports errors that retrying cannot fix.
func Permanent(err error) bool {
	return errors.Is(err, ErrConfig) || errors.Is(err, ErrNotImplemented)
}
