package bootstrap

import (
	"fmt"
	"strings"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Environment variables read at launch.
const (
	EnvEnv     = "HUGINN_ENV"
	EnvTheme   = "HUGINN_THEME"
	EnvNoColor = "NO_COLOR"
)

// ResolveEnv picks the environment: --env, then the positional argument,
// then HUGINN_ENV, then huginn.yaml default_env. Giving different values as flag and
// argument is an error, as is a name that is not configured.
func ResolveEnv(o cli.Options, getenv func(string) string, c *config.Config) (domain.Env, error) {
	if o.EnvFlag != "" && o.EnvArg != "" && o.EnvFlag != o.EnvArg {
		return "", fmt.Errorf("environment given twice: argument %q and --env %q", o.EnvArg, o.EnvFlag)
	}
	name := firstNonEmpty(o.EnvFlag, o.EnvArg, getenv(EnvEnv), c.Huginn.DefaultEnv)
	if _, ok := c.Environments.ByName[name]; !ok {
		return "", fmt.Errorf("unknown environment %q (environments.yaml has: %s)", name, strings.Join(c.Environments.Names, ", "))
	}
	return domain.ParseEnv(name)
}

// ResolveTheme picks the theme: --theme, then NO_COLOR (forces "none"),
// then HUGINN_THEME, then ui.yaml theme.
func ResolveTheme(flag string, getenv func(string) string, c *config.Config) string {
	if flag != "" {
		return flag
	}
	if getenv(EnvNoColor) != "" {
		return "none"
	}
	return firstNonEmpty(getenv(EnvTheme), c.UI.Theme)
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
