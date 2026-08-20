package cmd

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/resolve"
)

// newRiskCmd builds the risk command, a deterministic view of where knowledge is
// concentrated in too few people, and what would leave with one of them.
func newRiskCmd(opts *options) *cobra.Command {
	var limit int
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
			if len(args) > 0 {
				who := strings.Join(args, " ")
				imp := resolve.Departure(ix, who)
				return opts.render(cmd.OutOrStdout(), imp, func(w io.Writer, s style) {
					renderDeparture(w, who, imp, s)
				})
			}
			report := resolve.Risk(ix, limit)
			return opts.render(cmd.OutOrStdout(), map[string]any{"topics": report}, func(w io.Writer, s style) {
				renderRisk(w, report, s)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum topics to report.")
	return cmd
}
