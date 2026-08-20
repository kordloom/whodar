package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/policy"
	"github.com/kordloom/whodar/internal/vault"
)

// policyEnvVar names the environment variable pointing to an org policy file.
const policyEnvVar = "WHODAR_POLICY_FILE"

// defaultPolicyFile is the path an organization can pin a policy at.
const defaultPolicyFile = "/etc/whodar/policy.json"

// options holds the shared flags resolved on the root command.
type options struct {
	// dataDir is the directory holding the on-disk index.
	dataDir string
	// policyName is the requested egress policy mode.
	policyName string
	// pretty indents JSON output when true.
	pretty bool
	// jsonOut forces JSON output even at a terminal.
	jsonOut bool
	// humanOut forces human-readable output even when piped.
	humanOut bool
	// pol is the resolved egress policy, set before any subcommand runs.
	pol policy.Policy
	// systemPolicyFile is the org-pinned policy path, defaultPolicyFile in production.
	systemPolicyFile string
	// envPolicyFile is the WHODAR_POLICY_FILE override, read once at startup.
	envPolicyFile string
	// codecCache is the resolved at-rest codec, nil for plain JSON. It is set
	// once, lazily, so a passphrase entered during a load is reused for the save.
	codecCache vault.Codec
	// codecResolved reports whether codecCache has been resolved.
	codecResolved bool
}

// indexPath returns the index file path under the data directory.
func (o *options) indexPath() string {
	return filepath.Join(o.dataDir, "index.json")
}

// feedbackPath returns the feedback file path under the data directory. It is
// separate from the index so votes survive re-indexing.
func (o *options) feedbackPath() string {
	return filepath.Join(o.dataDir, "feedback.json")
}

// factsPath returns the facts file path under the data directory. It is
// separate from the index so recorded facts survive re-indexing.
func (o *options) factsPath() string {
	return filepath.Join(o.dataDir, "facts.json")
}

// refreshPath returns the refresh-config path under the data directory. It
// records the flags each source was last indexed with so `refresh` can replay
// them without the operator retyping the scope.
func (o *options) refreshPath() string {
	return filepath.Join(o.dataDir, "refresh.json")
}

// episodePath returns the episode file path under the data directory. Past
// conversations live beside the index rather than inside it, so a question
// about who knows something never pays to load them.
func (o *options) episodePath() string {
	return filepath.Join(o.dataDir, "episodes.json")
}

// statePath returns the incremental watermark file under the data directory. It
// is a plain sidecar holding only per-source timestamps and scope names, so it
// is never encrypted.
func (o *options) statePath() string {
	return filepath.Join(o.dataDir, "index.state.json")
}

// newRootCmd builds the root command, wires shared flags, and adds subcommands.
func newRootCmd() *cobra.Command {
	opts := &options{
		dataDir: defaultDataDir(), policyName: "strict", systemPolicyFile: defaultPolicyFile,
	}

	root := &cobra.Command{
		Use:           "whodar",
		Short:         "Know who knows",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			opts.envPolicyFile = os.Getenv(policyEnvVar)
			return opts.resolvePolicy(cmd.Flags().Changed("policy"), cmd.ErrOrStderr())
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&opts.dataDir, "data-dir", opts.dataDir, "Directory for the on-disk index.")
	pf.StringVar(&opts.policyName, "policy", opts.policyName, "Egress policy: strict, redacted, or open.")
	pf.BoolVar(&opts.pretty, "pretty", false, "Indent JSON output.")
	pf.BoolVar(&opts.jsonOut, "json", false, "Force JSON output, even at a terminal.")
	pf.BoolVar(&opts.humanOut, "human", false, "Force human-readable output, even when piped.")

	root.AddCommand(
		newIndexCmd(opts), newConnectCmd(opts), newAskCmd(opts), newRecallCmd(opts), newNearCmd(opts),
		newDirectoryCmd(opts), newIdentityCmd(opts), newSearchCmd(opts), newStatusCmd(opts), newDoctorCmd(opts), newServeCmd(opts), newBotCmd(opts),
		newFeedbackCmd(opts), newFactCmd(opts), newDemoCmd(opts), newMCPCmd(opts), newVaultCmd(opts),
		newRefreshCmd(opts), newScheduleCmd(opts),
		newArchiveCmd(opts), newLicenseCmd(opts), newVersionCmd())
	return root
}

// resolvePolicy sets o.pol from the org policy files and the user's flag. A
// locked system policy always wins, even when WHODAR_POLICY_FILE names another
// file, so the env var cannot unlock a pinned machine. Otherwise the env file
// replaces the system file entirely, a locked file pins its mode over the
// flag, and an unlocked file supplies the default the flag may override.
func (o *options) resolvePolicy(policyChanged bool, errOut io.Writer) error {
	cfg, found, err := policy.Load(o.systemPolicyFile)
	if err != nil {
		return err
	}
	systemLocked := found && cfg.Locked
	if systemLocked && o.envPolicyFile != "" {
		fmt.Fprintln(errOut, "whodar: "+policyEnvVar+" ignored; system policy is locked")
	}
	if !systemLocked && o.envPolicyFile != "" {
		if cfg, found, err = policy.Load(o.envPolicyFile); err != nil {
			return err
		}
	}
	if found && cfg.Locked {
		pol, err := cfg.Policy()
		if err != nil {
			return fmt.Errorf("org policy: %w", err)
		}
		o.pol = pol
		if policyChanged {
			fmt.Fprintln(errOut, "whodar: --policy ignored; pinned by org policy")
		}
		return nil
	}

	mode := o.policyName
	if found && !policyChanged && cfg.Mode != "" {
		mode = cfg.Mode
	}
	parsed, err := policy.ParseMode(mode)
	if err != nil {
		return err
	}
	// Start from the config so an unlocked file's private_channels pin survives,
	// then swap in the resolved mode. The base is unlocked here, so WithMode
	// only changes the mode.
	base := policy.Default()
	if found {
		if base, err = cfg.Policy(); err != nil {
			return fmt.Errorf("org policy: %w", err)
		}
	}
	o.pol, err = base.WithMode(parsed)
	return err
}

// defaultDataDir returns the default data directory under the user's home.
func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".whodar")
	}
	return ".whodar"
}
