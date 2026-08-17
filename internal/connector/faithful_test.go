package connector

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/confluence"
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/fakeapi"
	"github.com/kordloom/whodar/internal/github"
	"github.com/kordloom/whodar/internal/jira"
	"github.com/kordloom/whodar/internal/pagerduty"
)

// These tests run the real connectors against servers that enforce the parts
// of each API contract a hand-written stub ignores. A stub answers with
// whatever its author typed no matter what was asked for, which is how whodar
// once shipped a Jira client that produced no episodes at all against a real
// site while every test passed.

// TestJiraAgainstFaithfulSite verifies whodar reads a Jira site that returns
// only the fields it was asked for. Nothing here hands back a field the client
// forgot to request, so a query missing one shows up as absent data rather
// than as a passing test.
func TestJiraAgainstFaithfulSite(t *testing.T) {
	t.Parallel()
	site := &fakeapi.Jira{Issues: []fakeapi.JiraIssue{{
		Key: "OPS-7", Summary: "Retry storm on billing",
		Description:   "Raised the retry ceiling and redeployed the worker.",
		AssigneeEmail: "jane@x.com", ReporterEmail: "pat@x.com",
		Components: []string{"billing"}, Labels: []string{"retries"},
		ProjectKey: "OPS", ProjectName: "Operations",
		Updated:        "2026-06-20T09:30:00.000-0500",
		ResolutionDate: "2026-06-21T09:30:00.000-0500",
		StatusName:     "Done", StatusCategory: "done",
	}, {
		Key: "OPS-8", Summary: "Still open", AssigneeEmail: "bob@x.com",
		ProjectKey: "OPS", ProjectName: "Operations",
		Updated:    "2026-06-22T09:30:00.000-0500",
		StatusName: "In Progress", StatusCategory: "indeterminate",
	}}}
	srv := site.Server()
	t.Cleanup(srv.Close)

	j := NewJiraWithClient(jira.New(srv.URL, "me@x.com", "token"),
		JiraOptions{Projects: []string{"OPS"}, Episodes: true})
	recs, err := j.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("no people came back from a site holding two issues")
	}

	eps := j.Episodes()
	if len(eps) != 1 {
		t.Fatalf("episodes = %d, want only the resolved issue. A site that returns "+
			"only requested fields exposes a query missing resolutiondate or status: %+v", len(eps), eps)
	}
	ep := eps[0]
	if ep.ID != "jira:OPS-7" {
		t.Errorf("episode = %+v", ep)
	}
	// The description is served as the rich-text tree Jira Cloud really sends,
	// so this also proves whodar reads that shape rather than a plain string.
	if !strings.Contains(ep.Body, "retry ceiling") {
		t.Errorf("episode body = %q, want the issue description flattened into it", ep.Body)
	}
	if !hasParticipant(ep, "jane@x.com") || !hasParticipant(ep, "pat@x.com") {
		t.Errorf("participants = %v", ep.Participants)
	}
}

// TestJiraPagesThroughFaithfulSite verifies paging works against a site that
// honors startAt and maxResults rather than returning everything at once.
func TestJiraPagesThroughFaithfulSite(t *testing.T) {
	t.Parallel()
	var issues []fakeapi.JiraIssue
	for i := range 250 {
		issues = append(issues, fakeapi.JiraIssue{
			Key: "OPS-" + strconv.Itoa(i), Summary: "issue", AssigneeEmail: "jane@x.com",
			ProjectKey: "OPS", ProjectName: "Operations",
			Updated:        "2026-06-20T09:30:00.000-0500",
			ResolutionDate: "2026-06-21T09:30:00.000-0500",
			StatusName:     "Done", StatusCategory: "done",
		})
	}
	site := &fakeapi.Jira{Issues: issues}
	srv := site.Server()
	t.Cleanup(srv.Close)

	j := NewJiraWithClient(jira.New(srv.URL, "me@x.com", "token"),
		JiraOptions{Projects: []string{"OPS"}, MaxIssues: 250, Episodes: true})
	if _, err := j.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := len(j.Episodes()); got != 250 {
		t.Errorf("episodes = %d across pages, want 250", got)
	}
	if len(site.Queries) < 3 {
		t.Errorf("site saw %d searches, want the client to page", len(site.Queries))
	}
}

// TestGitHubAgainstFaithfulAPI verifies whodar reads merged pull requests from
// an API that defaults to open ones and pages with a Link header. A client
// that forgets state=all sees nothing merged here, which is what would happen
// in production.
func TestGitHubAgainstFaithfulAPI(t *testing.T) {
	t.Parallel()
	api := &fakeapi.GitHub{PerPage: 2, Repos: []fakeapi.GitHubRepo{{
		Owner: "acme", Name: "billing",
		Contributors: map[string]int{"jane": 40},
		Pulls: []fakeapi.GitHubPull{
			{
				Number: 42, Title: "Raise retry ceiling",
				Body:        "Fixes the retry storm seen during the billing outage.",
				AuthorLogin: "jane", ReviewerLogins: []string{"bob"},
				Labels:    []string{"billing"},
				UpdatedAt: "2026-06-20T09:30:00Z", MergedAt: "2026-06-20T10:00:00Z",
			},
			{
				Number: 43, Title: "Second landed change", AuthorLogin: "cy",
				UpdatedAt: "2026-06-21T09:30:00Z", MergedAt: "2026-06-21T10:00:00Z",
			},
			{
				Number: 44, Title: "Third landed change", AuthorLogin: "dee",
				UpdatedAt: "2026-06-22T09:30:00Z", MergedAt: "2026-06-22T10:00:00Z",
			},
			{
				Number: 45, Title: "Never landed", AuthorLogin: "ed",
				UpdatedAt: "2026-06-23T09:30:00Z",
			},
		},
	}}}
	srv := api.Server()
	t.Cleanup(srv.Close)

	g := NewGitHubWithClient(github.New("token", github.WithBaseURL(srv.URL)),
		GitHubOptions{Repos: []string{"acme/billing"}, Episodes: true})
	if _, err := g.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	eps := g.Episodes()
	if len(eps) != 3 {
		t.Fatalf("episodes = %d, want the three merged changes across paged results: %+v",
			len(eps), eps)
	}
	ep, ok := episodeByID(eps, "github:acme/billing:42")
	if !ok {
		t.Fatalf("first merged change missing: %+v", eps)
	}
	// The description is only on the list object because the real list
	// endpoint carries it; reading it costs no extra request.
	if !strings.Contains(ep.Body, "retry storm") {
		t.Errorf("episode body = %q, want the pull request description indexed", ep.Body)
	}
	if !hasParticipant(ep, "github:jane") || !hasParticipant(ep, "github:bob") {
		t.Errorf("participants = %v", ep.Participants)
	}
}

// TestPagerDutyAgainstFaithfulAPI verifies whodar reads resolved incidents from
// an API that actually applies the status filter and pages by offset, so a
// client asking for the wrong status gets nothing rather than everything.
func TestPagerDutyAgainstFaithfulAPI(t *testing.T) {
	t.Parallel()
	api := &fakeapi.PagerDuty{Incidents: []fakeapi.PagerDutyIncident{{
		ID: "I1", Number: 7, Title: "Billing retries exhausted", Status: "resolved",
		ServiceName: "Billing API", CreatedAt: "2026-08-01T10:00:00Z",
		ResolvedAt:     "2026-08-01T11:00:00Z",
		AssigneeEmails: []string{"jane@x.com"}, AcknowledgerEmails: []string{"bob@x.com"},
	}, {
		ID: "I2", Number: 8, Title: "Still burning", Status: "triggered",
		ServiceName: "Billing API", CreatedAt: "2026-08-02T10:00:00Z",
		AssigneeEmails: []string{"jane@x.com"},
	}}}
	srv := api.Server()
	t.Cleanup(srv.Close)

	p := NewPagerDutyWithClient(
		pagerduty.New("token", pagerduty.WithBaseURL(srv.URL)),
		PagerDutyOptions{Episodes: true})
	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	eps := p.Episodes()
	if len(eps) != 1 {
		t.Fatalf("episodes = %d, want only the resolved incident: %+v", len(eps), eps)
	}
	ep := eps[0]
	if ep.ID != "pagerduty:I1" || ep.Kind != episode.KindIncident {
		t.Errorf("episode = %+v", ep)
	}
	if !hasParticipant(ep, "jane@x.com") || !hasParticipant(ep, "bob@x.com") {
		t.Errorf("participants = %v, want the assignee and the acknowledger", ep.Participants)
	}
	if len(api.Queries) == 0 || !strings.Contains(api.Queries[0], "statuses") {
		t.Errorf("queries = %v, want the client to filter by status", api.Queries)
	}
}

// TestConfluenceAgainstFaithfulSite verifies whodar reads a Confluence site
// that withholds anything it was not asked to expand. Space, labels, history,
// and version all arrive only when named, so an expansion dropped from the
// query shows up as missing people rather than as a passing test.
func TestConfluenceAgainstFaithfulSite(t *testing.T) {
	t.Parallel()
	site := &fakeapi.Confluence{Pages: []fakeapi.ConfluencePage{{
		Title: "Billing retry runbook", SpaceKey: "ENG", SpaceName: "Engineering",
		Labels:         []string{"billing", "runbook"},
		CreatedByEmail: "jane@x.com", CreatedAt: "2026-06-01T09:30:00.000Z",
		EditedByEmail: "bob@x.com", EditedAt: "2026-06-20T09:30:00.000Z",
	}}}
	srv := site.Server()
	t.Cleanup(srv.Close)

	c := NewConfluenceWithClient(
		confluence.New(srv.URL, "me@x.com", "token"),
		ConfluenceOptions{Spaces: []string{"ENG"}})
	recs, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("no people came back; the creator and last editor both expand from history and version")
	}
	var sawCreator, sawEditor bool
	for _, r := range recs {
		switch r.Email {
		case "jane@x.com":
			sawCreator = true
		case "bob@x.com":
			sawEditor = true
		}
		if r.Team == "" && r.Text == "" && len(r.Topics) == 0 {
			t.Errorf("record %+v carries nothing from the expanded page", r)
		}
	}
	if !sawCreator || !sawEditor {
		t.Errorf("records = %+v, want both the page creator and the last editor", recs)
	}
}

// TestJiraReportsProgressWhilePaging verifies the connector shows movement as
// it pages rather than printing one line after every issue has already
// arrived. A long index left a user staring at a still screen otherwise.
func TestJiraReportsProgressWhilePaging(t *testing.T) {
	t.Parallel()
	var issues []fakeapi.JiraIssue
	for i := range 350 {
		issues = append(issues, fakeapi.JiraIssue{
			Key: "OPS-" + strconv.Itoa(i), Summary: "issue", AssigneeEmail: "jane@x.com",
			ProjectKey: "OPS", ProjectName: "Operations",
			Updated:        "2026-06-20T09:30:00.000-0500",
			ResolutionDate: "2026-06-21T09:30:00.000-0500",
			StatusName:     "Done", StatusCategory: "done",
		})
	}
	srv := (&fakeapi.Jira{Issues: issues}).Server()
	t.Cleanup(srv.Close)

	var log strings.Builder
	src := NewJira(srv.URL, "me@x.com", "token", JiraOptions{
		Projects: []string{"OPS"}, MaxIssues: 350, Log: &log,
	})
	if _, err := src.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Four pages of 100, so several interim progress lines, not just the final
	// summary.
	if got := strings.Count(log.String(), "fetched"); got < 3 {
		t.Errorf("saw %d progress lines in:\n%s", got, log.String())
	}
}
