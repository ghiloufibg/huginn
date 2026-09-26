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
)
