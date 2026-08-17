package connector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/confluence"
)

// TestConfluenceFetch verifies authors get topics from labels, title words, and
// space name, with email and account-id identity.
func TestConfluenceFetch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"size":2,"limit":100,"results":[`+
			`{"title":"Wiz scanning runbook","space":{"key":"SEC","name":"Security"},`+
			`"metadata":{"labels":{"results":[{"name":"wiz"}]}},`+
			`"history":{"createdBy":{"accountId":"a1","displayName":"Jane","email":"jane@x.com"}},`+
			`"version":{"by":{"accountId":"a1","displayName":"Jane","email":"jane@x.com"},`+
			`"when":"2026-06-25T14:00:00.000Z"}},`+
			`{"title":"Dashboard outage","space":{"key":"OPS","name":"Operations"},`+
			`"metadata":{"labels":{"results":[{"name":"dashboard"}]}},`+
			`"history":{"createdBy":{"accountId":"b1","displayName":"Bob"}},`+
			`"version":{"by":{"accountId":"b1","displayName":"Bob"}}}]}`)
	}))
	t.Cleanup(srv.Close)

	client := confluence.New(srv.URL, "me@x.com", "token")
	recs, err := NewConfluenceWithClient(client, ConfluenceOptions{Spaces: []string{"SEC"}}).Fetch(context.Background())
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

	if jane := byKey["jane@x.com"]; !slices.Contains(jane.Topics, "wiz") ||
		!slices.Contains(jane.Topics, "scanning") {
		t.Errorf("jane topics = %v, want wiz, scanning", jane.Topics)
	}
	if bob := byKey["confluence:b1"]; !slices.Contains(bob.Topics, "dashboard") {
		t.Errorf("bob topics = %v, want dashboard", bob.Topics)
	}
	if want := time.Date(2026, 6, 25, 14, 0, 0, 0, time.UTC); !byKey["jane@x.com"].Time.Equal(want) {
		t.Errorf("jane time = %v, want the page edit time %v", byKey["jane@x.com"].Time, want)
	}
}
