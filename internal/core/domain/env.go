package domain

import (
	"fmt"
	"regexp"
)

// Env is the name of a deployment environment such as "rec" or "prd".
// Environment names are defined by the config folder only.
type Env string

var envNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ParseEnv validates an environment name. It accepts any lower-case
// DNS-label-like name so that other companies can define their own
// environments; membership in the configured set is checked by config.
func ParseEnv(s string) (Env, error) {
	if !envNamePattern.MatchString(s) {
		return "", fmt.Errorf("invalid environment name %q: use lower-case letters, digits and '-'", s)
	}
	return Env(s), nil
}

// String returns the environment name.
func (e Env) String() string { return string(e) }
