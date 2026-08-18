package connector

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/github"
)

// TestGitHubFetch verifies topics come from repo metadata, PR labels and titles,
// reviewers and assignees, non-PR issues, and CODEOWNERS, and that pull requests
// returned by the issues endpoint are skipped.
func TestGitHubFetch(t *testing.T) {
	t.Parallel()
	owners := base64.StdEncoding.EncodeToString([]byte("/internal/ @kim"))
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"name":"billing-service","full_name":"o/r",`+
			`"description":"Wiz scanning integration","topics":["billing"]}`)
	})
	mux.HandleFunc("/repos/o/r/contributors", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"login":"jane","contributions":10}]`)
	})
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"title":"Fix wiz scan flakiness","user":{"login":"jane"},`+
			`"labels":[{"name":"retries"}],"requested_reviewers":[{"login":"bob"}],`+
			`"assignees":[{"login":"carol"}],"updated_at":"2026-07-01T10:00:00Z"}]`)
	})
	mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"user":{"login":"dan"},"labels":[{"name":"dashboard"}],`+
			`"title":"Wiz dashboard broken","updated_at":"2026-06-15T08:00:00Z"},`+
			`{"user":{"login":"ghost"},"labels":[{"name":"shouldskip"}],"title":"x",`+
			`"pull_request":{"url":"y"}}]`)
	})
	mux.HandleFunc("/repos/o/r/contents/CODEOWNERS", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"encoding":"base64","content":"`+owners+`"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := github.New("ghp-test", github.WithBaseURL(srv.URL))
	recs, err := NewGitHubWithClient(client, GitHubOptions{Repos: []string{"o/r"}}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	byID := make(map[string]Record)
	for _, r := range recs {
		byID[r.PersonID] = r
	}

	if jane := byID["github:jane"]; !slices.Contains(jane.Topics, "wiz") ||
		!slices.Contains(jane.Topics, "scan") || !slices.Contains(jane.Topics, "retries") {
		t.Errorf("jane topics = %v, want wiz, scan, retries", jane.Topics)
	}
	if want := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC); !byID["github:jane"].Time.Equal(want) {
		t.Errorf("jane time = %v, want the PR update time %v", byID["github:jane"].Time, want)
	}
	if want := time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC); !byID["github:dan"].Time.Equal(want) {
		t.Errorf("dan time = %v, want the issue update time %v", byID["github:dan"].Time, want)
	}
	if bob := byID["github:bob"]; !slices.Contains(bob.Topics, "wiz") {
		t.Errorf("reviewer bob topics = %v, want wiz", bob.Topics)
	}
	if carol := byID["github:carol"]; !slices.Contains(carol.Topics, "wiz") {
		t.Errorf("assignee carol topics = %v, want wiz", carol.Topics)
	}
	if dan := byID["github:dan"]; !slices.Contains(dan.Topics, "dashboard") ||
		!slices.Contains(dan.Topics, "wiz") {
		t.Errorf("issue author dan topics = %v, want dashboard, wiz", dan.Topics)
	}
	if _, ok := byID["github:ghost"]; ok {
		t.Error("pull request returned by the issues endpoint should be skipped")
	}
	// A repo's own CODEOWNERS @login remaps into the github namespace, so it
	// merges with that login's pull request and issue activity.
	if kim := byID["github:kim"]; kim.Name != "@kim" {
		t.Errorf("codeowners record = %+v, want @kim under github:kim", kim)
	}
	if _, ok := byID["codeowners:kim"]; ok {
		t.Error("a repo's own CODEOWNERS @login should not stay in the codeowners namespace")
	}
}

// TestGitHubFetchSkipsBadRepo verifies one failing repository is logged and
// skipped rather than discarding every other repository's data.
func TestGitHubFetchSkipsBadRepo(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	// The good repo answers every endpoint.
	mux.HandleFunc("/repos/o/good", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"name":"good","full_name":"o/good","topics":["billing"]}`)
	})
	mux.HandleFunc("/repos/o/good/contributors", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"login":"jane","contributions":5}]`)
	})
	mux.HandleFunc("/repos/o/good/pulls", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[]`)
	})
	mux.HandleFunc("/repos/o/good/issues", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[]`)
	})
	// The bad repo fails on its metadata endpoint.
	mux.HandleFunc("/repos/o/bad", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var logBuf strings.Builder
	client := github.New("ghp-test", github.WithBaseURL(srv.URL))
	recs, err := NewGitHubWithClient(client, GitHubOptions{
		Repos: []string{"o/bad", "o/good"}, Log: &logBuf,
	}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: one bad repo should not fail the run: %v", err)
	}

	var foundJane bool
	for _, r := range recs {
		if r.PersonID == "github:jane" {
			foundJane = true
		}
	}
	if !foundJane {
		t.Error("good repo's contributor was dropped when another repo failed")
	}
	if !strings.Contains(logBuf.String(), "skipping o/bad") {
		t.Errorf("expected a skip warning for o/bad, log = %q", logBuf.String())
	}
}

// TestGitHubResolvesEmailsConcurrently verifies email resolution fetches many
// contributor profiles concurrently and correctly, without dropping or racing
// on any of them. Against a real active org this is hundreds of profile
// requests, and doing them serially once made an org index time out.
func TestGitHubResolvesEmailsConcurrently(t *testing.T) {
	t.Parallel()
	const contributors = 200
	var accountHits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/billing", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"name":"billing","full_name":"acme/billing"}`)
	})
	mux.HandleFunc("/repos/acme/billing/contributors", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("["))
		for i := range contributors {
			if i > 0 {
				w.Write([]byte(","))
			}
			fmt.Fprintf(w, `{"login":"dev%d","contributions":%d}`, i, i+1)
		}
		w.Write([]byte("]"))
	})
	mux.HandleFunc("/repos/acme/billing/pulls", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "[]") })
	mux.HandleFunc("/repos/acme/billing/issues", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "[]") })
	// One profile endpoint per contributor, each returning that dev's email.
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&accountHits, 1)
		login := strings.TrimPrefix(r.URL.Path, "/users/")
		fmt.Fprintf(w, `{"login":%q,"email":%q}`, login, login+"@x.com")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := github.New("token", github.WithBaseURL(srv.URL))
	recs, err := NewGitHubWithClient(client,
		GitHubOptions{Repos: []string{"acme/billing"}, ResolveEmails: true}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := atomic.LoadInt64(&accountHits); got != contributors {
		t.Errorf("resolved %d profiles, want one per contributor (%d)", got, contributors)
	}
	withEmail := 0
	for _, r := range recs {
		if strings.HasSuffix(r.Email, "@x.com") {
			withEmail++
		}
	}
	if withEmail != contributors {
		t.Errorf("%d records got an email, want all %d resolved", withEmail, contributors)
	}
}
