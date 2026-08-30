package cmd

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/kordloom/whodar/internal/license"
)

// riskEvaluation reports whether the intelligence layer is running unlicensed,
// which is allowed and complete but labeled: a leader has to see the finding
// before anyone budgets for it, and the label is what separates a look from a
// deliverable.
func riskEvaluation(opts *options) bool {
	return !license.Resolve(opts.dataDir, time.Now()).Has(license.Risk)
}

// renderEvaluationNote writes the evaluation label under a risk or ownership
// report. It goes to the human rendering only; JSON carries the same fact as
// an "evaluation" field instead.
func renderEvaluationNote(w io.Writer, s style) {
	fmt.Fprintf(w, "\n%s\n", s.dim(
		"Evaluation. Findings are complete and nothing is held back; a license marks\n"+
			"sealed reports as licensed. Early access: whodar.dev/pricing"))
}

// sealLicensee returns the signed license to embed in a sealed finding, or
// nil. It embeds only when the install is licensed for the intelligence layer
// AND the license names this install's sealing key: an unbound license proves
// somebody bought one, a bound license proves THIS seal came from the install
// it was issued to, and only the second is provenance.
func sealLicensee(opts *options, pub ed25519.PublicKey) any {
	state := license.Resolve(opts.dataDir, time.Now())
	if !state.Has(license.Risk) || len(state.Raw) == 0 {
		return nil
	}
	if state.License.AttestKey != base64.StdEncoding.EncodeToString(pub) {
		return nil
	}
	var signed any
	if err := json.Unmarshal(state.Raw, &signed); err != nil {
		return nil
	}
	return signed
}
