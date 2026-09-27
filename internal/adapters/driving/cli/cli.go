// Package cli is the command-line driving adapter: it parses arguments and
// flags into Options and hands them to the handlers supplied by the
// composition root. It knows nothing about how the application is built.
package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// Options are the parsed launch options.
type Options struct {
	// EnvArg is the positional environment ("huginn prd"), EnvFlag the
	// value of -e/--env; the composition root resolves precedence.
	EnvArg, EnvFlag string
	Repo            string
	Since           string
	Containers      string
	ConfigPath      string
	Demo            bool
	Theme           string
	LogLevel        string
}

// Handlers are supplied by the composition root.
type Handlers struct {
	// Run starts the TUI.
	Run func(ctx context.Context, o Options) error
}

// NewRootCommand builds the huginn command tree.
func NewRootCommand(h Handlers, version string) *cobra.Command {
	var o Options
	root := &cobra.Command{
		Use:   "huginn [env]",
		Short: "Read-only terminal UI for Kubernetes pod logs",
		Long: "Huginn — Odin's raven of thought: it flies out to your pods and brings back what they are saying.\n\n" +
			"A keyboard-driven, read-only log viewer for application pods on Kubernetes (GKE).\n" +
			"The environment can be given as an argument (huginn prd) or with -e/--env.",
		Example: "  huginn --config ~/work/acme-huginn rec\n  huginn prd                         # config folder from $HUGINN_CONFIG or the user config directory\n" +
			"  huginn -e dev --repo payment-service --since 1h\n  huginn --demo                      # synthetic cluster and the embedded example folder",
		Version:       version,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.EnvArg = args[0]
			}
			return h.Run(cmd.Context(), o)
		},
	}
	f := root.Flags()
	f.StringVarP(&o.EnvFlag, "env", "e", "", "environment to open: a name of environments.yaml")
	f.StringVar(&o.Repo, "repo", "", "open the logs of this repository directly")
	f.StringVar(&o.Since, "since", "", "initial time window, e.g. 15m, 1h, 2d, tail (last lines) or head (first lines); tail:N and head:N set the size")
	f.StringVar(&o.Containers, "containers", "", "containers the logs open on: app (application) or all (sidecars too); default from containers.yaml")
	f.StringVar(&o.ConfigPath, "config", "", "config folder (default: $HUGINN_CONFIG, else <user config dir>/huginn); see docs/CONFIG.md")
	f.BoolVar(&o.Demo, "demo", false, "use a synthetic in-memory cluster and, without --config, the embedded example folder")
	f.StringVar(&o.Theme, "theme", "", "color theme: light, accessible, classic or none (NO_COLOR forces none)")
	f.StringVar(&o.LogLevel, "log-level", "", "write Huginn's diagnostic log at this level (debug, info, warn, error)")
	root.SetVersionTemplate("huginn {{.Version}}\n")
	return root
}
