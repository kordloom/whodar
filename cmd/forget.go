package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/model"
)

// newForgetCmd builds the forget command, which purges one person from
// everything whodar stores.
func newForgetCmd(opts *options) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "forget <email or name>",
		Short: "Purge a person from the index and remembered conversations",
		Long: `Purge a person from everything whodar stores on this machine.

Their records are removed from the index under every identity they were known
by, references to them are stripped from channel membership and org relations,
their retained conversation notes are deleted, and conversations where they
were the only participant are forgotten entirely. A conversation with other
participants keeps its pointer and loses its searchable words, since theirs
cannot be separated out.

The purge covers the index and conversations. Two caveats:
  - feedback.json holds questions people asked; prune it by deleting the file.
  - Re-indexing a source that still contains the person brings them back. To
    keep them out, remove them at the source or stop indexing it.

Examples:
  whodar forget jane@corp.com
  whodar forget "Jane Roe" --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			if err := opts.loadSources(ix); err != nil {
				return err
			}
			person := ix.FindPerson(args[0])
			if person == nil {
				return fmt.Errorf("%w: no person matches %q", ErrBadArgs, args[0])
			}
			out := cmd.ErrOrStderr()
			label := person.Name
			if person.Email != "" {
				label += " <" + person.Email + ">"
			}
			if !yes {
				fmt.Fprintf(out, "This purges %s and %d linked identities from the index "+
					"and remembered conversations.\nProceed? [y/N]: ", label, len(person.Identities))
				var answer string
				if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil ||
					!strings.EqualFold(strings.TrimSpace(answer), "y") {
					fmt.Fprintln(out, "left everything as it was")
					return nil
				}
			}

			// Collect the identity set before the purge rewrites the graph.
			purgeIDs := append([]model.ID{person.ID}, person.Identities...)

			res := ix.Forget(person)
			if err := opts.saveIndex(ix); err != nil {
				return err
			}

			store, err := opts.loadEpisodes(cmd)
			if err != nil {
				return err
			}
			dropped, edited := 0, 0
			for _, id := range purgeIDs {
				d, e := store.ForgetPerson(id)
				dropped += d
				edited += e
			}
			if err := opts.saveEpisodes(store); err != nil {
				return err
			}

			fmt.Fprintf(out,
				"forgot %s: %d records removed, %d mentions stripped, "+
					"%d conversations dropped, %d edited\n",
				label, res.Records, res.Mentions, dropped, edited)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Purge without asking.")
	return cmd
}
