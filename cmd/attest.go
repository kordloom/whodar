package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/attest"
	"github.com/kordloom/whodar/internal/resolve"
)

// newAttestCmd builds the attest command: a signed, offline-verifiable LoomSeal
// bundle of the knowledge-risk finding, so a board, auditor, or acquirer can
// trust the finding without trusting the machine that produced it.
func newAttestCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attest",
		Short: "Seal the knowledge-risk finding into an offline-verifiable LoomSeal bundle",
		Long: `Produce a LoomSeal evidence bundle for the current knowledge-risk finding: the
claim, a digest of the evidence behind it, and an ed25519 signature. Anyone can
verify it offline with the loomseal verifier, with no account and nothing sent
to us, so the finding can be trusted without trusting the machine that made it.

The signing key is created once under the data directory and reused.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			priv, err := opts.attestKey()
			if err != nil {
				return err
			}
			report := resolve.Risk(ix, 0)
			payload, evidence := attestPayload(report)
			pub, _ := priv.Public().(ed25519.PublicKey)
			bundle, err := attest.Seal(priv, "whodar", version, attest.InstallID(pub),
				"whodar.knowledge-risk/1", map[string]any{"id": "organization", "type": "fleet"},
				payload, evidence, time.Now())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(bundle))
			return err
		},
	}
	return cmd
}

// attestPayload summarizes the risk report as the claim payload and returns the
// full report as the evidence bytes the claim digests.
func attestPayload(report []resolve.TopicRisk) (any, []byte) {
	critical := 0
	var top []any
	for _, t := range report {
		if t.Level != "critical" {
			continue
		}
		critical++
		if len(top) < 10 {
			expert := ""
			if len(t.Experts) > 0 {
				expert = t.Experts[0].Name
			}
			top = append(top, map[string]any{"topic": t.Topic, "bus_factor": t.BusFactor, "sole_expert": expert})
		}
	}
	payload := map[string]any{
		"finding":       "knowledge concentration",
		"topics_scored": len(report),
		"critical":      critical,
		"top_critical":  top,
	}
	evidence, _ := json.Marshal(report)
	return payload, evidence
}

// attestKey loads the ed25519 signing key from the data directory, creating it
// on first use. It is the persistent identity every attestation is signed with.
func (o *options) attestKey() (ed25519.PrivateKey, error) {
	path := filepath.Join(o.dataDir, "attest.key")
	if seed, err := os.ReadFile(path); err == nil && len(seed) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(seed), nil
	}
	fresh := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("attest: generate key: %w", err)
	}
	// Name the directory in both failures. The default sits under the home
	// directory, and a hardened service unit usually cannot reach that, so the
	// message has to say which path was refused or the remedy is invisible.
	if err := os.MkdirAll(o.dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("attest: cannot create %s: %w", o.dataDir, err)
	}
	if err := os.WriteFile(path, fresh, 0o600); err != nil {
		return nil, fmt.Errorf("attest: cannot save the key in %s: %w", o.dataDir, err)
	}
	return ed25519.NewKeyFromSeed(fresh), nil
}
