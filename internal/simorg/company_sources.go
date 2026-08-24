package simorg

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// topicSlug is the path- and repo-friendly form of a subject name.
func topicSlug(s int) string { return strings.ReplaceAll(subjects[s].Topic, " ", "-") }

// topicWord is the leading word of a subject, used as a label and project key.
func topicWord(s int) string { return strings.Fields(subjects[s].Topic)[0] }

// experts returns everyone made fluent in subject s, owner first, so a source can
// spread its activity across the same people the Slack history made expert.
func (c *company) experts(s int) []person {
	out := []person{c.owners[s].who}
	for i := range c.people {
		if c.people[i].email == c.owners[s].who.email {
			continue
		}
		for _, t := range c.people[i].topics {
			if t == s {
				out = append(out, c.people[i])
				break
			}
		}
	}
	return out
}

// jsonHandler serves a fixed value as JSON.
func jsonHandler(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, v) }
}

// githubServer serves one repository per subject in GitHub's wire format, owned by
// that subject's experts, and resolves every login to an email so the GitHub
// identity joins the same person in Slack and the org chart. It returns the repo
// list the connector should read.
func (c *company) githubServer() (*httptest.Server, []string) {
	mux := http.NewServeMux()
	var repos []string
	logins := map[string]person{}

	for s := range c.owners {
		exp := c.experts(s)
		name := topicSlug(s)
		full := "corp/" + name
		repos = append(repos, full)

		mux.HandleFunc("/repos/"+full, jsonHandler(map[string]any{
			"name": name, "full_name": full,
			"description": topicSlug(s) + " service", "topics": []string{topicSlug(s)},
		}))

		contribs := make([]map[string]any, 0, len(exp))
		for j, p := range exp {
			if p.github == "" {
				continue
			}
			logins[p.github] = p
			contribs = append(contribs, map[string]any{"login": p.github, "contributions": 40 - j*8})
		}
		mux.HandleFunc("/repos/"+full+"/contributors", jsonHandler(contribs))

		pulls := make([]map[string]any, 0, 3)
		if exp[0].github != "" {
			for k := range 3 {
				num := s*10 + k
				pulls = append(pulls, map[string]any{
					"number":     num,
					"html_url":   fmt.Sprintf("https://github.com/%s/pull/%d", full, num),
					"title":      fmt.Sprintf("fix %s regression %d", topicSlug(s), k+1),
					"user":       map[string]any{"login": exp[0].github},
					"labels":     []map[string]any{{"name": topicSlug(s)}},
					"updated_at": isoDaysAgo(3 + k), "merged_at": isoDaysAgo(3 + k),
				})
			}
		}
		mux.HandleFunc("/repos/"+full+"/pulls", jsonHandler(pulls))
		mux.HandleFunc("/repos/"+full+"/issues", jsonHandler([]map[string]any{}))
	}

	for login, p := range logins {
		lp := p
		mux.HandleFunc("/users/"+login, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"login": lp.github, "name": lp.name, "email": lp.email})
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	return httptest.NewServer(mux), repos
}

// jiraServer serves resolved issues in Jira Cloud's wire format, each closed by the
// subject's owner, so tickets add expertise and recall.
func (c *company) jiraServer() *httptest.Server {
	issues := make([]map[string]any, 0, len(c.owners)*3)
	for s := range c.owners {
		exp := c.experts(s)
		word := topicWord(s)
		proj := strings.ToUpper(word)
		if len(proj) > 4 {
			proj = proj[:4]
		}
		for k := range 3 {
			// Rotate the work across everyone fluent in the subject, so a topic
			// with several experts reads as shared rather than concentrated.
			owner := exp[k%len(exp)]
			issues = append(issues, map[string]any{
				"key": fmt.Sprintf("%s-%d", proj, s*10+k),
				"fields": map[string]any{
					"summary": fmt.Sprintf("%s issue %d", topicSlug(s), k+1),
					"labels":  []string{topicSlug(s)},
					"project": map[string]any{"key": proj, "name": topicSlug(s)},
					"updated": jiraDaysAgo(5 + k),
					"assignee": map[string]any{
						"accountId": "j-" + owner.id, "displayName": owner.name, "emailAddress": owner.email,
					},
					"resolutiondate": jiraDaysAgo(5 + k),
					"status": map[string]any{
						"name": "Done", "statusCategory": map[string]any{"key": "done"},
					},
				},
			})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search/jql", jsonHandler(map[string]any{"issues": issues, "isLast": true}))
	return httptest.NewServer(mux)
}

// confluenceServer serves one runbook per subject in Confluence v2's wire format,
// authored by the subject's owner.
func (c *company) confluenceServer() *httptest.Server {
	type page struct {
		id, title, space, author string
		labels                   []string
		ago                      int
	}
	pages := make([]page, 0, len(c.owners))
	spaceName := map[string]string{}
	users := map[string]map[string]any{}
	for s := range c.owners {
		owner := c.owners[s].who
		sp := "sp-" + owner.id
		spaceName[sp] = topicSlug(s)
		aid := "c-" + owner.id
		users[aid] = map[string]any{"accountId": aid, "displayName": owner.name, "email": owner.email}
		pages = append(pages, page{
			fmt.Sprint(s), topicSlug(s) + " runbook", sp, aid, []string{topicSlug(s)}, 6 + s,
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/user/current", jsonHandler(map[string]any{"accountId": "c-me", "email": "demo@corp.com"}))
	mux.HandleFunc("/wiki/rest/api/user", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := users[r.URL.Query().Get("accountId")]; ok {
			writeJSON(w, u)
			return
		}
		writeJSON(w, map[string]any{"accountId": r.URL.Query().Get("accountId")})
	})
	mux.HandleFunc("/wiki/api/v2/spaces/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/spaces/")
		writeJSON(w, map[string]any{"id": id, "key": id, "name": spaceName[id]})
	})
	mux.HandleFunc("/wiki/api/v2/pages", func(w http.ResponseWriter, _ *http.Request) {
		results := make([]any, 0, len(pages))
		for _, p := range pages {
			results = append(results, map[string]any{
				"id": p.id, "title": p.title, "spaceId": p.space, "authorId": p.author,
				"createdAt": isoDaysAgo(p.ago),
				"version":   map[string]any{"authorId": p.author, "createdAt": isoDaysAgo(p.ago)},
			})
		}
		writeJSON(w, map[string]any{"results": results, "_links": map[string]any{}})
	})
	mux.HandleFunc("/wiki/api/v2/pages/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/pages/"), "/labels")
		labels := make([]any, 0)
		for _, p := range pages {
			if p.id == id {
				for _, l := range p.labels {
					labels = append(labels, map[string]any{"name": l})
				}
			}
		}
		writeJSON(w, map[string]any{"results": labels})
	})
	return httptest.NewServer(mux)
}

// pagerdutyServer serves services, resolved incidents, and on-calls in PagerDuty's
// wire format for the first several subjects, owned by their experts.
func (c *company) pagerdutyServer() *httptest.Server {
	var services, incidents, oncalls []map[string]any
	for s := 0; s < len(c.owners) && s < 8; s++ {
		exp := c.experts(s)
		owner := exp[0]
		sid, epid, pid := fmt.Sprintf("S%d", s), fmt.Sprintf("EP%d", s), "P"+owner.id
		svcName := topicSlug(s)
		services = append(services, map[string]any{
			"id": sid, "name": svcName, "description": topicSlug(s),
			"escalation_policy": map[string]any{"id": epid},
		})
		oncalls = append(oncalls, map[string]any{
			"user":              map[string]any{"id": pid, "name": owner.name, "email": owner.email},
			"escalation_policy": map[string]any{"id": epid},
		})
		for k := range 2 {
			// A rotation means incidents land on different responders.
			resp := exp[k%len(exp)]
			id := fmt.Sprintf("PINC%d%d", s, k)
			incidents = append(incidents, map[string]any{
				"id": id, "incident_number": s*100 + k, "status": "resolved",
				"title":      fmt.Sprintf("%s degraded", topicSlug(s)),
				"html_url":   "https://corp.pagerduty.com/incidents/" + id,
				"created_at": isoDaysAgo(20 + k), "resolved_at": isoDaysAgo(20 + k),
				"service": map[string]any{"id": sid, "summary": svcName},
				"assignments": []map[string]any{
					{"assignee": map[string]any{
						"id": "P" + resp.id, "name": resp.name, "email": resp.email}},
				},
			})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/services", jsonHandler(map[string]any{"more": false, "services": services}))
	mux.HandleFunc("/incidents", jsonHandler(map[string]any{"more": false, "incidents": incidents}))
	mux.HandleFunc("/oncalls", jsonHandler(map[string]any{"more": false, "oncalls": oncalls}))
	return httptest.NewServer(mux)
}

// buildGitRepo writes a repository under dir with a subject's owner committing to
// that subject's path, so authorship and recency line up with the rest.
func (c *company) buildGitRepo(dir string) error {
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return fmt.Errorf("simorg: git init: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("simorg: git worktree: %w", err)
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
		_, err := wt.Commit(strings.TrimSuffix(rel, "/main.tf"), &git.CommitOptions{Author: sig, Committer: sig})
		return err
	}
	now := time.Now()
	for s := range c.owners {
		exp := c.experts(s)
		// The directory carries the subject; the filename must not carry a
		// second one. A real extension maps to a technology topic (.tf means
		// terraform), so every subject sharing one filename would make the
		// whole company expert in it.
		// "main" is a generic path segment the connector already ignores, and the
		// file carries no extension, so the directory is the only thing this
		// path says: the subject. Anything else, a real extension or a novel
		// stem, becomes a topic of its own held by everyone who ever committed.
		rel := topicSlug(s) + "/main"
		for k := range 3 {
			owner := exp[k%len(exp)]
			if err := commit(rel, fmt.Sprintf("v%d", k), owner.name, owner.email,
				now.AddDate(0, 0, -(s*3+k+1))); err != nil {
				return fmt.Errorf("simorg: git commit: %w", err)
			}
		}
	}
	return nil
}
