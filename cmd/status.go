package cmd

import (
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/license"
)

// newStatusCmd builds the status command: one view of what the index holds and
// how fresh it is, so a user need not re-run an index to answer "when did I last
// index?", "what is in here?", or "is it encrypted?".
func newStatusCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what the index holds and how fresh it is",
		Long: `Report the index: when it was last built, how many people, channels, teams,
and topics it holds, how many records each source contributed, whether it carries
embeddings, whether a key is set to encrypt it at rest, and the license tier.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			codec, err := opts.codec()
			if err != nil {
				return err
			}
			sources := make(map[string]int)
			for _, name := range ix.SourceNames() {
				sources[name] = ix.SourceSize(name)
			}
			view := map[string]any{
				"index":                     opts.indexPath(),
				"people":                    len(ix.Graph.People),
				"channels":                  len(ix.Graph.Channels),
				"teams":                     len(ix.Graph.Teams),
				"topics":                    len(ix.Graph.Topics),
				"sources":                   sources,
				"embeddings":                ix.HasEmbeddings(),
				"encryption_key_configured": codec != nil,
				"license":                   license.Resolve(opts.dataDir, time.Now()).Reason(),
			}
			if t := ix.BuiltAt(); !t.IsZero() {
				view["built_at"] = t.Format(time.RFC3339)
				view["age"] = time.Since(t).Round(time.Minute).String()
			}
			return opts.render(cmd.OutOrStdout(), view, func(w io.Writer, s style) {
				renderStatus(w, view, s)
			})
		},
	}
	return cmd
}
