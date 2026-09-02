package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/resolve"
	"github.com/kordloom/whodar/internal/secret"
)

// assessDeparturesListed caps how many people the departure file covers.
const assessDeparturesListed = 10

// assessMinPlaceWork is the least credited work a directory needs before it
// is reported as a system.
const assessMinPlaceWork = 30

// assessPlacesListed caps how many systems the deliverable lists.
const assessPlacesListed = 40

// newAssessCmd builds the assess command, which turns a directory of exports
// into a sealed knowledge-risk assessment: the deliverable, not the app.
func newAssessCmd(opts *options) *cobra.Command {
	var (
		repoPaths    []string
		slackExport  string
		orgCSV       string
		codeowners   string
		outDir       string
		githubRepos  []string
		githubURL    string
		githubPages  int
		top          int
		gitSinceDays int
		maxCommits   int
	)
	cmd := &cobra.Command{
		Use:   "assess",
		Short: "Produce a sealed knowledge-risk assessment from local exports",
		Long: `Build a knowledge-risk assessment for one company from the material a data
room provides: local git clones, a Slack export zip, an org chart CSV, a
CODEOWNERS file. Nothing is fetched from the network and no credentials are
used, unless you also point it at a GitHub repository to read reviews from,
which is the one thing a data room rarely contains and the only way to see
the people who approve changes rather than write them. The index is built fresh in memory and never saved, so an assessment never
mixes with your own index.

The output directory holds the deliverable:

  summary.md           the executive summary: findings, questions, actions
  report.html          the knowledge-risk brief, readable without whodar
  findings.json        every scored topic: bus factor, level, experts
  ownership.json       where the owner on paper is not the one doing the work
  systems.json         each significant directory and who its work rests on
  departures.json      what leaves with each of the most load-bearing people
  assessment.loomseal  the sealed finding; verify offline with loomseal
  README.txt           what each file is and how to verify the seal

Examples:
  whodar assess --repo-path ./target-repo --out acme-assessment
  whodar assess --repo-path ./target-repo --github-repo acme/platform
  whodar assess --repo-path ./svc-a --repo-path ./svc-b --slack-export export.zip`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(repoPaths) == 0 && slackExport == "" && orgCSV == "" && codeowners == "" &&
				len(githubRepos) == 0 {
				return fmt.Errorf("%w: name at least one input: --repo-path, --slack-export, "+
					"--org-csv, or --codeowners", ErrBadArgs)
			}
			if outDir == "" {
				outDir = "whodar-assessment-" + time.Now().Format("20060102")
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("assess: %w", err)
			}
			log := cmd.ErrOrStderr()

			ix := index.New()
			if orgCSV != "" {
				oc := connector.NewOrgCSV(orgCSV)
				oc.Log = log
				recs, err := oc.Fetch(cmd.Context())
				if err != nil {
					return err
				}
				ix.Add(recs)
			}
			var git *connector.GitHistory
			if len(repoPaths) > 0 {
				git = connector.NewGitHistory(connector.GitOptions{
					Paths: repoPaths, SinceDays: gitSinceDays, MaxCommits: maxCommits, Log: log,
				})
				recs, err := git.Fetch(cmd.Context())
				if err != nil {
					return err
				}
				ix.Add(recs)
			}
			if codeowners != "" {
				recs, err := connector.NewCodeOwners(codeowners).Fetch(cmd.Context())
				if err != nil {
					return err
				}
				ix.Add(recs)
			}
			var gh *connector.GitHub
			if len(githubRepos) > 0 {
				token := secret.Resolve(githubTokenEnv)
				if token == "" {
					return fmt.Errorf("%w: --github-repo needs %s", ErrBadArgs, githubTokenEnv)
				}
				gh = connector.NewGitHub(token, connector.GitHubOptions{
					Repos: githubRepos, MaxPages: githubPages, BaseURL: githubURL, Log: log,
				})
				recs, err := gh.Fetch(cmd.Context())
				if err != nil {
					return explainSourceError("GitHub", githubTokenEnv, err)
				}
				ix.Add(recs)
			}
			if slackExport != "" {
				se := connector.NewSlackExport(slackExport, connector.SlackExportOptions{
					IncludePrivate: true, SinceDays: 36500, Log: log,
				})
				recs, err := se.Fetch(cmd.Context())
				if err != nil {
					return err
				}
				ix.Add(recs)
			}
			ix.AutoJoin()
			ix.Canonicalize()
			if len(ix.Graph.People) == 0 {
				return fmt.Errorf("%w: the inputs yielded no people; check the paths", ErrBadArgs)
			}

			findings := resolve.Risk(ix, 0)
			if err := writeAssessJSON(outDir, "findings.json", findings); err != nil {
				return err
			}
			ownership := resolve.Ownership(ix)
			if err := writeAssessJSON(outDir, "ownership.json", ownership); err != nil {
				return err
			}
			departures := assessDepartures(ix, top)
			if err := writeAssessJSON(outDir, "departures.json", departures); err != nil {
				return err
			}
			var places []resolve.Place
			if git != nil {
				// Systems are places, not words: the per-directory tally
				// answers who a system rests on without pooling every path
				// that shares its name.
				dirWork, totals := git.DirWork(), git.WorkTotals()
				if gh != nil {
					// A review is evidence of holding the place it reviewed.
					// The merges name their pull requests, so the link needs
					// no extra request, and without it a reviewer is credited
					// with the words of a title instead of the code.
					dirWork = resolve.AddReviewCredit(dirWork, totals,
						git.PullDirs(), gh.PullPeople())
				}
				places = resolve.PlaceLeads(ix, dirWork, totals,
					assessMinPlaceWork, 3)
				if len(places) > assessPlacesListed {
					places = places[:assessPlacesListed]
				}
				if err := writeAssessJSON(outDir, "systems.json", places); err != nil {
					return err
				}
			}
			spans := resolve.SoleSpans(ix, summaryListed)
			summary := assessSummary(ix, findings, ownership, departures, spans, places)
			if err := os.WriteFile(filepath.Join(outDir, "summary.md"),
				[]byte(summary), 0o644); err != nil {
				return fmt.Errorf("assess: %w", err)
			}
			if err := writeRiskHTML(cmd, ix, filepath.Join(outDir, "report.html"), briefRows); err != nil {
				return err
			}
			bundle, err := sealRiskBundle(opts, ix)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(outDir, "assessment.loomseal"), bundle, 0o600); err != nil {
				return fmt.Errorf("assess: %w", err)
			}
			if err := os.WriteFile(filepath.Join(outDir, "README.txt"),
				[]byte(assessReadme), 0o644); err != nil {
				return fmt.Errorf("assess: %w", err)
			}

			critical := 0
			for _, f := range findings {
				if f.Level == "critical" {
					critical++
				}
			}
			fmt.Fprintf(log,
				"assess: %d people across %v; %d topics scored, %d critical; "+
					"departure impact for %d people\nassess: wrote %s\n",
				len(ix.Graph.People), ix.SourceNames(), len(findings), critical,
				len(departures), outDir)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&repoPaths, "repo-path", nil, "Local repository root, repeatable.")
	f.StringVar(&slackExport, "slack-export", "", "Slack export zip or its unzipped folder.")
	f.StringVar(&orgCSV, "org-csv", "", "Org chart CSV.")
	f.StringVar(&codeowners, "codeowners", "", "CODEOWNERS file or a repo root holding one.")
	f.StringArrayVar(&githubRepos, "github-repo", nil,
		"Repository as owner/name whose reviews to read, repeatable. Needs "+
			githubTokenEnv+". Reviews are credited to the places they reviewed, "+
			"which is what finds the people who approve rather than commit.")
	f.StringVar(&githubURL, "github-url", "",
		"API root, for GitHub Enterprise Server. Empty uses github.com.")
	f.IntVar(&githubPages, "github-pages", 20,
		"Pages of pull requests to read per repository, in hundreds.")
	f.StringVar(&outDir, "out", "", "Output directory (default whodar-assessment-<date>).")
	f.IntVar(&top, "top", assessDeparturesListed, "People to cover in the departure file.")
	f.IntVar(&gitSinceDays, "git-since-days", 730,
		"How far back to read git history. Two years, because an assessment is about "+
			"what the company knows, not what it did this quarter.")
	f.IntVar(&maxCommits, "max-commits", 100000,
		"Commit cap per repository. Far above the indexing default: a cap that "+
			"truncates history silently drops the people who built the older half of "+
			"a system, which is exactly what an assessment is asked to find.")
	return cmd
}

// assessDepartures computes departure impact for the people who lead the most
// subjects, worst first.
func assessDepartures(ix *index.Index, top int) []resolve.DepartureImpact {
	seen := make(map[string]bool)
	var out []resolve.DepartureImpact
	for _, tr := range resolve.Risk(ix, 0) {
		for _, e := range tr.Experts {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			if imp := resolve.Departure(ix, e.ID); len(imp.Sole)+len(imp.Top) > 0 {
				out = append(out, imp)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Sole) != len(out[j].Sole) {
			return len(out[i].Sole) > len(out[j].Sole)
		}
		if len(out[i].Top) != len(out[j].Top) {
			return len(out[i].Top) > len(out[j].Top)
		}
		return out[i].Person < out[j].Person
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out
}

// writeAssessJSON writes one deliverable file as indented JSON.
func writeAssessJSON(dir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return fmt.Errorf("assess: %s: %w", name, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return fmt.Errorf("assess: %w", err)
	}
	return nil
}

// assessReadme explains the deliverable to somebody who has never run whodar.
const assessReadme = `This directory is a whodar knowledge-risk assessment.

summary.md           The executive summary: what was found, the questions it
                     raises for management, and the actions it suggests.
report.html          The knowledge-risk brief. Open it in a browser.
findings.json        Every scored topic: bus factor, risk level, and the
                     people who hold it.
ownership.json       Areas whose declared owner is not the person doing the
                     work in them.
systems.json         Each significant directory of the code base and the
                     people its observed work rests on, breadth-discounted so
                     wide-ranging committers do not outrank owners.
departures.json      For each of the most load-bearing people, the subjects
                     that leave with them: sole means nobody else holds it.
assessment.loomseal  A signed seal over the findings. Verify it offline:

    whodar attest verify assessment.loomseal

or with the standalone loomseal verifier, no account and nothing sent anywhere.
A verified seal proves the findings were produced by the named whodar install
and have not been edited since.
`
