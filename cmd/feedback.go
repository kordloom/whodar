package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/index"
)

// newFeedbackCmd builds the feedback command group: record a vote, review
// what has been recorded, and clear votes that no longer apply.
func newFeedbackCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Confirm, correct, review, or clear answer feedback",
		Long: `Votes teach the ranking: a helpful vote lifts a result for that question and
its close variants, a not-helpful vote lowers it, capped so feedback tunes
answers without burying the evidence. Votes live in feedback.json next to the
index and survive re-indexing.

  whodar feedback record "billing retries" --person alice@corp.com --helpful
  whodar feedback list
  whodar feedback clear --person alice@corp.com`,
	}
	cmd.AddCommand(newFeedbackRecordCmd(opts), newFeedbackListCmd(opts), newFeedbackClearCmd(opts))
	return cmd
}

// newFeedbackRecordCmd builds the record subcommand.
func newFeedbackRecordCmd(opts *options) *cobra.Command {
	var (
		person     string
		channel    string
		comment    string
		helpful    bool
		notHelpful bool
	)
	cmd := &cobra.Command{
		Use:   "record [question]",
		Short: "Record a vote on an answer",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if helpful == notHelpful {
				return fmt.Errorf("%w: pass exactly one of --helpful or --not-helpful", ErrBadArgs)
			}
			vote := feedback.Helpful
			if notHelpful {
				vote = feedback.NotHelpful
			}
			store, err := feedback.Load(opts.feedbackPath())
			if err != nil {
				return err
			}
			entry := feedback.Entry{
				Query:   strings.Join(args, " "),
				Person:  person,
				Channel: channel,
				Vote:    vote,
				Comment: strings.TrimSpace(comment),
				Time:    time.Now(),
			}
			if err := store.Add(entry); err != nil {
				return fmt.Errorf("%w: %w", ErrBadArgs, err)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "feedback recorded")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&person, "person", "", "Person identifier from the answer.")
	f.StringVar(&channel, "channel", "", "Channel name from the answer.")
	f.StringVar(&comment, "comment", "", "Optional note explaining the vote.")
	f.BoolVar(&helpful, "helpful", false, "The result answered the question.")
	f.BoolVar(&notHelpful, "not-helpful", false, "The result was wrong for the question.")
	return cmd
}

// newFeedbackListCmd builds the list subcommand.
func newFeedbackListCmd(opts *options) *cobra.Command {
	var filter feedback.Filter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show recorded votes as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := feedback.Load(opts.feedbackPath())
			if err != nil {
				return err
			}
			entries := store.List(filter)
			if entries == nil {
				entries = []feedback.Entry{}
			}
			return writeJSON(cmd.OutOrStdout(), entries, opts.pretty)
		},
	}
	addFeedbackFilterFlags(cmd, &filter)
	return cmd
}

// newFeedbackClearCmd builds the clear subcommand.
func newFeedbackClearCmd(opts *options) *cobra.Command {
	var (
		filter feedback.Filter
		all    bool
	)
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Remove recorded votes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !all && filter == (feedback.Filter{}) {
				return fmt.Errorf("%w: pass --query, --person, --channel, or --all", ErrBadArgs)
			}
			store, err := feedback.Load(opts.feedbackPath())
			if err != nil {
				return err
			}
			removed, err := store.Clear(filter)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "cleared %d votes\n", removed)
			return nil
		},
	}
	addFeedbackFilterFlags(cmd, &filter)
	cmd.Flags().BoolVar(&all, "all", false, "Clear every recorded vote.")
	return cmd
}

// addFeedbackFilterFlags registers the shared vote filter flags.
func addFeedbackFilterFlags(cmd *cobra.Command, filter *feedback.Filter) {
	f := cmd.Flags()
	f.StringVar(&filter.Query, "query", "", "Match votes for this exact question.")
	f.StringVar(&filter.Person, "person", "", "Match votes on this person identifier.")
	f.StringVar(&filter.Channel, "channel", "", "Match votes on this channel name.")
}

// feedbackStrengths maps the --feedback presets to a per-vote multiplier and
// a net-vote clamp.
var feedbackStrengths = map[string]struct {
	// Step is the per-vote score multiplier.
	Step float64
	// MaxNet clamps net votes per result; negative disables feedback.
	MaxNet int
}{
	"off":    {Step: 1, MaxNet: -1},
	"low":    {Step: 1.1, MaxNet: 2},
	"normal": {Step: 1.25, MaxNet: 3},
	"high":   {Step: 1.5, MaxNet: 4},
}

// applyFeedbackStrength configures how hard votes move ranking.
func applyFeedbackStrength(ix *index.Index, name string) error {
	if name == "" {
		name = "normal"
	}
	s, ok := feedbackStrengths[name]
	if !ok {
		return fmt.Errorf("%w: feedback strength %q (want off, low, normal, or high)", ErrBadArgs, name)
	}
	ix.SetFeedbackStrength(s.Step, s.MaxNet)
	return nil
}

// applyFeedback loads stored votes onto the index, warning instead of failing
// when the feedback file is unreadable. It returns the store, or nil when the
// file could not be read.
func applyFeedback(ix *index.Index, opts *options, errOut io.Writer) *feedback.Store {
	store, err := feedback.Load(opts.feedbackPath())
	if err != nil {
		fmt.Fprintf(errOut, "feedback ignored: %v\n", err)
		return nil
	}
	ix.SetFeedback(store.All())
	return store
}
