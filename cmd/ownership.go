package cmd

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/resolve"
)

// newOwnershipCmd builds the ownership command, which compares declared
// ownership against who actually has the expertise and reports the drift.
func newOwnershipCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ownership",
		Short: "Show where the owner on paper is not the one doing the work",
		Long: `Compare declared ownership, from a source of record such as CODEOWNERS, against
who actually has the expertise. A mismatch is ownership drift: the code says one
team or person owns an area, but the work and the knowledge sit somewhere else.

Deterministic over the graph, no model needed.

Examples:
  whodar ownership`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			drift := resolve.OwnershipDrift(ix)
			return opts.render(cmd.OutOrStdout(), map[string]any{"drift": drift}, func(w io.Writer, s style) {
				renderOwnership(w, drift, s)
			})
		},
	}
	return cmd
}
