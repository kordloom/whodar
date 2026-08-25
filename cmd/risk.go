package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/report"
	"github.com/kordloom/whodar/internal/resolve"
)

// newRiskCmd builds the risk command, a deterministic view of where knowledge is
// concentrated in too few people, and what would leave with one of them.
func newRiskCmd(opts *options) *cobra.Command {
	var (
		limit    int
		htmlPath string
	)
	cmd := &cobra.Command{
		Use:   "risk [person]",
		Short: "Show where knowledge is dangerously concentrated",
		Long: `Score knowledge concentration across the graph: the topics where one or two
people hold most of the expertise, so a single departure is visible before it
hurts. Name a person for the offboarding view: what leaves with them.

Deterministic arithmetic over the graph, no model needed.

Examples:
  whodar risk
  whodar risk "Angela Malone"`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			if htmlPath != "" {
				if len(args) > 0 {
					return fmt.Errorf("%w: --html writes the whole-company brief, so it takes no person",
						ErrBadArgs)
				}
				// A brief is read, not searched. Every subject is still
				// scored and still counted in the figures it leads with, but a
				// large organization has thousands of them and listing all of
				// them makes a document nobody opens twice. --limit 0 lists
				// every one for somebody who wants the whole table.
				capped := briefRows
				if cmd.Flags().Changed("limit") {
					capped = limit
				}
				return writeRiskHTML(cmd, ix, htmlPath, capped)
			}
			if len(args) > 0 {
				who := strings.Join(args, " ")
				imp := resolve.Departure(ix, who)
				return opts.render(cmd.OutOrStdout(), imp, func(w io.Writer, s style) {
					renderDeparture(w, who, imp, s)
				})
			}
			report := resolve.Risk(ix, limit)
			regions := resolve.Regions(ix, regionsShown)
			spans := resolve.SoleSpans(ix, spansShown)
			view := map[string]any{"topics": report, "regions": regions, "spans": spans}
			return opts.render(cmd.OutOrStdout(), view, func(w io.Writer, s style) {
				renderRisk(w, report, s)
				renderRegions(w, regions, s)
				renderSpans(w, spans, s)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum topics to report.")
	cmd.Flags().StringVar(&htmlPath, "html", "",
		"Write a self-contained HTML brief to this path, for sending to somebody without whodar.")
	return cmd
}

// regionsShown is how many joined bodies of work the report names. They are
// the largest findings it has, so a handful is enough to act on.
const regionsShown = 8

// spansShown is how many one-person connections the report names.
const spansShown = 8

// briefRows is how many subjects a brief lists before it stops, unless asked
// for a different number. The figures above the table always count every scored
// subject, so this shortens the reading without shrinking the finding.
const briefRows = 100

// writeRiskHTML renders the knowledge-risk brief to a file. It renders into
// memory first so a template failure leaves no half-written report behind.
func writeRiskHTML(cmd *cobra.Command, ix *index.Index, path string, limit int) error {
	// Score everything, then cap only what is listed. A brief that quietly
	// counted just the rows it printed would understate the finding it is for.
	all := resolve.Risk(ix, 0)
	exposed := report.Exposures(all)
	listed := all
	if limit > 0 && len(listed) > limit {
		listed = listed[:limit]
	}
	brief := report.Brief{
		Generated: time.Now(),
		People:    len(ix.Graph.People),
		Scored:    len(all),
		Sources:   ix.SourceNames(),
		Risks:     listed,
		Regions:   resolve.Regions(ix, regionsShown),
		Spans:     resolve.SoleSpans(ix, spansShown),
		Totals:    report.Count(all, exposed),
		Exposed:   exposed,
	}
	var buf bytes.Buffer
	if err := report.WriteRisk(&buf, brief); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("risk: write %s: %w", path, err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "whodar: wrote the knowledge-risk brief to %s\n", path)
	return nil
}
