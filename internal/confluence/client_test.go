package confluence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestPages verifies page fields, labels, and authors decode.
func TestPages(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"size":1,"limit":100,"results":[{`+
			`"title":"Wiz scanning runbook",`+
			`"space":{"key":"SEC","name":"Security"},`+
			`"metadata":{"labels":{"results":[{"name":"wiz"}]}},`+
			`"history":{"createdBy":{"accountId":"a1","displayName":"Jane","email":"jane@x.com"}},`+
			`"version":{"by":{"accountId":"a2","displayName":"Bob","email":"bob@x.com"}}}]}`)
	}))
	t.Cleanup(srv.Close)

	pages, err := New(srv.URL, "me@x.com", "token").Pages(context.Background(), "type = page", 0)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	p := pages[0]
	if p.Title != "Wiz scanning runbook" || p.Space.Key != "SEC" {
		t.Errorf("page = %+v", p)
	}
	if !slices.Contains(p.LabelNames(), "wiz") {
		t.Errorf("labels = %v, want wiz", p.LabelNames())
	}
	authors := p.Authors()
	if len(authors) != 2 || authors[0].Email != "jane@x.com" || authors[1].Email != "bob@x.com" {
		t.Errorf("authors = %+v, want jane and bob", authors)
	}
}

// TestNewEmptyPanics verifies the constructor guards empty arguments.
func TestNewEmptyPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("New with empty args did not panic")
		}
	}()
	New("", "e", "t")
}

// TestUserIdentity verifies the identity fallback: Cloud keys on the account
// id, Server and Data Center on the username or user key.
func TestUserIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		In   User
		Want string
	}{
		{User{AccountID: "a1", Username: "jdoe", UserKey: "k1"}, "a1"}, // Test 0: Cloud.
		{User{Username: "jdoe", UserKey: "k1"}, "jdoe"},                // Test 1: Server username.
		{User{UserKey: "k1"}, "k1"},                                    // Test 2: Server key.
		{User{}, ""},                                                   // Test 3: none.
	}
	for testNum, test := range tests {
		if got := test.In.Identity(); got != test.Want {
			t.Errorf("test %d: Identity() = %q, want %q", testNum, got, test.Want)
		}
	}
}

// TestNewServerAnonymousAtRoot verifies the Server client serves the REST API
// at the site root without the Cloud /wiki prefix, sends no auth header when
// the token is empty, and reads Server-shaped users. This is the shape a public
// wiki such as Apache's Confluence returns.
func TestNewServerAnonymousAtRoot(t *testing.T) {
	t.Parallel()
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"size":1,"limit":100,"results":[{"title":"KRaft design",`+
			`"space":{"key":"KAFKA","name":"Kafka"},`+
			`"history":{"createdBy":{"username":"showuon","displayName":"Luke Chen"}},`+
			`"version":{"by":{"username":"showuon","displayName":"Luke Chen"}}}]}`)
	}))
	t.Cleanup(srv.Close)

	c := NewServer(srv.URL, "")
	pages, err := c.Pages(context.Background(), "type=page", 10)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if strings.HasPrefix(gotPath, "/wiki/") {
		t.Errorf("Server hit the Cloud /wiki path %q", gotPath)
	}
	if !strings.HasPrefix(gotPath, apiBaseServer) {
		t.Errorf("path = %q, want the Server API base %q", gotPath, apiBaseServer)
	}
	if gotAuth != "" {
		t.Errorf("anonymous client sent an Authorization header: %q", gotAuth)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	if got := pages[0].History.CreatedBy.Identity(); got != "showuon" {
		t.Errorf("creator identity = %q, want the username showuon", got)
	}
}

// TestNewServerBearerToken verifies a Confluence PAT is sent as a bearer.
func TestNewServerBearerToken(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"size":0,"limit":100,"results":[]}`)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewServer(srv.URL, "PAT9").Pages(context.Background(), "", 10); err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if gotAuth != "Bearer PAT9" {
		t.Errorf("Authorization = %q, want Bearer PAT9", gotAuth)
	}
}
