package cmd

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/license"
	"github.com/kordloom/whodar/internal/llm"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/util"
)

// slackTokenEnv is the environment variable holding the Slack bot token.
const slackTokenEnv = "WHODAR_SLACK_TOKEN"

// githubTokenEnv is the environment variable holding the GitHub token.
const githubTokenEnv = "WHODAR_GITHUB_TOKEN"

// pagerdutyTokenEnv is the environment variable holding the PagerDuty token.
const pagerdutyTokenEnv = "WHODAR_PAGERDUTY_TOKEN"

// Jira environment variables for the site URL, email, and API token.
const (
	jiraURLEnv   = "WHODAR_JIRA_URL"
	jiraEmailEnv = "WHODAR_JIRA_EMAIL"
	jiraTokenEnv = "WHODAR_JIRA_TOKEN"
)

// Confluence environment variables. They fall back to the Jira ones because
// both use the same Atlassian site and token.
const (
	confluenceURLEnv   = "WHODAR_CONFLUENCE_URL"
	confluenceEmailEnv = "WHODAR_CONFLUENCE_EMAIL"
	confluenceTokenEnv = "WHODAR_CONFLUENCE_TOKEN"
)

// newIndexCmd builds the index command, which ingests a source into the index.
func newIndexCmd(opts *options) *cobra.Command {
	var (
		source           string
		file             string
		includePrivate   bool
		sinceDays        int
		maxMessages      int
		episodes         bool
		maxEpisodes      int
		archive          bool
		maxArchive       int
		changesFile      string
		embed            bool
		embedModel       string
		ollamaURL        string
		repos            []string
		githubOrg        string
		maxRepos         int
		githubEmails     bool
		jiraURL          string
		jiraProjects     []string
		jiraJQL          string
		maxIssues        int
		merge            bool
		aliasesFile      string
		halfLifeDays     int
		repoPaths        []string
		gitSinceDays     int
		maxCommits       int
		confluenceSpaces []string
		confluenceCQL    string
		maxPages         int
	)
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Build the index from a source",
		Long: `Build or extend the index from one source per run. Combine sources with --merge;
people join across sources by email, or by an alias file (--aliases) when a
source only knows a handle. Dated activity decays per --half-life-days.

Sources and their credentials:
  org-csv     --file people.csv                          none
  codeowners  --file CODEOWNERS|repo-root                none
  git         --repo-path DIR (repeatable)               none
  slack       [--include-private]                        WHODAR_SLACK_TOKEN
  github      --repo o/r | --github-org ORG              WHODAR_GITHUB_TOKEN
  jira        --jira-project KEY | --jira-jql JQL        WHODAR_JIRA_URL/EMAIL/TOKEN
  confluence  --confluence-space KEY | --confluence-cql  WHODAR_CONFLUENCE_* (or Jira's)
  pagerduty   (no scope flags)                           WHODAR_PAGERDUTY_TOKEN

Start with the org chart, then merge everything else onto it:
  whodar index --source org-csv --file people.csv
  whodar index --source slack --merge
  whodar index --source git --repo-path ~/src/billing --merge`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var (
				recs []connector.Record
				eps  []episode.Episode
				err  error
			)
			if archive {
				if err := guardArchive(cmd, opts); err != nil {
					return err
				}
			}
			switch source {
			case "org-csv":
				if file == "" {
					return fmt.Errorf("%w: --file is required for org-csv", ErrBadArgs)
				}
				oc := connector.NewOrgCSV(file)
				oc.Log = cmd.ErrOrStderr()
				recs, err = oc.Fetch(cmd.Context())
			case "slack":
				recs, eps, err = fetchSlack(cmd, opts, slackArgs{
					includePrivate: includePrivate, sinceDays: sinceDays, maxMessages: maxMessages,
					episodes: episodes || archive, maxEpisodes: maxEpisodes,
					archive: archive, maxArchive: maxArchive,
				})
			case "codeowners":
				if file == "" {
					return fmt.Errorf("%w: --file (CODEOWNERS path or repo root) required for codeowners", ErrBadArgs)
				}
				recs, err = connector.NewCodeOwners(file).Fetch(cmd.Context())
			case "github":
				recs, eps, err = fetchGitHub(cmd,
					githubArgs{repos, githubOrg, maxRepos, githubEmails, episodes || archive})
			case "jira":
				recs, eps, err = fetchJira(cmd,
					jiraArgs{jiraURL, jiraProjects, jiraJQL, maxIssues, episodes || archive})
			case "confluence":
				recs, err = fetchConfluence(cmd, confluenceArgs{confluenceSpaces, confluenceCQL, maxPages})
			case "pagerduty":
				recs, eps, err = fetchPagerDuty(cmd, episodes || archive)
			case "git":
				if len(repoPaths) == 0 {
					return fmt.Errorf("%w: --repo-path is required for git", ErrBadArgs)
				}
				recs, err = connector.NewGitHistory(connector.GitOptions{
					Paths:      repoPaths,
					SinceDays:  gitSinceDays,
					MaxCommits: maxCommits,
					Log:        cmd.ErrOrStderr(),
				}).Fetch(cmd.Context())
			default:
				return fmt.Errorf("%w: %q (want org-csv, slack, codeowners, github, jira, confluence, pagerduty, or git)", ErrUnknownSource, source)
			}
			if err != nil {
				return err
			}

			return indexRecords(cmd, opts, recs, indexParams{
				merge: merge, halfLifeDays: halfLifeDays, aliasesFile: aliasesFile,
				embed: embed, embedModel: embedModel, ollamaURL: ollamaURL, changesFile: changesFile,
				episodes: eps,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&source, "source", "org-csv", "Source type: org-csv, slack, codeowners, github, jira, confluence, pagerduty, or git.")
	f.StringVar(&file, "file", "", "Path to the source file (org-csv).")
	f.BoolVar(&includePrivate, "include-private", false, "Ingest private Slack channels if policy allows.")
	f.IntVar(&sinceDays, "since-days", 180, "Slack history window in days.")
	f.IntVar(&maxMessages, "max-messages", 5000, "Slack message cap per channel.")
	f.BoolVar(&episodes, "episodes", false,
		"Record past conversations so whodar recall can point back at them.")
	f.IntVar(&maxEpisodes, "max-episodes-per-channel", 200, "Episode cap per channel.")
	f.BoolVar(&archive, "archive", false,
		"Slack only: keep the words of each conversation, not just a link. Needs a Memory license, implies --episodes.")
	f.IntVar(&maxArchive, "max-archive-messages", 50, "Retained message cap per conversation.")
	f.StringVar(&changesFile, "changes-file", "", "Write the index diff as JSON to this path.")
	f.BoolVar(&merge, "merge", false, "Merge into the existing index instead of replacing it.")
	f.StringVar(&aliasesFile, "aliases", "",
		"JSON file mapping a canonical id to its aliases, joining one person across sources.")
	f.IntVar(&halfLifeDays, "half-life-days", 180,
		"Days for a dated record's weight to halve; 0 disables recency decay.")
	f.StringArrayVar(&repoPaths, "repo-path", nil, "Local repository root for the git source (repeatable).")
	f.IntVar(&gitSinceDays, "git-since-days", 365, "Git history window in days.")
	f.IntVar(&maxCommits, "max-commits", 2000, "Commit cap per repository for the git source.")
	f.BoolVar(&embed, "embed", false, "Generate embeddings via Ollama for semantic search.")
	f.StringVar(&embedModel, "embed-model", "", "Ollama embed model (default nomic-embed-text).")
	f.StringVar(&ollamaURL, "ollama-url", "http://localhost:11434", "Ollama base URL for --embed.")
	f.StringArrayVar(&repos, "repo", nil, "GitHub repo owner/name (repeatable).")
	f.StringVar(&githubOrg, "github-org", "", "GitHub org to index all repositories of.")
	f.IntVar(&maxRepos, "max-repos", 0, "Cap repositories taken from --github-org (0 = all).")
	f.BoolVar(&githubEmails, "github-emails", false, "Resolve GitHub user emails to join other sources.")
	f.StringVar(&jiraURL, "jira-url", "", "Jira site URL (or WHODAR_JIRA_URL).")
	f.StringArrayVar(&jiraProjects, "jira-project", nil, "Jira project key (repeatable).")
	f.StringVar(&jiraJQL, "jira-jql", "", "Jira JQL query (overrides --jira-project).")
	f.IntVar(&maxIssues, "max-issues", 1000, "Cap Jira issues read.")
	f.StringArrayVar(&confluenceSpaces, "confluence-space", nil, "Confluence space key (repeatable).")
	f.StringVar(&confluenceCQL, "confluence-cql", "", "Confluence CQL query (overrides --confluence-space).")
	f.IntVar(&maxPages, "max-pages", 2000, "Cap Confluence pages read.")
	return cmd
}

// indexParams holds the index-build knobs shared by the index and connect
// commands: how records fold into the existing graph and how the result decays,
// embeds, and reports.
type indexParams struct {
	// merge adds records onto the existing index instead of replacing it.
	merge bool
	// halfLifeDays halves a dated record's weight after this many days; 0 disables decay.
	halfLifeDays int
	// aliasesFile joins one person across sources by canonical id when set.
	aliasesFile string
	// embed generates embeddings via Ollama when true.
	embed bool
	// embedModel names the Ollama embedding model; empty uses the default.
	embedModel string
	// ollamaURL is the Ollama base URL for embedding.
	ollamaURL string
	// changesFile writes the index diff as JSON to this path when set.
	changesFile string
	// episodes are the conversations a source observed, stored beside the
	// index so recall can point back at them.
	episodes []episode.Episode
}

// indexRecords folds recs into the on-disk index and reports what changed. It
// loads the previous graph for the diff, builds or merges, auto-joins handle
// identities, canonicalizes, optionally embeds, then saves atomically. The index
// and connect commands share it so ingest lives in one place.
func indexRecords(cmd *cobra.Command, opts *options, recs []connector.Record, p indexParams) error {
	// Load any existing index once. A missing file is a first run; any other
	// error, including an encrypted index with no key, aborts so re-indexing
	// never overwrites an index it could not read.
	existing, err := opts.loadIndex(cmd)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	var prev *model.Graph
	if existing != nil {
		prev = existing.Graph
	}

	ix := index.New()
	if p.merge && existing != nil {
		ix = existing
	}
	ix.SetHalfLife(time.Duration(p.halfLifeDays) * 24 * time.Hour)
	if p.aliasesFile != "" {
		if err := ix.LoadAliases(p.aliasesFile); err != nil {
			return err
		}
	}
	if p.merge {
		ix.Add(recs)
	} else {
		ix.Build(recs)
	}
	if joined := ix.AutoJoin(); joined > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "auto-joined %d handle identities\n", joined)
	}
	ix.Canonicalize()

	if p.embed {
		if err := guardLLMHost(opts.pol, p.ollamaURL); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"embedding %d people and %d channels via Ollama...\n",
			len(ix.Graph.People), len(ix.Graph.Channels))
		if err := ix.Embed(cmd.Context(), newOllama("", p.embedModel, p.ollamaURL)); err != nil {
			return fmt.Errorf("embed: %w", err)
		}
	}

	changes := index.Diff(prev, ix.Graph)
	if err := opts.saveIndex(ix); err != nil {
		return err
	}
	if err := saveEpisodes(cmd, opts, ix, p); err != nil {
		return err
	}

	out := cmd.ErrOrStderr()
	fmt.Fprintf(out,
		"indexed %d people, %d channels, %d teams, %d topics into %s\n",
		len(ix.Graph.People), len(ix.Graph.Channels), len(ix.Graph.Teams),
		len(ix.Graph.Topics), opts.indexPath())
	reportChanges(out, changes)
	if p.changesFile != "" {
		if err := writeChangesFile(p.changesFile, changes); err != nil {
			return err
		}
	}
	return nil
}

// guardArchive refuses to retain conversation content unless the organization
// is licensed for it and its own policy allows it. Both checks are local: the
// license is verified against a compiled-in key, and the policy is a file the
// organization controls.
func guardArchive(cmd *cobra.Command, opts *options) error {
	if !opts.pol.AllowArchive() {
		return fmt.Errorf("%w: keeping conversation content is disabled by policy", ErrBadArgs)
	}
	state := license.Resolve(opts.dataDir, time.Now())
	if !state.Has(license.Memory) {
		return fmt.Errorf(
			"%w: keeping the words of a conversation needs a Memory license "+
				"($5,000 a year, flat per organization). %s Ask at hello@whodar.dev",
			ErrBadArgs, state.Reason())
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "archive: %s\n", state.Reason())
	return nil
}

// saveEpisodes merges newly observed conversations into the episode store.
// Episodes always merge, even when the index itself is rebuilt: a source stops
// serving old messages long before they stop being worth remembering, so
// dropping them would throw away history whodar may be the last to hold.
func saveEpisodes(cmd *cobra.Command, opts *options, ix *index.Index, p indexParams) error {
	eps := p.episodes
	if len(eps) == 0 {
		return nil
	}
	// Participants arrive as each source names them. Resolving them against
	// the graph is what makes one person's work findable across every tool.
	ix.CanonicalizeEpisodes(eps)
	store, err := opts.loadEpisodes(cmd)
	if err != nil {
		return err
	}
	// A conversation can only be embedded while its text is in hand: the store
	// keeps terms, not messages, so an episode indexed without embeddings can
	// never gain them later.
	var embedder *llm.Ollama
	if p.embed {
		embedder = newOllama("", p.embedModel, p.ollamaURL)
		fmt.Fprintf(cmd.ErrOrStderr(), "embedding %d conversations via Ollama...\n", len(eps))
	}
	before := store.Len()
	for _, ep := range eps {
		body := strings.TrimSpace(ep.Body + " " + ep.Text())
		store.Add(ep)
		if embedder == nil || body == "" {
			continue
		}
		vec, err := embedder.Embed(cmd.Context(), body)
		if err != nil {
			return fmt.Errorf("embed conversation: %w", err)
		}
		store.SetVector(ep.ID, vec)
	}
	if err := opts.saveEpisodes(store); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"recorded %d conversations (%d new) into %s\n",
		len(eps), store.Len()-before, opts.episodePath())
	return nil
}

// slackArgs holds the Slack-specific index flags.
type slackArgs struct {
	// includePrivate requests private-channel ingest.
	includePrivate bool
	// sinceDays is the history window in days.
	sinceDays int
	// maxMessages caps messages per channel.
	maxMessages int
	// episodes records the conversations behind the messages.
	episodes bool
	// maxEpisodes caps episodes kept per channel.
	maxEpisodes int
	// archive retains the content of each conversation.
	archive bool
	// maxArchive caps retained messages per conversation.
	maxArchive int
}

// fetchSlack builds Slack records, enforcing the private-channel policy guard.
func fetchSlack(
	cmd *cobra.Command, opts *options, a slackArgs,
) ([]connector.Record, []episode.Episode, error) {
	token := os.Getenv(slackTokenEnv)
	if token == "" {
		return nil, nil, fmt.Errorf("%w: set %s", ErrBadArgs, slackTokenEnv)
	}
	if a.includePrivate && !opts.pol.AllowPrivateChannels() {
		return nil, nil, fmt.Errorf("%w: private-channel ingest is disabled by policy", ErrBadArgs)
	}
	src := connector.NewSlack(token, connector.SlackOptions{
		IncludePrivate:        a.includePrivate,
		SinceDays:             a.sinceDays,
		MaxMessages:           a.maxMessages,
		Episodes:              a.episodes,
		MaxEpisodesPerChannel: a.maxEpisodes,
		Archive:               a.archive,
		MaxArchiveMessages:    a.maxArchive,
		Log:                   cmd.ErrOrStderr(),
	})
	recs, err := src.Fetch(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return recs, src.Episodes(), nil
}

// githubArgs holds the GitHub-specific index flags.
type githubArgs struct {
	// repos is the list of owner/name repositories.
	repos []string
	// org adds every repository in the org.
	org string
	// maxRepos caps repositories taken from the org.
	maxRepos int
	// emails resolves user emails to join other sources.
	emails bool
	// episodes records merged changes.
	episodes bool
}

// fetchGitHub builds GitHub records from the configured repositories or org.
func fetchGitHub(cmd *cobra.Command, a githubArgs) ([]connector.Record, []episode.Episode, error) {
	token := os.Getenv(githubTokenEnv)
	if token == "" {
		return nil, nil, fmt.Errorf("%w: set %s", ErrBadArgs, githubTokenEnv)
	}
	if len(a.repos) == 0 && a.org == "" {
		return nil, nil, fmt.Errorf("%w: --repo or --github-org required for github", ErrBadArgs)
	}
	src := connector.NewGitHub(token, connector.GitHubOptions{
		Repos: a.repos, Org: a.org, MaxRepos: a.maxRepos, ResolveEmails: a.emails,
		Episodes: a.episodes, Log: cmd.ErrOrStderr(),
	})
	recs, err := src.Fetch(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return recs, src.Episodes(), nil
}

// jiraArgs holds the Jira-specific index flags.
type jiraArgs struct {
	// url is the Jira site URL.
	url string
	// projects scopes the search to these project keys.
	projects []string
	// jql overrides the query.
	jql string
	// maxIssues caps issues read.
	maxIssues int
	// episodes records resolved issues.
	episodes bool
}

// fetchJira builds Jira records, reading the URL and credentials from flags and
// the environment.
func fetchJira(cmd *cobra.Command, a jiraArgs) ([]connector.Record, []episode.Episode, error) {
	site := a.url
	if site == "" {
		site = os.Getenv(jiraURLEnv)
	}
	email := os.Getenv(jiraEmailEnv)
	token := os.Getenv(jiraTokenEnv)
	if site == "" || email == "" || token == "" {
		return nil, nil, fmt.Errorf("%w: set --jira-url (or %s), %s, and %s",
			ErrBadArgs, jiraURLEnv, jiraEmailEnv, jiraTokenEnv)
	}
	src := connector.NewJira(site, email, token, connector.JiraOptions{
		Projects: a.projects, JQL: a.jql, MaxIssues: a.maxIssues,
		Episodes: a.episodes, Log: cmd.ErrOrStderr(),
	})
	recs, err := src.Fetch(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return recs, src.Episodes(), nil
}

// confluenceArgs holds the Confluence-specific index flags.
type confluenceArgs struct {
	// spaces scopes the search to these space keys.
	spaces []string
	// cql overrides the query.
	cql string
	// maxPages caps pages read.
	maxPages int
}

// fetchConfluence builds Confluence records. The URL and credentials fall back
// to the Jira environment variables, since both use the same Atlassian site.
func fetchConfluence(cmd *cobra.Command, a confluenceArgs) ([]connector.Record, error) {
	site := cmp.Or(os.Getenv(confluenceURLEnv), os.Getenv(jiraURLEnv))
	email := cmp.Or(os.Getenv(confluenceEmailEnv), os.Getenv(jiraEmailEnv))
	token := cmp.Or(os.Getenv(confluenceTokenEnv), os.Getenv(jiraTokenEnv))
	if site == "" || email == "" || token == "" {
		return nil, fmt.Errorf("%w: set WHODAR_CONFLUENCE_URL, EMAIL, and TOKEN (or the Jira ones)", ErrBadArgs)
	}
	src := connector.NewConfluence(site, email, token, connector.ConfluenceOptions{
		Spaces: a.spaces, CQL: a.cql, MaxPages: a.maxPages, Log: cmd.ErrOrStderr(),
	})
	return src.Fetch(cmd.Context())
}

// fetchPagerDuty builds PagerDuty records from services and on-call data.
func fetchPagerDuty(cmd *cobra.Command, episodes bool) ([]connector.Record, []episode.Episode, error) {
	token := os.Getenv(pagerdutyTokenEnv)
	if token == "" {
		return nil, nil, fmt.Errorf("%w: set %s", ErrBadArgs, pagerdutyTokenEnv)
	}
	src := connector.NewPagerDuty(token, connector.PagerDutyOptions{
		Episodes: episodes, Log: cmd.ErrOrStderr(),
	})
	recs, err := src.Fetch(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return recs, src.Episodes(), nil
}

// reportChanges prints a one-line summary and capped lists of who and what
// joined or left since the last index.
func reportChanges(w io.Writer, c index.Changes) {
	if c.Empty() {
		return
	}
	fmt.Fprintf(w, "changes since last index: %s\n", c.Summary())
	printChangeList(w, "joined", c.PeopleJoined)
	printChangeList(w, "left", c.PeopleLeft)
	printChangeList(w, "new channels", c.ChannelsAdded)
	printChangeList(w, "gone channels", c.ChannelsRemoved)
}

// printChangeList prints up to a fixed number of items under a label, noting
// any remainder.
func printChangeList(w io.Writer, label string, items []string) {
	const limit = 15
	if len(items) == 0 {
		return
	}
	shown := items
	if len(shown) > limit {
		shown = shown[:limit]
	}
	fmt.Fprintf(w, "  %s: %s", label, strings.Join(shown, ", "))
	if len(items) > limit {
		fmt.Fprintf(w, ", and %d more", len(items)-limit)
	}
	fmt.Fprintln(w)
}

// writeChangesFile writes the changes as indented JSON to path atomically.
func writeChangesFile(path string, c index.Changes) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("changes file: %w", err)
	}
	if err := util.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("changes file: %w", err)
	}
	return nil
}
