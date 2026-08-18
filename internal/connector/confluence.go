package connector

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/confluence"
	"github.com/kordloom/whodar/internal/util"
)

// ConfluenceOptions configures the Confluence connector.
type ConfluenceOptions struct {
	// Spaces scopes the search to these space keys.
	Spaces []string
	// CQL overrides the query entirely when set.
	CQL string
	// MaxPages caps pages read; zero uses a default.
	MaxPages int
	// Server selects a self-hosted Server or Data Center deployment, which
	// serves the REST API at the site root and authenticates with a bearer
	// token or not at all, rather than Cloud's /wiki path and email-plus-token.
	Server bool
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// withDefaults fills the log writer and page cap when unset.
func (o ConfluenceOptions) withDefaults() ConfluenceOptions {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 2000
	}
	return o
}

// Confluence is a Source that ingests pages and weights their creator and last
// editor by the labels, title words, and space of the pages they wrote.
type Confluence struct {
	// client calls the Confluence API.
	client *confluence.Client
	// opts holds the resolved options.
	opts ConfluenceOptions
}

// confluenceProgressEvery reports progress each time this many more pages
// arrive.
const confluenceProgressEvery = 100

// NewConfluence returns a Confluence connector for the site, authenticating with
// an email and API token.
func NewConfluence(siteURL, email, token string, opts ConfluenceOptions) *Confluence {
	o := opts.withDefaults()
	progress := confluence.WithProgress(
		util.ProgressWriter(o.Log, "confluence: fetched", confluenceProgressEvery))
	var client *confluence.Client
	if o.Server {
		client = confluence.NewServer(siteURL, token, progress)
	} else {
		client = confluence.New(siteURL, email, token, progress)
	}
	return &Confluence{client: client, opts: o}
}

// NewConfluenceWithClient returns a Confluence connector using a preconfigured
// client. Tests use it to inject a client pointed at a mock server.
func NewConfluenceWithClient(client *confluence.Client, opts ConfluenceOptions) *Confluence {
	if client == nil {
		panic("connector: NewConfluenceWithClient requires a non-nil client")
	}
	return &Confluence{client: client, opts: opts.withDefaults()}
}

// Ping verifies the credentials with a cheap current-user call, so a wizard can
// confirm the site, email, and token before committing to a full index.
func (c *Confluence) Ping(ctx context.Context) error {
	return c.client.Ping(ctx)
}

// Fetch searches pages and returns one record per person, weighted by topic.
func (c *Confluence) Fetch(ctx context.Context) ([]Record, error) {
	query := c.cql()
	pages, err := c.client.Pages(ctx, query, c.opts.MaxPages)
	if err != nil {
		return nil, fmt.Errorf("confluence search: %w", err)
	}
	fmt.Fprintf(c.opts.Log, "confluence: %d pages for %q\n", len(pages), query)

	counts := make(map[string]map[string]int)
	users := make(map[string]confluence.User)
	latest := make(map[string]time.Time)
	bump := func(u *confluence.User, tokens []string, t time.Time) {
		if u == nil {
			return
		}
		key := confluenceUserKey(*u)
		if key == "" {
			return
		}
		m := counts[key]
		if m == nil {
			m = make(map[string]int)
			counts[key] = m
		}
		for _, tok := range tokens {
			if tok = strings.ToLower(strings.TrimSpace(tok)); tok != "" {
				m[tok]++
			}
		}
		if t.After(latest[key]) {
			latest[key] = t
		}
		users[key] = *u
	}

	for _, page := range pages {
		tokens := pageTopics(page)
		creator, editor := page.History.CreatedBy, page.Version.By
		// Credit the creator at creation time and the last editor at edit time,
		// so an old page edited yesterday does not make its author look recently
		// active. A person who did both is credited once, at their later action.
		editorIsCreator := creator != nil && editor != nil &&
			confluenceUserKey(*creator) == confluenceUserKey(*editor)
		if creator != nil {
			when := page.History.CreatedAt
			if editorIsCreator && page.Version.When.After(when) {
				when = page.Version.When
			}
			bump(creator, tokens, when)
		}
		if editor != nil && !editorIsCreator {
			bump(editor, tokens, page.Version.When)
		}
	}

	records := make([]Record, 0, len(counts))
	for key, m := range counts {
		rec := confluencePersonRecord(users[key], expandTopics(m))
		rec.Time = latest[key]
		records = append(records, rec)
	}
	return records, nil
}

// cql returns the query: an explicit CQL, or a space scope, or all pages.
func (c *Confluence) cql() string {
	if strings.TrimSpace(c.opts.CQL) != "" {
		return c.opts.CQL
	}
	if len(c.opts.Spaces) > 0 {
		quoted := make([]string, len(c.opts.Spaces))
		for i, s := range c.opts.Spaces {
			quoted[i] = `"` + s + `"`
		}
		return "type = page and space in (" + strings.Join(quoted, ",") + ")"
	}
	return "type = page"
}

// pageTopics derives topic tokens from a page's labels, title, and space name.
func pageTopics(p confluence.Page) []string {
	var out []string
	out = append(out, p.LabelNames()...)
	out = append(out, titleTokens(p.Title)...)
	out = append(out, titleTokens(p.Space.Name)...)
	return out
}

// confluenceUserKey returns a stable key for a user, preferring email.
func confluenceUserKey(u confluence.User) string {
	if u.Email != "" {
		return strings.ToLower(u.Email)
	}
	if id := u.Identity(); id != "" {
		return "confluence:" + id
	}
	return ""
}

// confluencePersonRecord builds a person record. An email lets the person join
// other sources; otherwise the account id keys the record.
func confluencePersonRecord(u confluence.User, topics []string) Record {
	rec := Record{Kind: KindPerson, Source: "confluence", Weight: 1, Topics: topics, Name: u.DisplayName}
	if u.Email != "" {
		rec.Email = util.NormalizeEmail(u.Email)
	} else if id := u.Identity(); id != "" {
		rec.PersonID = "confluence:" + id
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
