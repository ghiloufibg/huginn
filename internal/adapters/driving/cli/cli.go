// Package cli is the command-line driving adapter: it parses arguments and
// flags into Options and hands them to the handlers supplied by the
// composition root. It knows nothing about how the application is built.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Options are the parsed launch options.
type Options struct {
	// EnvArg is the positional environment ("huginn prd"), EnvFlag the
	// value of -e/--env; the composition root resolves precedence.
	EnvArg, EnvFlag string
	Repo            string
	Since           string
	ConfigPath      string
	Demo            bool
	Theme           string
	LogLevel        string
}

// Handlers are supplied by the composition root.
type Handlers struct {
	// Run starts the TUI.
	Run func(ctx context.Context, o Options) error
	// ConfigExample returns the documented example configuration.
	ConfigExample func() []byte
	// ConfigValidate validates configuration data read from name.
	ConfigValidate func(data []byte, name string) error
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
		Example:       "  huginn               # default environment (rec unless configured)\n  huginn prd\n  huginn -e dev --repo payment-service --since 1h\n  huginn --demo        # synthetic cluster, no credentials needed",
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
	f.StringVarP(&o.EnvFlag, "env", "e", "", "environment to open (dev, rec, prprd, prd, or any configured name)")
	f.StringVar(&o.Repo, "repo", "", "open the logs of this repository directly")
	f.StringVar(&o.Since, "since", "", "initial time window, e.g. 15m, 1h, 2d or tail")
	f.StringVar(&o.ConfigPath, "config", "", "config file (default: $HUGINN_CONFIG or the user config directory)")
	f.BoolVar(&o.Demo, "demo", false, "use a synthetic in-memory cluster (no credentials needed)")
	f.StringVar(&o.Theme, "theme", "", "color theme: light, accessible, classic or none (NO_COLOR forces none)")
	f.StringVar(&o.LogLevel, "log-level", "", "write Huginn's diagnostic log at this level (debug, info, warn, error)")
	root.SetVersionTemplate("huginn {{.Version}}\n")
	root.AddCommand(configCommand(h))
	return root
}

func configCommand(h Handlers) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect and validate the configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "example",
		Short: "Print the documented example configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(h.ConfigExample())
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "validate <file|->",
		Short: "Validate a configuration file ('-' reads standard input)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, name, err := readInput(cmd.InOrStdin(), args[0])
			if err != nil {
				return err
			}
			if err := h.ConfigValidate(data, name); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: configuration is valid\n", name)
			return err
		},
	})
	return cmd
}

func readInput(stdin io.Reader, arg string) ([]byte, string, error) {
	if arg == "-" {
		b, err := io.ReadAll(stdin)
		return b, "stdin", err
	}
	b, err := os.ReadFile(arg)
	return b, arg, err
}
