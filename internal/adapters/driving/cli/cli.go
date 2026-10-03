// Package cli is the command-line driving adapter: it parses arguments and
// flags into Options and hands them to the handlers supplied by the
// composition root. It knows nothing about how the application is built.
package cli

import (
	"context"
	"fmt"
	"io"

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

// KafkaOptions are the options of the kafka subcommands.
type KafkaOptions struct {
	Options
	Repo, Topic string
	// Since is a window such as 15m; Tail the records per partition
	// otherwise (0: the kafka.tail_records of huginn.yaml).
	Since     string
	Tail      int
	Follow    bool
	Committed bool
	// Raw prints each value as received, one per line (for jq), instead
	// of a line per record.
	Raw bool
}

// Handlers are supplied by the composition root.
type Handlers struct {
	// Run starts the TUI.
	Run func(ctx context.Context, o Options) error
	// KafkaCheck resolves a repository's Kafka profile and lists its
	// topics; KafkaRead prints the records of one topic. Both write to w.
	KafkaCheck func(ctx context.Context, o KafkaOptions, w io.Writer) error
	KafkaRead  func(ctx context.Context, o KafkaOptions, w io.Writer) error
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
	f.StringVar(&o.Theme, "theme", "", "color theme: auto (from the terminal's background), light, dark, accessible, classic or none (NO_COLOR forces none)")
	f.StringVar(&o.LogLevel, "log-level", "", "write Huginn's diagnostic log at this level (debug, info, warn, error)")
	root.SetVersionTemplate("huginn {{.Version}}\n")
	root.AddCommand(kafkaCommand(h))
	return root
}

// kafkaCommand is "huginn kafka": the Kafka screens' reading, without the
// TUI and without a Kubernetes cluster, to check a profile or read a topic
// from a shell. Read only, like the screens.
func kafkaCommand(h Handlers) *cobra.Command {
	var o KafkaOptions
	k := &cobra.Command{
		Use:   "kafka",
		Short: "Check a repository's Kafka profile or read a topic, read only (no TUI, no Kubernetes)",
		Long: "Reads Kafka the way the Kafka screens do (docs/CONFIG.md, kafka/): never joins a consumer group, never\n" +
			"commits, never produces. Useful to check a profile, or to read a topic from a shell or a script.",
	}
	pf := k.PersistentFlags()
	pf.StringVarP(&o.EnvFlag, "env", "e", "", "environment: a name of environments.yaml (default: default_env)")
	pf.StringVar(&o.ConfigPath, "config", "", "config folder (default: $HUGINN_CONFIG, else <user config dir>/huginn)")
	pf.BoolVar(&o.Demo, "demo", false, "generated records and, without --config, the embedded example folder")
	pf.StringVar(&o.LogLevel, "log-level", "", "write Huginn's diagnostic log at this level (debug, info, warn, error)")
	check := &cobra.Command{
		Use:     "check <repo>",
		Short:   "Show which profile applies to a repository and whether each of its topics can be read",
		Example: "  huginn kafka check payment-service -e rec",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Repo = args[0]
			return h.KafkaCheck(cmd.Context(), o, cmd.OutOrStdout())
		},
	}
	read := &cobra.Command{
		Use:   "read <repo> <topic>",
		Short: "Print the records of a topic: the last of each partition, a window, or live ones",
		Example: "  huginn kafka read payment-service payments.requested --tail 20\n" +
			"  huginn kafka read payment-service payments.requested --since 1h --raw | jq .\n" +
			"  huginn kafka read payment-service payments.requested --follow",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Repo, o.Topic = args[0], args[1]
			if o.Since != "" && cmd.Flags().Changed("tail") {
				return fmt.Errorf("--since and --tail: give one of them")
			}
			return h.KafkaRead(cmd.Context(), o, cmd.OutOrStdout())
		},
	}
	rf := read.Flags()
	rf.StringVar(&o.Since, "since", "", "records since this long ago, e.g. 15m, 1h, 2d")
	rf.IntVar(&o.Tail, "tail", 0, "last records of each partition (default: kafka.tail_records)")
	rf.BoolVarP(&o.Follow, "follow", "f", false, "keep printing new records until interrupted")
	rf.BoolVar(&o.Committed, "committed", false, "committed records only (read_committed isolation)")
	rf.BoolVar(&o.Raw, "raw", false, "print each value as received, one per line: line breaks inside a value are written \\n, a tombstone null, control characters escaped on a terminal")
	k.AddCommand(check, read)
	return k
}
