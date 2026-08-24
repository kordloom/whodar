package connector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/confluence"
)

// TestConfluenceFetch verifies authors get topics from labels, title words, and
// space name, with email and account-id identity, reading through the Cloud v2
// endpoints that serve the space, labels, and identities apart from the page.
func TestConfluenceFetch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/api/v2/pages", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"results":[`+
			`{"id":"1","title":"Wiz scanning runbook","spaceId":"100","authorId":"a1",`+
			`"createdAt":"2026-06-24T09:00:00.000Z",`+
			`"version":{"authorId":"a1","createdAt":"2026-06-25T14:00:00.000Z"}},`+
			`{"id":"2","title":"Dashboard outage","spaceId":"200","authorId":"b1",`+
			`"createdAt":"2026-06-10T09:00:00.000Z",`+
			`"version":{"authorId":"b1","createdAt":"2026-06-10T09:00:00.000Z"}}`+
			`],"_links":{}}`)
	})
	mux.HandleFunc("/wiki/api/v2/spaces/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/100") {
			io.WriteString(w, `{"id":"100","key":"SEC","name":"Security"}`)
			return
		}
		io.WriteString(w, `{"id":"200","key":"OPS","name":"Operations"}`)
	})
	mux.HandleFunc("/wiki/api/v2/pages/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/wiki/api/v2/pages/1/") {
			io.WriteString(w, `{"results":[{"name":"wiz"}]}`)
			return
		}
		io.WriteString(w, `{"results":[{"name":"dashboard"}]}`)
	})
	mux.HandleFunc("/wiki/rest/api/user", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("accountId") == "a1" {
			io.WriteString(w, `{"accountId":"a1","displayName":"Jane","email":"jane@x.com"}`)
			return
		}
		io.WriteString(w, `{"accountId":"b1","displayName":"Bob"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := confluence.New(srv.URL, "me@x.com", "token")
	recs, err := NewConfluenceWithClient(client, ConfluenceOptions{}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	byKey := make(map[string]Record)
	for _, r := range recs {
		key := r.PersonID
		if key == "" {
			key = r.Email
		}
		byKey[key] = r
	}

	// A page's labels are curated topics; its title, space, and body words are
	// weak. Both carry affinity, so check the union for presence.
	all := func(r Record) []string {
		return append(append([]string(nil), r.Topics...), r.WeakTopics...)
	}
	jane := byKey["jane@x.com"]
	if !slices.Contains(all(jane), "wiz") || !slices.Contains(all(jane), "scanning") {
		t.Errorf("jane topics = %v, weak = %v, want wiz, scanning", jane.Topics, jane.WeakTopics)
	}
	if !slices.Contains(jane.Topics, "wiz") {
		t.Errorf("jane curated topics = %v, want the wiz label", jane.Topics)
	}
	if !slices.Contains(jane.WeakTopics, "scanning") {
		t.Errorf("jane weak topics = %v, want the title word scanning", jane.WeakTopics)
	}
	if bob := byKey["confluence:b1"]; !slices.Contains(all(bob), "dashboard") {
		t.Errorf("bob topics = %v, weak = %v, want dashboard", bob.Topics, bob.WeakTopics)
	}
	if want := time.Date(2026, 6, 25, 14, 0, 0, 0, time.UTC); !byKey["jane@x.com"].Time.Equal(want) {
		t.Errorf("jane time = %v, want the page edit time %v", byKey["jane@x.com"].Time, want)
	}
}
