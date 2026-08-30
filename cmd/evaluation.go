package cmd

import (
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
