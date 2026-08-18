package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/kordloom/whodar/internal/util"
)

// Git history ingest bounds. A year of history captures current ownership;
// the commit cap keeps one huge repository from dominating a run.
const (
	// defaultGitSinceDays bounds how far back history is read.
	defaultGitSinceDays = 365
	// defaultMaxCommits caps commits read per repository.
	defaultMaxCommits = 2000
	// maxRootCommitFiles bounds the files a parentless commit may credit. A root
	// commit diffs against the empty tree, so a wholesale import would otherwise
	// credit its committer with every path in the repository.
	maxRootCommitFiles = 100
)

// GitOptions configures the git history connector.
type GitOptions struct {
	// Paths are local repository roots to read.
	Paths []string
	// SinceDays bounds how far back to read history; zero means one year.
	SinceDays int
	// MaxCommits caps commits read per repository; zero means 2000.
	MaxCommits int
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// withDefaults fills unset options.
func (o GitOptions) withDefaults() GitOptions {
	if o.SinceDays <= 0 {
		o.SinceDays = defaultGitSinceDays
	}
	if o.MaxCommits <= 0 {
		o.MaxCommits = defaultMaxCommits
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	return o
}

// GitHistory is a Source that mines commit authors per changed path, so the
// people doing the work on a system surface even when nothing declares
// ownership. Authors join other sources by commit email.
type GitHistory struct {
	// opts holds the ingest configuration.
	opts GitOptions
}

// NewGitHistory returns a git history source over the given repositories.
func NewGitHistory(opts GitOptions) *GitHistory {
	return &GitHistory{opts: opts.withDefaults()}
}

// Fetch reads each repository's recent history and returns one record per
// author, weighted by how often they touched each topic.
func (g *GitHistory) Fetch(ctx context.Context) ([]Record, error) {
	if len(g.opts.Paths) == 0 {
		return nil, ErrNoRepoPaths
	}

	counts := make(map[string]map[string]int)
	names := make(map[string]string)
	latest := make(map[string]time.Time)
	for _, path := range g.opts.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, err := g.readRepo(ctx, path, counts, names, latest)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			fmt.Fprintf(g.opts.Log, "git: skipping %s: %v\n", path, err)
			continue
		}
		fmt.Fprintf(g.opts.Log, "git: %s: %d commits\n", path, read)
	}

	records := make([]Record, 0, len(counts))
	for email, c := range counts {
		rec := Record{
			Kind:   KindPerson,
			Source: "git",
			Weight: 1,
			Email:  email,
			Name:   names[email],
			Topics: expandTopics(c),
			Time:   latest[email],
		}
		// A GitHub noreply commit email encodes the author's login, so key the
		// person by that login to join their commits to their GitHub reviews
		// and pull requests. Without this the same engineer appears once from
		// git and again from github, since the two share no other identifier.
		if login, ok := util.GitHubNoreplyLogin(email); ok {
			rec.PersonID = "github:" + strings.ToLower(login)
		}
		records = append(records, rec)
	}
	return records, nil
}

// readRepo walks one repository's log, accumulating per-author topic counts,
// display names, and latest activity. Authors are canonicalized through the
// repository's .mailmap so one person's several emails merge. It returns the
// number of commits read.
func (g *GitHistory) readRepo(
	ctx context.Context,
	path string,
	counts map[string]map[string]int,
	names map[string]string,
	latest map[string]time.Time,
) (int, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return 0, fmt.Errorf("git: open %s: %w", path, err)
	}
	mm := loadMailmap(path)
	since := time.Now().AddDate(0, 0, -g.opts.SinceDays)
	iter, err := repo.Log(&git.LogOptions{Since: &since})
	if err != nil {
		return 0, fmt.Errorf("git: log %s: %w", path, err)
	}
	defer iter.Close()

	read := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if read >= g.opts.MaxCommits {
			return storer.ErrStop
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name, email := c.Author.Name, c.Author.Email
		if mm != nil {
			name, email = mm.resolve(name, email)
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || c.NumParents() > 1 || isBotAuthor(name, email) {
			return nil
		}
		stats, err := c.Stats()
		if err != nil {
			fmt.Fprintf(g.opts.Log, "git: %s: stats for %s: %v\n", path, c.Hash, err)
			return nil
		}
		if c.NumParents() == 0 && len(stats) > maxRootCommitFiles {
			return nil
		}
		read++
		newer := c.Author.When.After(latest[email])
		if newer {
			latest[email] = c.Author.When
		}
		if name != "" && (newer || names[email] == "") {
			names[email] = name
		}
		m := counts[email]
		if m == nil {
			m = make(map[string]int)
			counts[email] = m
		}
		for _, stat := range stats {
			for _, tok := range pathTopics(stat.Name) {
				m[tok]++
			}
		}
		// The commit subject carries the domain vocabulary the filenames often
		// hide: "fix rate-limiter backoff" against a generically named limiter.go.
		// Mine it once per commit, so it weighs less than the per-file paths but
		// still lets ask match what the work was about, not just which files moved.
		for _, tok := range titleTokens(commitSubject(c.Message)) {
			m[tok]++
		}
		return nil
	})
	if err != nil {
		return read, fmt.Errorf("git: walk %s: %w", path, err)
	}
	return read, nil
}

// commitSubject returns the first line of a commit message, the summary that
// carries the domain vocabulary, without the body or trailers such as
// Signed-off-by that would only add name and email noise.
func commitSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

// isBotAuthor reports whether a commit author is an automation account, such
// as dependabot, whose activity says nothing about human expertise.
func isBotAuthor(name, email string) bool {
	return strings.HasSuffix(name, "[bot]") || strings.Contains(email, "[bot]")
}
