package cmd

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/license"
	"github.com/kordloom/whodar/internal/util"
)

// signingKeyEnv holds the base64 private key that issues licenses. It is the
// licensor's key and is never present on a customer machine.
const signingKeyEnv = "WHODAR_LICENSE_SIGNING_KEY"

// newLicenseCmd builds the license command group.
func newLicenseCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Show which features this install is licensed for",
		Long: `Show which features this install is licensed for.

A license is a small signed file. whodar verifies it against a key compiled into
the binary and never contacts a server, so a licensed install works offline and
nothing about your organization leaves the machine.

Put the file at ` + license.FileName + ` in the data directory, or point
` + license.EnvLicense + ` at it. Without one, the free tier is in force: the
people graph, and recall pointing back at past conversations.

Free remembers your history: the people graph, and the conversations you took part
in. Memory preserves the organization's, keeping the words of Slack conversations
so an answer can show how something was fixed after the messages are gone. It is
$5,000 a year, flat per organization, with no seat count. Ask at hello@whodar.dev.`,
	}
	cmd.AddCommand(newLicenseStatusCmd(opts), newLicenseMintCmd())
	return cmd
}

// newLicenseStatusCmd builds the status subcommand.
func newLicenseStatusCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the tier in force and why",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			state := license.Resolve(opts.dataDir, time.Now())
			out := struct {
				// Tier is the feature set in force.
				Tier string `json:"tier"`
				// Org is who the license was issued to, when there is one.
				Org string `json:"org,omitempty"`
				// ID identifies the license in support conversations.
				ID string `json:"id,omitempty"`
				// Expires is when the license term ends.
				Expires string `json:"expires,omitempty"`
				// Reason explains the tier in a sentence.
				Reason string `json:"reason"`
			}{
				Tier:    string(state.Tier),
				Org:     state.License.Org,
				ID:      state.License.ID,
				Expires: expiryText(state.License.Expires),
				Reason:  state.Reason(),
			}
			return opts.render(cmd.OutOrStdout(), out, func(w io.Writer, s style) {
				renderLicense(w, string(state.Tier), state.License.Org, state.License.ID, expiryText(state.License.Expires), state.Reason(), s)
			})
		},
	}
}

// newLicenseMintCmd builds the hidden minting subcommand. It is useless
// without the licensor's private key, so it ships in the same binary rather
// than as a separate tool.
func newLicenseMintCmd() *cobra.Command {
	var (
		org     string
		tier    string
		id      string
		days    int
		outFile string
	)
	cmd := &cobra.Command{
		Use:    "mint",
		Short:  "Issue a signed license",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw := os.Getenv(signingKeyEnv)
			if raw == "" {
				return fmt.Errorf("%w: set %s to the base64 signing key", ErrBadArgs, signingKeyEnv)
			}
			key, err := base64.StdEncoding.DecodeString(raw)
			if err != nil || len(key) != ed25519.PrivateKeySize {
				return fmt.Errorf("%w: %s is not a base64 ed25519 private key", ErrBadArgs, signingKeyEnv)
			}
			now := time.Now().UTC()
			lic := license.License{
				ID:     id,
				Org:    org,
				Tier:   license.Tier(tier),
				Issued: now,
			}
			if days > 0 {
				lic.Expires = now.AddDate(0, 0, days)
			}
			if lic.ID == "" {
				lic.ID = fmt.Sprintf("whodar-%s", now.Format("20060102-150405"))
			}
			out, err := license.Sign(lic, ed25519.PrivateKey(key))
			if err != nil {
				return err
			}
			if outFile == "" {
				_, err = cmd.OutOrStdout().Write(out)
				return err
			}
			if err := util.WriteFileAtomic(outFile, out, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s for %s (%s)\n", outFile, org, tier)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&org, "org", "", "Organization the license is issued to.")
	f.StringVar(&tier, "tier", string(license.Memory), "Tier to grant.")
	f.StringVar(&id, "id", "", "License identifier; defaults to a timestamp.")
	f.IntVar(&days, "days", 365, "Term in days; 0 never expires.")
	f.StringVar(&outFile, "out", "", "Write the license here instead of stdout.")
	return cmd
}

// expiryText renders a license end date, or the empty string when it never
// expires.
func expiryText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}
