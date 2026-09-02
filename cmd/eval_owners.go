package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/ownersbench"
)

// newEvalOwnersCmd builds the eval owners subcommand, which scores whodar's
// ownership claim against a repository's own OWNERS files.
func newEvalOwnersCmd(opts *options) *cobra.Command {
	var (
		repo         string
		sinceDays    int
		minCommits   int
		topK         int
		maxLeafShare int
		useIndex     bool
		tallyPath    string
		jsonOut      bool
	)
	cmd := &cobra.Command{
		Use:    "owners",
		Short:  "Score ownership recovery against a repo's own OWNERS files",
		Hidden: true,
		Long: `Score whodar against the ownership humans wrote down: the OWNERS files
projects in the Kubernetes ecosystem maintain for real governance, aliases
expanded to individuals.

This measures ownership recovery, not expertise. The naive baseline is git's
top committers per directory, printed beside every whodar number, and the
directories where the baseline misses are reported as their own cohort: they
are the only ones that can tell this tool apart from counting commits.

By default the index is built here, from git history alone, so the ground
truth never leaks into the input. Pass --use-index to score an existing index
built with more sources (such as GitHub review data), and make sure that index
was never fed CODEOWNERS for this repository: judging ownership recovery on an
index that ingested declared ownership is a circle.

Examples:
  whodar eval owners --repo ~/src/kubernetes
  whodar eval owners --repo ~/src/kubernetes --use-index --data-dir ./k8sidx`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repo == "" {
				return fmt.Errorf("%w: --repo is required", ErrBadArgs)
			}
			log := cmd.ErrOrStderr()
			// The git walk always runs: its place-scoped tally is the primary
			// ranking, whatever index supplies the identities.
			git := connector.NewGitHistory(connector.GitOptions{
				Paths: []string{repo}, SinceDays: sinceDays, MaxCommits: 1000000, Log: log,
			})
			recs, err := git.Fetch(cmd.Context())
			if err != nil {
				return err
			}
			var ix *index.Index
			if useIndex {
				loaded, err := opts.loadIndex(cmd)
				if err != nil {
					return noIndexError(err)
				}
				ix = loaded
			} else {
				ix = index.New()
				ix.Build(recs)
				ix.AutoJoin()
				ix.Canonicalize()
			}

			dirWork, workTotals := git.DirWork(), git.WorkTotals()
			if tallyPath != "" {
				// A saved tally carries review credit placed against the
				// directories each pull request changed, which a git walk
				// alone cannot know.
				loaded, totals, err := loadPlaceTally(tallyPath)
				if err != nil {
					return err
				}
				dirWork, workTotals = loaded, totals
			}

			res, err := ownersbench.Run(ix, ownersbench.Config{
				Repo: repo, SinceDays: sinceDays, MinCommits: minCommits,
				TopK: topK, MaxLeafShare: maxLeafShare, Log: log,
				DirWork: dirWork, WorkTotals: workTotals,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), res, opts.pretty)
			}

			w, n := res.Score(func(d ownersbench.DirResult) bool { return d.WhodarHit })
			g, _ := res.Score(func(d ownersbench.DirResult) bool { return d.GitHit })
			cohC := res.CohortC()
			cw := 0
			for _, d := range cohC {
				if d.WhodarHit {
					cw++
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "judged %d directories (of %d with truth; dropped %d quiet, %d ambiguous, %d unmappable)\n",
				n, res.TruthDirs, res.DroppedQuiet, res.DroppedAmbiguous, res.DroppedUnmappable)
			fmt.Fprintf(out, "whodar names an approver in its top %d:  %d/%d (%.0f%%)\n",
				topK, w, n, 100*float64(w)/float64(n))
			fmt.Fprintf(out, "git top-%d committers baseline:         %d/%d (%.0f%%)\n",
				topK, g, n, 100*float64(g)/float64(n))
			if len(cohC) > 0 {
				fmt.Fprintf(out, "cohort C, approver not a top committer: %d/%d (%.0f%%)  <- the discriminating cohort\n",
					cw, len(cohC), 100*float64(cw)/float64(len(cohC)))
			}
			for _, d := range res.Dirs {
				mark := "."
				if d.WhodarHit {
					mark = "Y"
				}
				base := "."
				if d.GitHit {
					base = "Y"
				}
				fmt.Fprintf(out, "  %s %s  %-46s whodar=%v\n", mark, base, d.Dir, d.WhodarTop)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "Repository root holding OWNERS ground truth.")
	f.IntVar(&sinceDays, "since-days", 730, "History window, for both index and baseline.")
	f.IntVar(&minCommits, "min-commits", 30, "Least directory activity worth judging.")
	f.IntVar(&topK, "top-k", 3, "Names each side may offer.")
	f.IntVar(&maxLeafShare, "max-leaf-share", 3,
		"Most directories a leaf name may be shared by before it is unjudgeable.")
	f.StringVar(&tallyPath, "place-tally", "",
		"Saved place tally with review credit folded in, from the review probe.")
	f.BoolVar(&useIndex, "use-index", false,
		"Score the index in --data-dir instead of building one from git here.")
	f.BoolVar(&jsonOut, "json", false, "Emit the full result as JSON.")
	return cmd
}

// loadPlaceTally reads a saved place tally: per-directory work with review
// credit already folded in, and the breadth totals that go with it.
func loadPlaceTally(path string) (map[string]map[string]float64, map[string]float64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("eval owners: read place tally: %w", err)
	}
	var blob struct {
		DirWork    map[string]map[string]float64 `json:"dirWork"`
		WorkTotals map[string]float64            `json:"workTotals"`
	}
	if err := json.Unmarshal(raw, &blob); err != nil {
		return nil, nil, fmt.Errorf("eval owners: parse place tally: %w", err)
	}
	return blob.DirWork, blob.WorkTotals, nil
}
