package connector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/kordloom/whodar/internal/pagerduty"
)

// TestPagerDutyFetch verifies on-call users get the topics of the services they
// answer for, with email and user-id identity.
func TestPagerDutyFetch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/services", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"more":false,"services":[`+
			`{"id":"S1","name":"Billing API","description":"Handles billing and payments",`+
			`"escalation_policy":{"id":"EP1"}},`+
			`{"id":"S2","name":"Infra","description":"","escalation_policy":{"id":"EP2"}}]}`)
	})
	mux.HandleFunc("/oncalls", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"more":false,"oncalls":[`+
			`{"user":{"id":"U1","name":"Jane Roe","email":"jane@x.com"},"escalation_policy":{"id":"EP1"}},`+
			`{"user":{"id":"U2","name":"Bob"},"escalation_policy":{"id":"EP2"}}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := pagerduty.New("token", pagerduty.WithBaseURL(srv.URL))
	recs, err := NewPagerDutyWithClient(client, PagerDutyOptions{}).Fetch(context.Background())
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

	// PagerDuty has no label field, so every topic it produces is mined from a
	// service name or description and arrives as a weak topic.
	if jane := byKey["jane@x.com"]; !slices.Contains(jane.WeakTopics, "billing") ||
		!slices.Contains(jane.WeakTopics, "api") {
		t.Errorf("jane weak topics = %v, want billing and api", jane.WeakTopics)
	}
	if bob := byKey["pagerduty:U2"]; !slices.Contains(bob.WeakTopics, "infra") {
		t.Errorf("bob weak topics = %v, want infra", bob.WeakTopics)
	}
}
