package connector

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/jira"
	"github.com/kordloom/whodar/internal/util"
)

// JiraOptions configures the Jira connector.
type JiraOptions struct {
	// Projects scopes the search to these project keys.
	Projects []string
	// JQL overrides the query entirely when set.
	JQL string
	// MaxIssues caps issues read; zero uses a default.
	MaxIssues int
	// Episodes records resolved issues, so recall can point back at the
	// ticket that settled something.
	Episodes bool
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// withDefaults fills the log writer and issue cap when unset.
func (o JiraOptions) withDefaults() JiraOptions {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.MaxIssues <= 0 {
		o.MaxIssues = 1000
	}
	return o
}

// jiraProgressEvery reports progress each time this many more issues arrive, so
// a long search shows movement without flooding the log.
const jiraProgressEvery = 100

// Jira is a Source that ingests issues and weights the assignee and reporter by
// the components, labels, summary words, and project of the issues they handle.
type Jira struct {
	// client calls the Jira API.
	client *jira.Client
	// opts holds the resolved options.
	opts JiraOptions
	// episodes holds the resolved issues seen by the last Fetch.
	episodes []episode.Episode
}

// NewJira returns a Jira connector for the site, authenticating with an email
// and API token.
func NewJira(baseURL, email, token string, opts JiraOptions) *Jira {
	o := opts.withDefaults()
	client := jira.New(baseURL, email, token,
		jira.WithProgress(util.ProgressWriter(o.Log, "jira: fetched", jiraProgressEvery)))
	return &Jira{client: client, opts: o}
}

// NewJiraWithClient returns a Jira connector using a preconfigured client.
// Tests use it to inject a client pointed at a mock server.
func NewJiraWithClient(client *jira.Client, opts JiraOptions) *Jira {
	if client == nil {
		panic("connector: NewJiraWithClient requires a non-nil client")
	}
	return &Jira{client: client, opts: opts.withDefaults()}
}

// Ping verifies the credentials with a cheap current-user call, so a wizard can
// confirm the site, email, and token before committing to a full index.
func (j *Jira) Ping(ctx context.Context) error {
	return j.client.Ping(ctx)
}

// Fetch searches issues and returns one record per person, weighted by topic.
func (j *Jira) Fetch(ctx context.Context) ([]Record, error) {
	j.episodes = nil
	query := j.jql()
	issues, err := j.client.Search(ctx, query, j.opts.MaxIssues)
	if err != nil {
		return nil, fmt.Errorf("jira search: %w", err)
	}
	fmt.Fprintf(j.opts.Log, "jira: %d issues for %q\n", len(issues), query)

	counts := make(map[string]map[string]int)
	users := make(map[string]jira.User)
	latest := make(map[string]time.Time)
	bump := func(u *jira.User, tokens []string, t time.Time) {
		if u == nil {
			return
		}
		key := jiraUserKey(*u)
		if key == "" {
			return
		}
		c := counts[key]
		if c == nil {
			c = make(map[string]int)
			counts[key] = c
		}
		for _, tok := range tokens {
			if tok = strings.ToLower(strings.TrimSpace(tok)); tok != "" {
				c[tok]++
			}
		}
		if t.After(latest[key]) {
			latest[key] = t
		}
		users[key] = *u
	}

	for _, is := range issues {
		if j.opts.Episodes {
			if ep, ok := issueEpisode(j.client.BaseURL(), is); ok {
				j.episodes = append(j.episodes, ep)
			}
		}
		tokens := issueTopics(is)
		updated := jiraTime(is.Fields.Updated)
		bump(is.Fields.Assignee, tokens, updated)
		bump(is.Fields.Reporter, tokens, updated)
	}

	records := make([]Record, 0, len(counts))
	for key, c := range counts {
		rec := jiraPersonRecord(users[key], expandTopics(c))
		rec.Time = latest[key]
		records = append(records, rec)
	}
	return records, nil
}

// jiraTime parses a Jira timestamp, returning the zero time when none of the
// accepted layouts match. Jira Cloud sends its own ISO 8601 form with a
// colon-less zone, such as "2026-07-05T12:34:56.789-0500", but some deployments
// and fixtures use RFC 3339 with a "Z" zone or without fractional seconds.
func jiraTime(s string) time.Time {
	for _, layout := range []string{
		"2006-01-02T15:04:05.999-0700",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// jql returns the query: an explicit JQL, or a project scope, or all issues.
func (j *Jira) jql() string {
	if strings.TrimSpace(j.opts.JQL) != "" {
		return j.opts.JQL
	}
	if len(j.opts.Projects) > 0 {
		quoted := make([]string, len(j.opts.Projects))
		for i, p := range j.opts.Projects {
			quoted[i] = `"` + p + `"`
		}
		return "project in (" + strings.Join(quoted, ",") + ") ORDER BY updated DESC"
	}
	return "ORDER BY updated DESC"
}

// issueTopics derives topic tokens from an issue's components, labels, summary,
// and project name.
func issueTopics(is jira.Issue) []string {
	f := is.Fields
	var out []string
	for _, c := range f.Components {
		out = append(out, titleTokens(c.Name)...)
	}
	out = append(out, f.Labels...)
	out = append(out, titleTokens(f.Summary)...)
	out = append(out, titleTokens(f.Project.Name)...)
	return out
}

// jiraUserKey returns a stable key for a user, preferring email.
func jiraUserKey(u jira.User) string {
	if u.EmailAddress != "" {
		return strings.ToLower(u.EmailAddress)
	}
	if u.AccountID != "" {
		return "jira:" + u.AccountID
	}
	return ""
}

// jiraPersonRecord builds a person record. The account id always keys the
// record and any email travels with it, so the indexer joins the two and work
// recorded under a Jira account is findable by the person who did it.
func jiraPersonRecord(u jira.User, topics []string) Record {
	rec := Record{Kind: KindPerson, Source: "jira", Weight: 1, Topics: topics, Name: u.DisplayName}
	if u.AccountID != "" {
		rec.PersonID = "jira:" + u.AccountID
	}
	if u.EmailAddress != "" {
		rec.Email = util.NormalizeEmail(u.EmailAddress)
	}
	if rec.Name == "" {
		if rec.Email != "" {
			rec.Name = rec.Email
		} else {
			rec.Name = rec.PersonID
		}
	}
	return rec
}
