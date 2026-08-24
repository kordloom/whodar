package connector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/jira"
)

// TestJiraFetch verifies the assignee and reporter get topics from components,
// labels, summary words, and project name, with email and account-id identity.
func TestJiraFetch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total":2,"startAt":0,"issues":[`+
			`{"key":"SEC-1","fields":{"summary":"Fix wiz scan flaky",`+
			`"assignee":{"accountId":"a1","displayName":"Jane Roe","emailAddress":"jane@x.com"},`+
			`"reporter":{"accountId":"p1","displayName":"Pat","emailAddress":"pat@x.com"},`+
			`"components":[{"name":"scanning"}],"labels":["wiz"],`+
			`"project":{"key":"SEC","name":"Security"},"updated":"2026-06-20T09:30:00.000-0500"}},`+
			`{"key":"OPS-2","fields":{"summary":"Dashboard down",`+
			`"assignee":{"accountId":"b1","displayName":"Bob"},`+
			`"labels":["dashboard"],"project":{"key":"OPS","name":"Operations"}}}]}`)
	}))
	t.Cleanup(srv.Close)

	client := jira.New(srv.URL, "me@x.com", "token")
	recs, err := NewJiraWithClient(client, JiraOptions{Projects: []string{"SEC"}}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// A record carries both the source's own id and any email, so key by the
	// email when there is one: that is the identity the index settles on.
	byKey := make(map[string]Record)
	for _, r := range recs {
		key := r.Email
		if key == "" {
			key = r.PersonID
		}
		byKey[key] = r
	}

	// Topics carries what an issue stated as a label or component; everything
	// mined from a summary or description arrives as a weak topic. Both still
	// count toward affinity, so check the union for presence and the split for
	// which side each one landed on.
	jane := byKey["jane@x.com"]
	janeAll := append(append([]string(nil), jane.Topics...), jane.WeakTopics...)
	if !slices.Contains(janeAll, "wiz") ||
		!slices.Contains(janeAll, "scan") || !slices.Contains(janeAll, "scanning") {
		t.Errorf("jane topics = %v, weak = %v, want wiz, scan, scanning",
			jane.Topics, jane.WeakTopics)
	}
	// The wiz label and the scanning component were stated by the issue, so both
	// are curated; scan is only a summary word, so it stays weak.
	if !slices.Contains(jane.Topics, "wiz") || !slices.Contains(jane.Topics, "scanning") {
		t.Errorf("jane curated topics = %v, want the wiz label and scanning component", jane.Topics)
	}
	if !slices.Contains(jane.WeakTopics, "scan") {
		t.Errorf("jane weak topics = %v, want the summary word scan", jane.WeakTopics)
	}
	bob := byKey["jira:b1"]
	bobAll := append(append([]string(nil), bob.Topics...), bob.WeakTopics...)
	if !slices.Contains(bobAll, "dashboard") {
		t.Errorf("bob topics = %v, weak = %v, want dashboard", bob.Topics, bob.WeakTopics)
	}
	zone := time.FixedZone("", -5*3600)
	if want := time.Date(2026, 6, 20, 9, 30, 0, 0, zone); !byKey["jane@x.com"].Time.Equal(want) {
		t.Errorf("jane time = %v, want the issue update time %v", byKey["jane@x.com"].Time, want)
	}
	if !byKey["jira:b1"].Time.IsZero() {
		t.Errorf("bob time = %v, want zero for an issue without a date", byKey["jira:b1"].Time)
	}
}

// TestJiraTime verifies the Jira colon-less zone form and RFC 3339 variants all
// parse, and that an unparseable string yields the zero time.
func TestJiraTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		In       string
		WantZero bool
	}{
		{In: "2026-07-05T12:34:56.789-0500"},
		{In: "2026-07-05T12:34:56Z"},
		{In: "2026-07-05T12:34:56.789Z"},
		{In: "2026-07-05T12:34:56+05:00"},
		{In: "not a time", WantZero: true},
		{In: "", WantZero: true},
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			if got := jiraTime(test.In).IsZero(); got != test.WantZero {
				t.Errorf("jiraTime(%q) zero=%v, want %v", test.In, got, test.WantZero)
			}
		})
	}
}

// TestJiraJQL verifies the query an incremental read builds: it restricts to
// issues updated since the watermark, orders them oldest first, and leaves an
// explicit JQL untouched.
func TestJiraJQL(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		Name      string
		Opts      JiraOptions
		WantHas   []string
		WantLacks []string
	}{{ // Test 0: Incremental with a project scope.
		Name: "incremental with projects", Opts: JiraOptions{Projects: []string{"SEC"}, Since: since},
		WantHas: []string{`project in ("SEC")`, `updated >= "2026/03/04 09:28"`, "ORDER BY updated ASC"},
	}, { // Test 1: Incremental with no scope.
		Name: "incremental no projects", Opts: JiraOptions{Since: since},
		WantHas: []string{`updated >= "2026/03/04 09:28"`, "ORDER BY updated ASC"}, WantLacks: []string{"project in"},
	}, { // Test 2: A full read keeps newest-first and adds no since clause.
		Name: "full with projects", Opts: JiraOptions{Projects: []string{"SEC"}},
		WantHas: []string{`project in ("SEC")`, "ORDER BY updated DESC"}, WantLacks: []string{"updated >="},
	}, { // Test 3: An explicit JQL is authoritative and ignores Since.
		Name: "explicit jql", Opts: JiraOptions{JQL: "assignee = currentUser() ORDER BY created DESC", Since: since},
		WantHas: []string{"assignee = currentUser() ORDER BY created DESC"}, WantLacks: []string{"updated >=", "ASC"},
	}}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			j := &Jira{opts: test.Opts.withDefaults()}
			got := j.jql()
			for _, s := range test.WantHas {
				if !strings.Contains(got, s) {
					t.Errorf("jql = %q, want it to contain %q", got, s)
				}
			}
			for _, s := range test.WantLacks {
				if strings.Contains(got, s) {
					t.Errorf("jql = %q, want it to NOT contain %q", got, s)
				}
			}
		})
	}
}
