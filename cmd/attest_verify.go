package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/license"
)

// bundleLicense is what the licensing chain of a sealed finding amounts to:
// who the embedded license names, whether it verifies against the published
// keys, and whether it is bound to the key that signed this bundle.
type bundleLicense struct {
	// Evaluation is the flag inside the signed payload marking an unlicensed
	// run of the intelligence layer.
	Evaluation bool `json:"evaluation"`
	// Licensed reports whether a license is embedded and verifies.
	Licensed bool `json:"licensed"`
	// Bound reports whether the verified license names the bundle's own
	// sealing key, which is what makes the seal provably issued.
	Bound bool `json:"bound"`
	// Org, Tier, and Expires describe the verified license, when there is one.
	Org     string `json:"org,omitempty"`
	Tier    string `json:"tier,omitempty"`
	Expires string `json:"expires,omitempty"`
	// Problem says what failed, when something did.
	Problem string `json:"problem,omitempty"`
}

// newAttestVerifyCmd builds the verify subcommand: the licensing half of
// checking a sealed finding. The loomseal verifier proves the bundle is intact
// and internally consistent; this proves what the licensing chain claims, per
// the keys published at whodar.dev/verify.
func newAttestVerifyCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "verify FILE",
		Short: "Check the licensing chain inside a sealed finding",
		Long: `Check the licensing chain inside a LoomSeal bundle whodar produced: whether the
run was an evaluation, whether a license is embedded and verifies against the
published signing keys, and whether that license names the bundle's own sealing
key. Only the last state makes a seal provably issued to an organization.

This checks the licensing chain only. For the bundle's own integrity, run the
independent verifier as well:  loomseal verify FILE`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("%w: %w", ErrBadArgs, err)
			}
			result := checkBundleLicense(raw, time.Now())
			return opts.render(cmd.OutOrStdout(), result, func(w io.Writer, s style) {
				renderBundleLicense(w, result, s)
			})
		},
	}
}

// verifyLicense is swapped by tests, which cannot mint against the production
// signing key.
var verifyLicense = license.Verify

// checkBundleLicense extracts and judges the licensing chain of a sealed
// bundle. It never fails the whole check for a missing license, because an
// evaluation bundle is a valid thing to have; it reports what is there.
func checkBundleLicense(raw []byte, now time.Time) bundleLicense {
	var bundle struct {
		Producer struct {
			PublicKey string `json:"public_key"`
		} `json:"producer"`
		Claims []struct {
			Payload map[string]any `json:"payload"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return bundleLicense{Problem: "not a LoomSeal bundle: " + err.Error()}
	}
	if len(bundle.Claims) == 0 {
		return bundleLicense{Problem: "the bundle carries no claims"}
	}
	payload := bundle.Claims[0].Payload
	out := bundleLicense{}
	out.Evaluation, _ = payload["evaluation"].(bool)

	embedded, ok := payload["licensee"]
	if !ok {
		if !out.Evaluation {
			out.Problem = "neither an evaluation mark nor a license: not a whodar intelligence seal, or an old build"
		}
		return out
	}
	licRaw, err := json.Marshal(embedded)
	if err != nil {
		out.Problem = "embedded license does not re-encode: " + err.Error()
		return out
	}
	lic, err := verifyLicense(licRaw, now)
	if err != nil {
		out.Problem = "embedded license does not verify: " + err.Error()
		return out
	}
	out.Licensed = true
	out.Org, out.Tier, out.Expires = lic.Org, string(lic.Tier), expiryText(lic.Expires)
	out.Bound = lic.AttestKey != "" && lic.AttestKey == bundle.Producer.PublicKey
	if !out.Bound {
		out.Problem = "the license verifies but does not name this bundle's sealing key"
	}
	return out
}

// renderBundleLicense writes the licensing verdict for a person.
func renderBundleLicense(w io.Writer, r bundleLicense, s style) {
	switch {
	case r.Problem != "" && !r.Licensed:
		fmt.Fprintf(w, "%s %s\n", s.warn("problem:"), r.Problem)
	case r.Bound:
		fmt.Fprintf(w, "%s issued to %s (%s tier", s.accent("licensed:"), s.bold(r.Org), r.Tier)
		if r.Expires != "" {
			fmt.Fprintf(w, ", through %s", r.Expires)
		}
		fmt.Fprintln(w, ") and bound to this bundle's sealing key")
	case r.Licensed:
		fmt.Fprintf(w, "%s a license for %s verifies, but it is not bound to this bundle's key\n",
			s.warn("unbound:"), r.Org)
	}
	if r.Evaluation {
		fmt.Fprintln(w, s.dim("evaluation: this finding was produced by an unlicensed run; it is complete, and says so"))
	}
	fmt.Fprintln(w, s.dim("bundle integrity is the independent verifier's half: loomseal verify FILE"))
}
