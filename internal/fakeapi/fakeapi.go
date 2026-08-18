// Package fakeapi serves the source APIs whodar reads, enforcing the parts of
// each contract that a hand-written stub quietly ignores.
//
// A stub written inside a test answers with whatever its author typed,
// whatever the client asked for. Real APIs do not: Jira returns only the
// fields named in the query, GitHub's list endpoint carries a narrower object
// than the single-item one, and both paginate on the caller's terms. A client
// that reads a field it never requested works perfectly against a stub and
// returns nothing in production, which is exactly what once made whodar
// produce no Jira episodes at all while every test passed.
//
// These servers close that gap. They are deliberately strict: asking for
// something the real API would not give back yields nothing here either.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
)

// writeJSON writes v as the response body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// JiraIssue is one issue a fake Jira site holds. It is the whole issue; what
// reaches the client depends on the fields that client asks for.
type JiraIssue struct {
	// Key is the issue key, such as OPS-7.
	Key string
	// Summary is the issue title.
	Summary string
	// Description is the issue description, served as the rich-text document
	// Jira Cloud returns rather than as a string.
	Description string
	// AssigneeEmail is the assignee's email; empty leaves the issue unassigned.
	AssigneeEmail string
	// ReporterEmail is the reporter's email.
	ReporterEmail string
	// Components are the issue components.
	Components []string
	// Labels are the issue labels.
	Labels []string
	// ProjectKey is the project key.
	ProjectKey string
	// ProjectName is the project name.
	ProjectName string
	// Updated is the last update time in Jira's format.
	Updated string
	// ResolutionDate is when the issue was resolved; empty means unresolved.
	ResolutionDate string
	// StatusName is the workflow status name, such as Done.
	StatusName string
	// StatusCategory is the status category key, "done" once finished.
	StatusCategory string
}

// fields returns the issue as Jira's full field map, before the caller's
// field selection narrows it.
func (i JiraIssue) fields() map[string]any {
	user := func(email string) any {
		if email == "" {
			return nil
		}
		return map[string]any{
			"accountId":    "acct-" + email,
			"emailAddress": email,
			"displayName":  email,
		}
	}
	components := make([]any, 0, len(i.Components))
	for _, c := range i.Components {
		components = append(components, map[string]any{"name": c})
	}
	out := map[string]any{
		"summary":    i.Summary,
		"assignee":   user(i.AssigneeEmail),
		"reporter":   user(i.ReporterEmail),
		"components": components,
		"labels":     i.Labels,
		"project":    map[string]any{"key": i.ProjectKey, "name": i.ProjectName},
		"issuetype":  map[string]any{"name": "Task"},
		"updated":    i.Updated,
		"status": map[string]any{
			"name":           i.StatusName,
			"statusCategory": map[string]any{"key": i.StatusCategory},
		},
	}
	if i.ResolutionDate != "" {
		out["resolutiondate"] = i.ResolutionDate
	}
	if i.Description != "" {
		out["description"] = adfDoc(i.Description)
	}
	return out
}

// serverFields returns the issue as a Server or Data Center site does: users
// keyed by username with no account id or email, and a plain string
// description rather than a node tree.
func (i JiraIssue) serverFields() map[string]any {
	user := func(email string) any {
		if email == "" {
			return nil
		}
		name, _, _ := strings.Cut(email, "@")
		return map[string]any{"name": name, "displayName": name}
	}
	components := make([]any, 0, len(i.Components))
	for _, c := range i.Components {
		components = append(components, map[string]any{"name": c})
	}
	out := map[string]any{
		"summary":    i.Summary,
		"assignee":   user(i.AssigneeEmail),
		"reporter":   user(i.ReporterEmail),
		"components": components,
		"labels":     i.Labels,
		"project":    map[string]any{"key": i.ProjectKey, "name": i.ProjectName},
		"issuetype":  map[string]any{"name": "Task"},
		"updated":    i.Updated,
		"status": map[string]any{
			"name":           i.StatusName,
			"statusCategory": map[string]any{"key": i.StatusCategory},
		},
	}
	if i.ResolutionDate != "" {
		out["resolutiondate"] = i.ResolutionDate
	}
	if i.Description != "" {
		out["description"] = i.Description
	}
	return out
}

// adfDoc wraps text in the Atlassian Document Format tree Jira Cloud returns
// for a rich-text field, so a client that expects a plain string fails here
// the way it would in production.
func adfDoc(text string) map[string]any {
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": text}},
		}},
	}
}

// Jira is a fake Jira site. It honors the field selection and the paging window
// the client sends, which is what a stub does not. With ServerMode set it
// behaves as a self-hosted Server or Data Center site: the v2 API path,
// username-based users with no account id, and a string description instead of
// a node tree.
type Jira struct {
	// Issues are the issues the site holds.
	Issues []JiraIssue
	// ServerMode serves the v2 API and Server-shaped users and descriptions.
	ServerMode bool
	// Queries records the raw query string of every search request, so a test
	// can assert what the client actually asked for.
	Queries []string
}

// Server starts the fake site. The caller closes it.
func (j *Jira) Server() *httptest.Server {
	base := "/rest/api/3"
	if j.ServerMode {
		base = "/rest/api/2"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/myself", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"accountId": "acct-me", "emailAddress": "me@x.com"})
	})
	mux.HandleFunc(base+"/serverInfo", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"version": "9.4.0", "deploymentType": "Server"})
	})
	mux.HandleFunc(base+"/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		j.Queries = append(j.Queries, r.URL.RawQuery)

		// Only the named fields come back. This is the whole point of the
		// fake: a client that reads a field it did not request sees it empty.
		wanted := make(map[string]bool)
		for _, name := range strings.Split(q.Get("fields"), ",") {
			if name = strings.TrimSpace(name); name != "" {
				wanted[name] = true
			}
		}

		startAt, _ := strconv.Atoi(q.Get("startAt"))
		maxResults, err := strconv.Atoi(q.Get("maxResults"))
		if err != nil || maxResults <= 0 {
			maxResults = 50
		}
		issues := make([]any, 0, maxResults)
		for i := startAt; i < len(j.Issues) && len(issues) < maxResults; i++ {
			all := j.Issues[i].fields()
			if j.ServerMode {
				all = j.Issues[i].serverFields()
			}
			selected := make(map[string]any)
			for name, value := range all {
				if wanted[name] {
					selected[name] = value
				}
			}
			issues = append(issues, map[string]any{"key": j.Issues[i].Key, "fields": selected})
		}
		writeJSON(w, map[string]any{
			"issues": issues, "startAt": startAt, "total": len(j.Issues),
			"maxResults": maxResults,
		})
	})
	return httptest.NewServer(mux)
}

// GitHubPull is one pull request a fake repository holds.
type GitHubPull struct {
	// Number is the pull request number.
	Number int
	// Title is the pull request title.
	Title string
	// Body is the description.
	Body string
	// AuthorLogin is the author's handle.
	AuthorLogin string
	// ReviewerLogins are the requested reviewers.
	ReviewerLogins []string
	// Labels are the applied labels.
	Labels []string
	// UpdatedAt is when it last changed, in RFC 3339.
	UpdatedAt string
	// MergedAt is when it merged, in RFC 3339; empty means never merged.
	MergedAt string
}

// listObject returns the pull request exactly as the list endpoint sends it.
// Fields the single-pull endpoint adds, such as merged, mergeable, and the
// file and comment counts, are deliberately absent: a client that reads one
// of those from a list response would get nothing in production.
func (p GitHubPull) listObject(owner, repo string) map[string]any {
	labels := make([]any, 0, len(p.Labels))
	for _, l := range p.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	reviewers := make([]any, 0, len(p.ReviewerLogins))
	for _, l := range p.ReviewerLogins {
		reviewers = append(reviewers, map[string]any{"login": l})
	}
	state := "open"
	if p.MergedAt != "" {
		state = "closed"
	}
	out := map[string]any{
		"number":              p.Number,
		"state":               state,
		"title":               p.Title,
		"body":                p.Body,
		"html_url":            fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, p.Number),
		"user":                map[string]any{"login": p.AuthorLogin},
		"labels":              labels,
		"requested_reviewers": reviewers,
		"assignees":           []any{},
		"updated_at":          p.UpdatedAt,
		"draft":               false,
	}
	if p.MergedAt != "" {
		out["merged_at"] = p.MergedAt
	}
	return out
}

// GitHubRepo is one repository a fake GitHub holds.
type GitHubRepo struct {
	// Owner is the owning account or org.
	Owner string
	// Name is the repository name.
	Name string
	// Contributors maps a login to its commit count.
	Contributors map[string]int
	// Pulls are the repository's pull requests, of any state.
	Pulls []GitHubPull
}

// GitHub is a fake GitHub API. It pages with a Link header and answers the
// list endpoint with the narrower object the real list endpoint returns.
type GitHub struct {
	// Repos are the repositories the API holds.
	Repos []GitHubRepo
	// PerPage caps a page; zero honors whatever the client asks for.
	PerPage int
}

// Server starts the fake API. The caller closes it.
func (g *GitHub) Server() *httptest.Server {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/repos/"), "/"), "/")
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		repo := g.find(parts[0], parts[1])
		if repo == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 2:
			writeJSON(w, map[string]any{
				"name": repo.Name, "full_name": repo.Owner + "/" + repo.Name,
			})
		case parts[2] == "contributors":
			out := make([]any, 0, len(repo.Contributors))
			for login, count := range repo.Contributors {
				out = append(out, map[string]any{"login": login, "contributions": count})
			}
			writeJSON(w, out)
		case parts[2] == "pulls":
			g.servePulls(w, r, srv, repo)
		default:
			http.NotFound(w, r)
		}
	})
	srv = httptest.NewServer(mux)
	return srv
}

// servePulls answers the list endpoint, honoring the state filter and paging
// with a Link header the way GitHub does.
func (g *GitHub) servePulls(
	w http.ResponseWriter, r *http.Request, srv *httptest.Server, repo *GitHubRepo,
) {
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		// GitHub defaults to open, which is what silently hides every merged
		// pull request from a client that forgets to ask for all of them.
		state = "open"
	}
	var matching []GitHubPull
	for _, p := range repo.Pulls {
		merged := p.MergedAt != ""
		if state == "all" || (state == "open" && !merged) || (state == "closed" && merged) {
			matching = append(matching, p)
		}
	}

	perPage, err := strconv.Atoi(q.Get("per_page"))
	if err != nil || perPage <= 0 {
		perPage = 30
	}
	if g.PerPage > 0 && g.PerPage < perPage {
		perPage = g.PerPage
	}
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	start := (page - 1) * perPage
	if start > len(matching) {
		start = len(matching)
	}
	end := min(start+perPage, len(matching))
	out := make([]any, 0, end-start)
	for _, p := range matching[start:end] {
		out = append(out, p.listObject(repo.Owner, repo.Name))
	}
	if end < len(matching) && srv != nil {
		next := fmt.Sprintf("%s%s?state=%s&per_page=%d&page=%d",
			srv.URL, r.URL.Path, state, perPage, page+1)
		w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"next\"", next))
	}
	writeJSON(w, out)
}

// find returns the named repository, or nil.
func (g *GitHub) find(owner, name string) *GitHubRepo {
	for i := range g.Repos {
		if g.Repos[i].Owner == owner && g.Repos[i].Name == name {
			return &g.Repos[i]
		}
	}
	return nil
}

// PagerDutyIncident is one incident a fake PagerDuty holds.
type PagerDutyIncident struct {
	// ID is the incident id.
	ID string
	// Number is the human-facing incident number.
	Number int
	// Title is the incident title.
	Title string
	// Status is the incident status, such as resolved or triggered.
	Status string
	// ServiceName is the affected service.
	ServiceName string
	// CreatedAt is when it opened, in RFC 3339.
	CreatedAt string
	// ResolvedAt is when it resolved, in RFC 3339; empty means unresolved.
	ResolvedAt string
	// AssigneeEmails are the people it was assigned to.
	AssigneeEmails []string
	// AcknowledgerEmails are the people who picked it up.
	AcknowledgerEmails []string
	// ResolverEmail is who resolved the incident, emitted as
	// last_status_change_by, which a manually-resolved incident carries even
	// with no assignee.
	ResolverEmail string
}

// PagerDuty is a fake PagerDuty API honoring the status filter and the
// offset paging the client sends.
type PagerDuty struct {
	// Incidents are the incidents the API holds.
	Incidents []PagerDutyIncident
	// Queries records each incidents query, so a test can assert the filter.
	Queries []string
}

// Server starts the fake API. The caller closes it.
func (p *PagerDuty) Server() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/users", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"users": []any{}, "more": false})
	})
	mux.HandleFunc("/services", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"services": []any{}, "more": false})
	})
	mux.HandleFunc("/oncalls", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"oncalls": []any{}, "more": false})
	})
	mux.HandleFunc("/incidents", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p.Queries = append(p.Queries, r.URL.RawQuery)
		wantStatuses := q["statuses[]"]

		var matching []PagerDutyIncident
		for _, in := range p.Incidents {
			if len(wantStatuses) == 0 {
				matching = append(matching, in)
				continue
			}
			for _, s := range wantStatuses {
				if in.Status == s {
					matching = append(matching, in)
					break
				}
			}
		}

		offset, _ := strconv.Atoi(q.Get("offset"))
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit <= 0 {
			limit = 25
		}
		if offset > len(matching) {
			offset = len(matching)
		}
		end := min(offset+limit, len(matching))
		out := make([]any, 0, end-offset)
		for _, in := range matching[offset:end] {
			out = append(out, in.object())
		}
		writeJSON(w, map[string]any{"incidents": out, "more": end < len(matching)})
	})
	return httptest.NewServer(mux)
}

// object returns the incident as the incidents endpoint sends it.
func (i PagerDutyIncident) object() map[string]any {
	person := func(email string) map[string]any {
		return map[string]any{"id": "PD-" + email, "summary": email, "email": email}
	}
	assignments := make([]any, 0, len(i.AssigneeEmails))
	for _, e := range i.AssigneeEmails {
		assignments = append(assignments, map[string]any{"assignee": person(e)})
	}
	acks := make([]any, 0, len(i.AcknowledgerEmails))
	for _, e := range i.AcknowledgerEmails {
		acks = append(acks, map[string]any{"acknowledger": person(e)})
	}
	out := map[string]any{
		"id":               i.ID,
		"incident_number":  i.Number,
		"title":            i.Title,
		"status":           i.Status,
		"html_url":         "https://x.pagerduty.com/incidents/" + i.ID,
		"created_at":       i.CreatedAt,
		"service":          map[string]any{"id": "S-" + i.ServiceName, "summary": i.ServiceName},
		"assignments":      assignments,
		"acknowledgements": acks,
	}
	if i.ResolvedAt != "" {
		out["resolved_at"] = i.ResolvedAt
	}
	if i.ResolverEmail != "" {
		out["last_status_change_by"] = map[string]any{
			"id": "PD-" + i.ResolverEmail, "type": "user_reference",
			"summary": i.ResolverEmail, "email": i.ResolverEmail,
		}
	}
	return out
}

// ConfluencePage is one page a fake Confluence site holds.
type ConfluencePage struct {
	// Title is the page title.
	Title string
	// SpaceKey is the space key.
	SpaceKey string
	// SpaceName is the space name.
	SpaceName string
	// Labels are the page labels.
	Labels []string
	// CreatedByEmail is the page creator's email.
	CreatedByEmail string
	// CreatedAt is when the page was created, in RFC 3339.
	CreatedAt string
	// EditedByEmail is the last editor's email.
	EditedByEmail string
	// EditedAt is when the page was last edited, in RFC 3339.
	EditedAt string
}

// sections returns the page split by the expansion each part needs, so the
// server can withhold anything the client did not ask to expand.
func (p ConfluencePage) sections(server bool) map[string]any {
	user := func(email string) any {
		if email == "" {
			return nil
		}
		if server {
			// Server and Data Center: username and user key, no account id or
			// email, exactly as a public wiki such as Apache's returns.
			name, _, _ := strings.Cut(email, "@")
			return map[string]any{"username": name, "userKey": "key-" + name, "displayName": name}
		}
		return map[string]any{
			"accountId":   "acct-" + email,
			"email":       email,
			"publicName":  email,
			"displayName": email,
		}
	}
	labels := make([]any, 0, len(p.Labels))
	for _, l := range p.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	return map[string]any{
		"space": map[string]any{"key": p.SpaceKey, "name": p.SpaceName},
		"metadata.labels": map[string]any{
			"labels": map[string]any{"results": labels},
		},
		"history": map[string]any{
			"history": map[string]any{"createdBy": user(p.CreatedByEmail), "createdDate": p.CreatedAt},
		},
		"version": map[string]any{
			"version": map[string]any{"by": user(p.EditedByEmail), "when": p.EditedAt},
		},
	}
}

// Confluence is a fake Confluence site. It withholds anything the client did
// not name in expand, which is how the real API behaves and what turns a
// forgotten expansion into a visibly empty field rather than a passing test.
// With ServerMode set it behaves as a self-hosted Server or Data Center site:
// the REST API at the root rather than under /wiki, and username-based users.
type Confluence struct {
	// ServerMode serves the Server/Data Center path and user shape.
	ServerMode bool
	// Pages are the pages the site holds.
	Pages []ConfluencePage
	// Queries records each search query string.
	Queries []string
}

// Server starts the fake site. The caller closes it.
func (c *Confluence) Server() *httptest.Server {
	base := "/wiki/rest/api"
	if c.ServerMode {
		base = "/rest/api"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/space", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"results": []any{}, "size": 0})
	})
	mux.HandleFunc(base+"/user/current", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"accountId": "acct-me", "email": "me@x.com"})
	})
	mux.HandleFunc(base+"/content/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		c.Queries = append(c.Queries, r.URL.RawQuery)
		expanded := make(map[string]bool)
		for _, name := range strings.Split(q.Get("expand"), ",") {
			if name = strings.TrimSpace(name); name != "" {
				expanded[name] = true
			}
		}
		start, _ := strconv.Atoi(q.Get("start"))
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit <= 0 {
			limit = 25
		}
		if start > len(c.Pages) {
			start = len(c.Pages)
		}
		end := min(start+limit, len(c.Pages))
		results := make([]any, 0, end-start)
		for _, p := range c.Pages[start:end] {
			out := map[string]any{"title": p.Title, "type": "page"}
			for name, section := range p.sections(c.ServerMode) {
				if !expanded[name] {
					continue
				}
				// A section may carry its own key, as history and version do.
				for k, v := range section.(map[string]any) {
					out[k] = v
				}
				if name == "space" {
					out["space"] = section
				}
			}
			results = append(results, out)
		}
		writeJSON(w, map[string]any{"results": results, "size": len(results), "limit": limit})
	})
	return httptest.NewServer(mux)
}
