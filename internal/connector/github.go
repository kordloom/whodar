package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"sync"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/github"

	"github.com/kordloom/whodar/internal/util"
)

// maxTopicWeight caps how many times one topic counts for a person, so a heavy
// contributor outranks a one-off without a single topic dominating the score.
const maxTopicWeight = 4

// noiseWords are common pull request and issue title words that carry no topic.
var noiseWords = map[string]bool{
	"fix": true, "fixes": true, "fixed": true, "add": true, "adds": true,
	"added": true, "update": true, "updates": true, "updated": true,
	"remove": true, "removes": true, "bump": true, "the": true, "and": true,
	"for": true, "with": true, "from": true, "into": true, "that": true,
	"this": true, "use": true, "uses": true, "new": true, "support": true,
	"make": true, "when": true, "not": true, "via": true, "run": true,
	"set": true, "get": true, "all": true, "out": true, "try": true,
}

// GitHubOptions configures the GitHub connector.
type GitHubOptions struct {
	// Repos is a list of "owner/name" repositories to index.
	Repos []string
	// Org, when set, adds the org's repositories.
	Org string
	// MaxRepos caps repositories taken from the org; zero means all returned.
	MaxRepos int
	// ResolveEmails fetches each user's profile to join by email.
	ResolveEmails bool
	// Episodes records merged changes, so recall can point back at the change
	// that fixed something.
	Episodes bool
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// withDefaults fills the log writer when unset.
func (o GitHubOptions) withDefaults() GitHubOptions {
	if o.Log == nil {
		o.Log = io.Discard
	}
	return o
}

// GitHub is a Source that ingests repositories. It weights people by what they
// actually work on: pull request and issue labels and titles for authors,
// reviewers, and assignees, plus repository topics for contributors and
// CODEOWNERS for path ownership.
type GitHub struct {
	// client calls the GitHub API.
	client *github.Client
	// opts holds the resolved options.
	opts GitHubOptions
	// episodes holds the merged changes seen by the last Fetch.
	episodes []episode.Episode
}

// NewGitHub returns a GitHub connector authenticating with token.
func NewGitHub(token string, opts GitHubOptions) *GitHub {
	return &GitHub{client: github.New(token), opts: opts.withDefaults()}
}

// NewGitHubWithClient returns a GitHub connector using a preconfigured client.
// Tests use it to inject a client pointed at a mock server.
func NewGitHubWithClient(client *github.Client, opts GitHubOptions) *GitHub {
	if client == nil {
		panic("connector: NewGitHubWithClient requires a non-nil client")
	}
	return &GitHub{client: client, opts: opts.withDefaults()}
}

// Ping verifies the token with a cheap authenticated request, so a wizard can
// confirm credentials before committing to a full index.
func (g *GitHub) Ping(ctx context.Context) error {
	return g.client.Ping(ctx)
}

// Fetch reads each repository and returns person records weighted by topic.
func (g *GitHub) Fetch(ctx context.Context) ([]Record, error) {
	g.episodes = nil
	repos, err := g.repoList(ctx)
	if err != nil {
		return nil, err
	}

	// Repos are fetched concurrently, so serialize every write to the shared
	// tallies and to the log writer, which a test may back with a plain buffer.
	g.opts.Log = &lockedWriter{w: g.opts.Log}
	var mu sync.Mutex

	counts := make(map[string]map[string]int) // login -> token -> count
	latest := make(map[string]time.Time)      // login -> most recent activity
	bump := func(login string, tokens []string, t time.Time) {
		if login == "" || len(tokens) == 0 || strings.HasSuffix(login, "[bot]") {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		c := counts[login]
		if c == nil {
			c = make(map[string]int)
			counts[login] = c
		}
		for _, tok := range tokens {
			if tok = strings.ToLower(strings.TrimSpace(tok)); tok != "" {
				c[tok]++
			}
		}
		if t.After(latest[login]) {
			latest[login] = t
		}
	}

	var (
		codeOwnerRecords []Record
		wg               sync.WaitGroup
	)
	sem := make(chan struct{}, repoWorkers)
	for _, full := range repos {
		owner, name, ok := splitRepo(full)
		if !ok {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		wg.Add(1)
		go func(full, owner, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			recs, eps, err := g.indexRepo(ctx, full, owner, name, bump)
			mu.Lock()
			codeOwnerRecords = append(codeOwnerRecords, recs...)
			g.episodes = append(g.episodes, eps...)
			mu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(g.opts.Log, "github: skipping %s: %v\n", full, err)
			}
		}(full, owner, name)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	accounts := g.resolveAccounts(ctx, counts)

	records := make([]Record, 0, len(counts)+len(codeOwnerRecords))
	for login, tokenCounts := range counts {
		rec := githubPersonRecord(login, expandTopics(tokenCounts), accounts[login])
		rec.Time = latest[login]
		records = append(records, rec)
	}
	records = append(records, codeOwnerRecords...)
	return records, nil
}

// indexRepo tallies one repository's contributors, pull requests, issues, and
// CODEOWNERS through bump, returning that repo's CODEOWNERS records. A truncated
// listing is indexed as a partial set with a warning. A hard error is returned
// so the caller can skip this repo, or abort on context cancellation, without
// discarding the repos already indexed.
func (g *GitHub) indexRepo(
	ctx context.Context, full, owner, name string, bump func(string, []string, time.Time),
) ([]Record, []episode.Episode, error) {
	repo, err := g.client.Repo(ctx, owner, name)
	if err != nil {
		return nil, nil, fmt.Errorf("repo: %w", err)
	}
	repoTokens := repoTopicSet(repo)

	cons, err := g.client.Contributors(ctx, owner, name)
	if e := g.usable(full, "contributors", len(cons), err); e != nil {
		return nil, nil, fmt.Errorf("contributors: %w", e)
	}
	for _, c := range cons {
		bump(c.Login, repoTokens, time.Time{})
	}

	var episodes []episode.Episode
	pulls, err := g.client.PullRequests(ctx, owner, name)
	if e := g.usable(full, "pulls", len(pulls), err); e != nil {
		return nil, nil, fmt.Errorf("pulls: %w", e)
	}
	for _, pr := range pulls {
		if g.opts.Episodes {
			if ep, ok := changeEpisode(owner, name, pr); ok {
				episodes = append(episodes, ep)
			}
		}
		tokens := append(pr.LabelNames(), titleTokens(pr.Title)...)
		bump(pr.Author(), tokens, pr.UpdatedAt)
		for _, u := range pr.Reviewers() {
			bump(u, tokens, pr.UpdatedAt)
		}
		for _, u := range pr.AssigneeLogins() {
			bump(u, tokens, pr.UpdatedAt)
		}
	}

	issues, err := g.client.Issues(ctx, owner, name)
	if e := g.usable(full, "issues", len(issues), err); e != nil {
		// Preserve the episodes already gathered from the pull requests: they
		// were read successfully, and a later failure should not discard them.
		return nil, episodes, fmt.Errorf("issues: %w", e)
	}
	var issueCount int
	for _, is := range issues {
		if is.IsPullRequest() {
			continue
		}
		issueCount++
		tokens := append(is.LabelNames(), titleTokens(is.Title)...)
		bump(is.Author(), tokens, is.UpdatedAt)
		for _, u := range is.AssigneeLogins() {
			bump(u, tokens, is.UpdatedAt)
		}
	}

	var codeOwnerRecords []Record
	if content := g.codeOwners(ctx, owner, name); content != nil {
		recs, err := parseCodeOwners(ctx, bytes.NewReader(content))
		switch {
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return codeOwnerRecords, episodes, err
		case err != nil:
			fmt.Fprintf(g.opts.Log, "github: %s CODEOWNERS parse failed: %v\n", full, err)
		default:
			codeOwnerRecords = remapCodeOwners(recs)
		}
	}
	fmt.Fprintf(g.opts.Log, "github: indexed %s (%d contributors, %d pulls, %d issues)\n",
		full, len(cons), len(pulls), issueCount)
	return codeOwnerRecords, episodes, nil
}

// usable reports whether a listing error is tolerable. A nil error and a
// truncation, whose partial results are still usable, both return nil; the
// truncation is logged. Any other error is returned so the repo is skipped.
func (g *GitHub) usable(full, what string, n int, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, github.ErrTruncated) {
		fmt.Fprintf(g.opts.Log, "github: %s %s truncated, indexing %d\n", full, what, n)
		return nil
	}
	return err
}

// repoList resolves the explicit repos plus any from the org, capped.
func (g *GitHub) repoList(ctx context.Context) ([]string, error) {
	repos := append([]string(nil), g.opts.Repos...)
	if g.opts.Org != "" {
		orgRepos, err := g.client.OrgRepos(ctx, g.opts.Org)
		if err != nil {
			return nil, fmt.Errorf("github org %s: %w", g.opts.Org, err)
		}
		for i, r := range orgRepos {
			if g.opts.MaxRepos > 0 && i >= g.opts.MaxRepos {
				fmt.Fprintf(g.opts.Log, "github: stopping at %d org repos (cap)\n", g.opts.MaxRepos)
				break
			}
			repos = append(repos, r.FullName)
		}
	}
	if len(repos) == 0 {
		return nil, ErrNoRepos
	}
	return repos, nil
}

// resolveAccounts looks up each login's profile when email resolution is on.
// repoWorkers bounds how many repositories are fetched at once. GitHub's
// secondary rate limits punish aggressive concurrency, so this stays modest.
const repoWorkers = 6

// lockedWriter serializes writes so concurrent per-repo logging cannot
// interleave or race on the underlying writer.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// Write locks around the underlying write.
func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// accountWorkers bounds how many profile lookups run at once. Email resolution
// is one request per contributor, so a large org means hundreds of them;
// running a bounded batch concurrently turns minutes of serial waiting into
// seconds without tripping GitHub's secondary rate limits.
const accountWorkers = 8

func (g *GitHub) resolveAccounts(ctx context.Context, logins map[string]map[string]int) map[string]github.Account {
	accounts := make(map[string]github.Account)
	if !g.opts.ResolveEmails {
		return accounts
	}
	progress := util.ProgressWriter(g.opts.Log, "github: resolved emails for", 100)
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		done int
	)
	sem := make(chan struct{}, accountWorkers)
	for login := range logins {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return accounts
		}
		wg.Add(1)
		go func(login string) {
			defer wg.Done()
			defer func() { <-sem }()
			a, err := g.client.Account(ctx, login)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accounts[login] = a
			}
			done++
			progress.Report(done)
		}(login)
	}
	wg.Wait()
	return accounts
}

// codeOwners returns the first CODEOWNERS file found in the repo, or nil.
func (g *GitHub) codeOwners(ctx context.Context, owner, name string) []byte {
	for _, p := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		if content, err := g.client.FileContents(ctx, owner, name, p); err == nil {
			return content
		}
	}
	return nil
}

// githubPersonRecord builds a person record. The handle always keys the
// record and a resolved email travels with it, so the indexer joins the two:
// without that join a GitHub login resolves to nobody, and work done under a
// handle cannot be found by the person who did it.
func githubPersonRecord(login string, topics []string, a github.Account) Record {
	rec := Record{
		Kind: KindPerson, Source: "github", Weight: 1, Topics: topics,
		PersonID: "github:" + strings.ToLower(login),
	}
	if a.Email != "" {
		rec.Email = util.NormalizeEmail(a.Email)
	}
	rec.Name = a.Name
	if rec.Name == "" {
		rec.Name = "@" + login
	}
	return rec
}

// remapCodeOwners rewrites a repo's own CODEOWNERS @login owners into the github
// identity namespace, so a login that also authored pull requests or issues in
// the same repo merges into one person. Team owners (@org/team) and email owners
// keep their own contact entries.
func remapCodeOwners(recs []Record) []Record {
	for i := range recs {
		login, ok := strings.CutPrefix(recs[i].Name, "@")
		if !ok || strings.Contains(login, "/") {
			continue
		}
		recs[i].PersonID = "github:" + strings.ToLower(login)
	}
	return recs
}

// repoTopicSet derives a repo's topic tags from its GitHub topics and the words
// of its name and description.
func repoTopicSet(repo github.Repo) []string {
	out := append([]string(nil), repo.Topics...)
	out = append(out, titleTokens(repo.Name)...)
	out = append(out, titleTokens(repo.Description)...)
	return out
}

// titleTokens splits text into lowercase topic words, dropping short tokens,
// generic code words, and common title filler.
func titleTokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if len(f) >= 3 && !codeStop[f] && !noiseWords[f] {
			out = append(out, f)
		}
	}
	return out
}

// expandTopics turns per-token counts into a topic slice with each token
// repeated by its capped count, so volume of work raises a person's score.
func expandTopics(counts map[string]int) []string {
	tokens := make([]string, 0, len(counts))
	for t := range counts {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)

	var out []string
	for _, t := range tokens {
		n := min(counts[t], maxTopicWeight)
		for range n {
			out = append(out, t)
		}
	}
	return out
}

// splitRepo splits "owner/name" into its parts.
func splitRepo(full string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(strings.TrimSpace(full), "/")
	if !ok || owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}
