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
	// Since, when set, restricts an incremental re-index to pull requests and
	// issues updated at or after it, and skips the whole-repo contributor and
	// CODEOWNERS snapshots, whose weight would double if folded again.
	Since time.Time
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
	// Tokens some repository, pull request, or issue stated as a topic or label.
	// Everything else mined from a name, title, or description stays weak.
	curated := make(map[string]bool)
	markCurated := func(tokens []string) {
		mu.Lock()
		defer mu.Unlock()
		for _, tok := range tokens {
			if tok = strings.ToLower(strings.TrimSpace(tok)); tok != "" {
				curated[tok] = true
			}
		}
	}
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
			// Cancelled while waiting for a slot: do not launch a worker, which
			// would receive a semaphore token that was never sent and hang the
			// WaitGroup. Skip the rest so wg.Wait returns.
			continue
		}
		wg.Add(1)
		go func(full, owner, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			recs, eps, err := g.indexRepo(ctx, full, owner, name, bump, markCurated)
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
		rec := githubPersonRecord(login, nil, accounts[login])
		rec.Topics, rec.WeakTopics = splitCurated(expandTopics(tokenCounts), curated)
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
	markCurated func([]string),
) ([]Record, []episode.Episode, error) {
	repo, err := g.client.Repo(ctx, owner, name)
	if err != nil {
		return nil, nil, fmt.Errorf("repo: %w", err)
	}
	// Contributors is a whole-repo snapshot with no since filter, so re-folding
	// it on an incremental run would add each repo topic to every contributor
	// again. Skip it when reading incrementally; those people are preserved from
	// the last full run, and a periodic --full recompacts.
	var conCount int
	if g.opts.Since.IsZero() {
		repoTokens := repoTopicSet(repo)
		markCurated(repo.Topics)
		cons, err := g.client.Contributors(ctx, owner, name)
		if e := g.usable(full, "contributors", len(cons), err); e != nil {
			return nil, nil, fmt.Errorf("contributors: %w", e)
		}
		conCount = len(cons)
		for _, c := range cons {
			bump(c.Login, repoTokens, time.Time{})
		}
	}

	var episodes []episode.Episode
	pulls, err := g.client.PullRequests(ctx, owner, name)
	if e := g.usable(full, "pulls", len(pulls), err); e != nil {
		return nil, nil, fmt.Errorf("pulls: %w", e)
	}
	for _, pr := range pulls {
		// Pulls come newest-first, so on an incremental run stop at the first one
		// older than the watermark; only changed pulls are folded again.
		if !g.opts.Since.IsZero() && pr.UpdatedAt.Before(g.opts.Since) {
			break
		}
		markCurated(pr.LabelNames())
		tokens := append(pr.LabelNames(), phraseTokens(pr.Title)...)
		bump(pr.Author(), tokens, pr.UpdatedAt)
		for _, u := range pr.Reviewers() {
			bump(u, tokens, pr.UpdatedAt)
		}
		for _, u := range pr.AssigneeLogins() {
			bump(u, tokens, pr.UpdatedAt)
		}
		// The list object carries only reviewers still pending; the people who
		// actually reviewed or commented on a merged change, often its real
		// experts, come from the reviews and comments endpoints. Fetch them only
		// for merged pull requests, which are the ones worth the extra calls.
		var helpers []string
		if pr.Merged() {
			helpers = g.pullHelpers(ctx, owner, name, pr.Number)
			for _, u := range helpers {
				bump(u, tokens, pr.UpdatedAt)
			}
		}
		if g.opts.Episodes {
			if ep, ok := changeEpisode(owner, name, pr, helpers); ok {
				episodes = append(episodes, ep)
			}
		}
	}

	issues, err := g.client.Issues(ctx, owner, name, g.opts.Since)
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
		markCurated(is.LabelNames())
		tokens := append(is.LabelNames(), phraseTokens(is.Title)...)
		bump(is.Author(), tokens, is.UpdatedAt)
		for _, u := range is.AssigneeLogins() {
			bump(u, tokens, is.UpdatedAt)
		}
	}

	// CODEOWNERS is a whole-file snapshot with no since filter and rarely changes,
	// so like contributors it is skipped on an incremental run to avoid folding
	// its weight twice.
	var codeOwnerRecords []Record
	if g.opts.Since.IsZero() {
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
	}
	fmt.Fprintf(g.opts.Log, "github: indexed %s (%d contributors, %d pulls, %d issues)\n",
		full, conCount, len(pulls), issueCount)
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

// pullHelpers returns the people who actually reviewed or commented on a merged
// pull request, beyond the author, requested reviewers, and assignees the list
// object already carries. A fetch failure costs those names, not the pull
// request, so it is logged and the rest proceeds.
func (g *GitHub) pullHelpers(ctx context.Context, owner, repo string, number int) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(logins []string, err error, what string) {
		if err != nil {
			fmt.Fprintf(g.opts.Log, "github: %s for %s/%s#%d: %v\n", what, owner, repo, number, err)
			return
		}
		for _, l := range logins {
			if l == "" || seen[l] {
				continue
			}
			seen[l] = true
			out = append(out, l)
		}
	}
	revs, err := g.client.PullReviewers(ctx, owner, repo, number)
	add(revs, err, "reviews")
	comments, err := g.client.PullCommenters(ctx, owner, repo, number)
	add(comments, err, "comments")
	return out
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

// phraseTokens returns the tokens of s plus the two-word phrases formed by
// adjacent surviving tokens, hyphenated. Expertise is often a compound
// ("billing retries", "incident response"), and tokenizing alone shatters it
// into words that each look like a subject of their own. Emitting the phrase
// beside its words lets the compound accumulate its own weight while the single
// words still match a question that only uses one of them.
func phraseTokens(s string) []string {
	words := titleTokens(s)
	if len(words) < 2 {
		return words
	}
	out := make([]string, 0, len(words)*2-1)
	out = append(out, words...)
	for i := 0; i+1 < len(words); i++ {
		out = append(out, words[i]+"-"+words[i+1])
	}
	return out
}

// splitCurated divides topic tokens into the ones some source stated outright
// and the ones inferred from prose, so the index can tell a declared subject
// apart from a word that happened to appear in a title. Order is preserved and
// repeats are kept, because repetition is how volume of work raises a score.
func splitCurated(tokens []string, curated map[string]bool) (strong, weak []string) {
	for _, t := range tokens {
		if curated[strings.ToLower(strings.TrimSpace(t))] {
			strong = append(strong, t)
			continue
		}
		weak = append(weak, t)
	}
	return strong, weak
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
