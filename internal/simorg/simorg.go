// Package simorg simulates a small company across every source whodar reads,
// serving each tool's wire format from in-process HTTP servers. It exercises
// the full pipeline end to end, at the wire level, without any credentials.
package simorg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/kordloom/whodar/internal/confluence"
	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/github"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/jira"
	"github.com/kordloom/whodar/internal/pagerduty"
	"github.com/kordloom/whodar/internal/slack"
)

// The simulated cast. The Payments team is Angela, Oscar, Kevin, and Pam;
// Bob, Carol, Dan, Eve, Frank, Grace, and Heidi round out the rest of the
// company. Victor owned terraform two years ago and must lose to Carol on
// recency. Eve's GitHub account exposes no email, so only an alias joins her;
// bots must be skipped everywhere.

// OrgCSV returns the org chart in the org-csv source format.
func OrgCSV() string {
	return `name,email,title,team,topics
Angela Malone,angela@corp.com,Staff Engineer,Payments,billing;retries
Oscar Scott,oscar@corp.com,Engineering Manager,Payments,billing;roadmap
Kevin Novak,kevin@corp.com,Software Engineer,Payments,retries
Pam Vance,pam@corp.com,Support Lead,Payments,billing;support
Bob Smith,bob@corp.com,Senior Engineer,Data Platform,kafka;streaming
Carol Lee,carol@corp.com,Site Reliability Engineer,Infrastructure,deploys
Victor Old,victor@corp.com,Systems Engineer,Infrastructure,
Dan Park,dan@corp.com,Security Engineer,Security,oauth;sso
Eve Ng,eve@corp.com,Frontend Engineer,Web,react;frontend
Frank Ito,frank@corp.com,Machine Learning Engineer,ML Platform,embeddings;models
Grace Kim,grace@corp.com,Site Reliability Engineer,Infrastructure,oncall;incidents
Heidi Cho,heidi@corp.com,Search Engineer,Search,relevance
`
}

// CodeOwners returns a CODEOWNERS file owning terraform paths by email.
func CodeOwners() string {
	return "*.tf carol@corp.com\ninfra/ carol@corp.com\n"
}

// Aliases returns the alias file joining Eve's email-less GitHub login.
func Aliases() string {
	return `{"eve@corp.com": ["github:eve-dev"]}`
}

// SlackServer serves users, channels, and history in Slack's wire format. The
// first history call returns HTTP 429 so the client's retry path runs.
func SlackServer() *httptest.Server {
	users := []map[string]any{
		slackUser("U1", "Angela Malone", "angela@corp.com", "Staff Engineer"),
		slackUser("U2", "Bob Smith", "bob@corp.com", "Senior Engineer"),
		slackUser("U3", "Carol Lee", "carol@corp.com", "Site Reliability Engineer"),
		slackUser("U4", "Dan Park", "dan@corp.com", "Security Engineer"),
		slackUser("U5", "Grace Kim", "grace@corp.com", "Site Reliability Engineer"),
		slackUser("U6", "Oscar Scott", "oscar@corp.com", "Engineering Manager"),
	}
	channels := []map[string]any{
		slackChannel("C1", "payments", "billing and payment questions"),
		slackChannel("C2", "data-platform", "kafka and streaming"),
		slackChannel("C3", "infra", "kubernetes deploys and oncall"),
		slackChannel("C4", "security", "auth login and sso"),
	}
	history := map[string][]map[string]any{
		"C1": {
			slackMessage("U1", "billing retry backoff is fixed, dunning next", daysAgo(2)),
			slackMessage("U1", "payments reconciliation ran clean", daysAgo(1)),
			slackMessage("U6", "billing roadmap review is on for next week", daysAgo(3)),
		},
		"C2": {
			slackMessage("U2", "kafka consumer lag is back to zero", daysAgo(3)),
		},
		"C3": {
			slackMessage("U3", "terraform plan for the new cluster is up", daysAgo(2)),
			slackMessage("U5", "paging policy updated after the incident", daysAgo(4)),
		},
		"C4": {
			slackMessage("U4", "sso login flow now enforces mfa", daysAgo(5)),
		},
	}

	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/users.list", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "members": users})
	})
	mux.HandleFunc("/conversations.list", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "channels": channels})
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		limited := false
		once.Do(func() { limited = true })
		if limited {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = r.ParseForm()
		writeJSON(w, map[string]any{
			"ok": true, "has_more": false, "messages": history[r.Form.Get("channel")],
		})
	})
	return httptest.NewServer(mux)
}

// GitHubServer serves two repositories in GitHub's wire format: Angela's
// billing service and Eve's web app. Angela's profile exposes her email;
// Eve's does not, so she stays a github: identity until an alias joins her.
func GitHubServer() *httptest.Server {
	mux := http.NewServeMux()
	repo := func(name, desc string, topics []string) map[string]any {
		return map[string]any{
			"name": name, "full_name": "corp/" + name, "description": desc, "topics": topics,
		}
	}
	mux.HandleFunc("/repos/corp/billing-service", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, repo("billing-service", "Payment processing and dunning", []string{"billing"}))
	})
	mux.HandleFunc("/repos/corp/webapp", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, repo("webapp", "Customer facing web application", []string{"frontend"}))
	})
	mux.HandleFunc("/repos/corp/billing-service/contributors", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{{"login": "amalone", "contributions": 40}})
	})
	mux.HandleFunc("/repos/corp/webapp/contributors", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{
			{"login": "eve-dev", "contributions": 30},
			{"login": "buildbot[bot]", "contributions": 900},
		})
	})
	mux.HandleFunc("/repos/corp/billing-service/pulls", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{{
			"title": "Fix billing retry backoff", "user": map[string]any{"login": "amalone"},
			"labels": []map[string]any{{"name": "billing"}}, "updated_at": isoDaysAgo(2),
		}})
	})
	mux.HandleFunc("/repos/corp/webapp/pulls", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{{
			"title": "Rewrite the react frontend in typescript",
			"user":  map[string]any{"login": "eve-dev"}, "updated_at": isoDaysAgo(4),
		}})
	})
	for _, r := range []string{"billing-service", "webapp"} {
		mux.HandleFunc("/repos/corp/"+r+"/issues", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, []map[string]any{})
		})
	}
	mux.HandleFunc("/users/amalone", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"login": "amalone", "name": "Angela Malone", "email": "angela@corp.com"})
	})
	mux.HandleFunc("/users/eve-dev", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"login": "eve-dev", "name": "Eve Ng"})
	})
	mux.HandleFunc("/users/buildbot[bot]", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"login": "buildbot[bot]"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	return httptest.NewServer(mux)
}

// JiraServer serves issue search in Jira Cloud's wire format.
func JiraServer() *httptest.Server {
	issue := func(key, summary string, assignee, reporter map[string]any, labels []string, project string, ago int) map[string]any {
		projectKey, _, _ := strings.Cut(key, "-")
		fields := map[string]any{
			"summary": summary, "labels": labels,
			"project": map[string]any{"key": projectKey, "name": project},
			"updated": jiraDaysAgo(ago),
		}
		if assignee != nil {
			fields["assignee"] = assignee
		}
		if reporter != nil {
			fields["reporter"] = reporter
		}
		return map[string]any{"key": key, "fields": fields}
	}
	bob := map[string]any{"accountId": "j-bob", "displayName": "Bob Smith", "emailAddress": "bob@corp.com"}
	dan := map[string]any{"accountId": "j-dan", "displayName": "Dan Park", "emailAddress": "dan@corp.com"}
	frank := map[string]any{"accountId": "j-frank", "displayName": "Frank Ito", "emailAddress": "frank@corp.com"}
	issues := []map[string]any{
		issue("DAT-1", "Kafka consumer lag on the stream ingest", bob, nil,
			[]string{"kafka"}, "Data Platform", 3),
		issue("SEC-1", "Enforce mfa on the sso login flow", dan, nil,
			[]string{"sso"}, "Security", 5),
		issue("DAT-2", "Embedding model serving latency", nil, frank,
			[]string{"embeddings"}, "Data Platform", 8),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"total": len(issues), "startAt": 0, "issues": issues})
	})
	return httptest.NewServer(mux)
}

// ConfluenceServer serves page search in Confluence Cloud's wire format.
func ConfluenceServer() *httptest.Server {
	page := func(title, space string, by map[string]any, labels []string, ago int) map[string]any {
		labelList := make([]map[string]any, 0, len(labels))
		for _, l := range labels {
			labelList = append(labelList, map[string]any{"name": l})
		}
		return map[string]any{
			"title":    title,
			"space":    map[string]any{"key": "ENG", "name": space},
			"metadata": map[string]any{"labels": map[string]any{"results": labelList}},
			"history":  map[string]any{"createdBy": by},
			"version":  map[string]any{"by": by, "when": isoDaysAgo(ago)},
		}
	}
	dan := map[string]any{"accountId": "c-dan", "displayName": "Dan Park", "email": "dan@corp.com"}
	frank := map[string]any{"accountId": "c-frank", "displayName": "Frank Ito", "email": "frank@corp.com"}
	pages := []map[string]any{
		page("SSO login runbook", "Security", dan, []string{"sso", "oauth"}, 6),
		page("Embeddings model serving guide", "ML Platform", frank, []string{"embeddings"}, 9),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/content/search", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"size": len(pages), "limit": 100, "results": pages})
	})
	return httptest.NewServer(mux)
}

// PagerDutyServer serves services and on-calls in PagerDuty's wire format.
func PagerDutyServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/services", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"more": false, "services": []map[string]any{
			{"id": "S1", "name": "Billing API", "description": "Payment processing",
				"escalation_policy": map[string]any{"id": "EP1"}},
			{"id": "S2", "name": "Platform Kubernetes", "description": "Cluster and deploys",
				"escalation_policy": map[string]any{"id": "EP2"}},
		}})
	})
	mux.HandleFunc("/oncalls", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"more": false, "oncalls": []map[string]any{
			{"user": map[string]any{"id": "P1", "name": "Angela Malone", "email": "angela@corp.com"},
				"escalation_policy": map[string]any{"id": "EP1"}},
			{"user": map[string]any{"id": "P2", "name": "Grace Kim", "email": "grace@corp.com"},
				"escalation_policy": map[string]any{"id": "EP2"}},
		}})
	})
	return httptest.NewServer(mux)
}

// BuildGitRepo creates a repository under dir with the simulated history:
// Victor's heavy terraform work two years ago, Carol's recent terraform work,
// Heidi's recent search work, and a bot commit that must be skipped.
func BuildGitRepo(dir string) error {
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return fmt.Errorf("simorg: init: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("simorg: worktree: %w", err)
	}
	commit := func(rel, content, name, email string, when time.Time) error {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			return err
		}
		if _, err := wt.Add(rel); err != nil {
			return err
		}
		sig := &object.Signature{Name: name, Email: email, When: when}
		_, err := wt.Commit("touch "+rel, &git.CommitOptions{Author: sig, Committer: sig})
		return err
	}

	now := time.Now()
	steps := []struct {
		rel, content, name, email string
		when                      time.Time
	}{
		{"infra/vpc.tf", "v1", "Victor Old", "victor@corp.com", now.AddDate(-2, 0, 0)},
		{"infra/cluster.tf", "v1", "Victor Old", "victor@corp.com", now.AddDate(-2, 0, 3)},
		{"infra/dns.tf", "v1", "Victor Old", "victor@corp.com", now.AddDate(-2, 0, 6)},
		{"infra/iam.tf", "v1", "Victor Old", "victor@corp.com", now.AddDate(-2, 0, 9)},
		{"infra/vpc.tf", "v2", "Victor Old", "victor@corp.com", now.AddDate(-2, 0, 12)},
		{"infra/cluster.tf", "v2", "Carol Lee", "carol@corp.com", now.AddDate(0, 0, -14)},
		{"infra/nodepool.tf", "v1", "Carol Lee", "carol@corp.com", now.AddDate(0, 0, -3)},
		{"internal/search/rank.go", "v1", "Heidi Cho", "heidi@corp.com", now.AddDate(0, 0, -7)},
		{"go.sum", "v1", "dependabot[bot]", "1+dependabot[bot]@users.noreply.github.com",
			now.AddDate(0, 0, -1)},
	}
	for _, s := range steps {
		if err := commit(s.rel, s.content, s.name, s.email, s.when); err != nil {
			return fmt.Errorf("simorg: commit %s: %w", s.rel, err)
		}
	}
	return nil
}

// BuildIndex assembles the simulated company into a merged, canonicalized
// index under dir: it writes the org chart, CODEOWNERS, and alias fixtures,
// creates the git repository, serves each tool's wire format from in-process
// HTTP servers, and ingests all eight sources through the real connectors.
func BuildIndex(dir string) (*index.Index, error) {
	ctx := context.Background()
	write := func(name, content string) (string, error) {
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, []byte(content), 0o600)
	}
	csvPath, err := write("org.csv", OrgCSV())
	if err != nil {
		return nil, fmt.Errorf("simorg: %w", err)
	}
	ownersPath, err := write("CODEOWNERS", CodeOwners())
	if err != nil {
		return nil, fmt.Errorf("simorg: %w", err)
	}
	aliasPath, err := write("aliases.json", Aliases())
	if err != nil {
		return nil, fmt.Errorf("simorg: %w", err)
	}
	repoDir := filepath.Join(dir, "repo")
	if err := BuildGitRepo(repoDir); err != nil {
		return nil, err
	}

	slackSrv := SlackServer()
	defer slackSrv.Close()
	githubSrv := GitHubServer()
	defer githubSrv.Close()
	jiraSrv := JiraServer()
	defer jiraSrv.Close()
	confluenceSrv := ConfluenceServer()
	defer confluenceSrv.Close()
	pagerdutySrv := PagerDutyServer()
	defer pagerdutySrv.Close()

	sources := []struct {
		Name   string
		Source connector.Source
	}{
		{"org-csv", connector.NewOrgCSV(csvPath)},
		{"codeowners", connector.NewCodeOwners(ownersPath)},
		{"slack", connector.NewSlackWithClient(
			slack.New("xoxb-demo", slack.WithBaseURL(slackSrv.URL)), connector.SlackOptions{})},
		{"github", connector.NewGitHubWithClient(
			github.New("ghp-demo", github.WithBaseURL(githubSrv.URL)),
			connector.GitHubOptions{
				Repos: []string{"corp/billing-service", "corp/webapp"}, ResolveEmails: true,
			})},
		{"jira", connector.NewJiraWithClient(
			jira.New(jiraSrv.URL, "demo@corp.com", "token"), connector.JiraOptions{})},
		{"confluence", connector.NewConfluenceWithClient(
			confluence.New(confluenceSrv.URL, "demo@corp.com", "token"),
			connector.ConfluenceOptions{})},
		{"pagerduty", connector.NewPagerDutyWithClient(
			pagerduty.New("token", pagerduty.WithBaseURL(pagerdutySrv.URL)),
			connector.PagerDutyOptions{})},
		{"git", connector.NewGitHistory(connector.GitOptions{
			Paths: []string{repoDir}, SinceDays: 900,
		})},
	}

	ix := index.New()
	if err := ix.LoadAliases(aliasPath); err != nil {
		return nil, err
	}
	for _, s := range sources {
		recs, err := s.Source.Fetch(ctx)
		if err != nil {
			return nil, fmt.Errorf("simorg: %s: %w", s.Name, err)
		}
		if len(recs) == 0 {
			return nil, fmt.Errorf("simorg: %s returned no records", s.Name)
		}
		ix.Add(recs)
	}
	ix.AutoJoin()
	ix.Canonicalize()
	return ix, nil
}

// slackUser builds one users.list member.
func slackUser(id, name, email, title string) map[string]any {
	return map[string]any{"id": id, "profile": map[string]any{
		"real_name": name, "email": email, "title": title,
	}}
}

// slackChannel builds one conversations.list channel.
func slackChannel(id, name, topic string) map[string]any {
	return map[string]any{
		"id": id, "name": name,
		"topic":   map[string]any{"value": topic},
		"purpose": map[string]any{"value": topic},
	}
}

// slackMessage builds one history message with an epoch timestamp.
func slackMessage(user, text string, when time.Time) map[string]any {
	return map[string]any{
		"type": "message", "user": user, "text": text,
		"ts": fmt.Sprintf("%d.000100", when.Unix()),
	}
}

// daysAgo returns a time n days in the past.
func daysAgo(n int) time.Time { return time.Now().AddDate(0, 0, -n) }

// isoDaysAgo formats a past time in RFC 3339, the GitHub and Confluence form.
func isoDaysAgo(n int) string { return daysAgo(n).UTC().Format(time.RFC3339) }

// jiraDaysAgo formats a past time in Jira's ISO 8601 form.
func jiraDaysAgo(n int) string { return daysAgo(n).Format("2006-01-02T15:04:05.000-0700") }

// writeJSON encodes v to the response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
