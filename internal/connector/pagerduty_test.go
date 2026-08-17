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

	byKey := make(map[string]Record)
	for _, r := range recs {
		key := r.PersonID
		if key == "" {
			key = r.Email
		}
		byKey[key] = r
	}

	if jane := byKey["jane@x.com"]; !slices.Contains(jane.Topics, "billing") ||
		!slices.Contains(jane.Topics, "api") {
		t.Errorf("jane topics = %v, want billing and api", jane.Topics)
	}
	if bob := byKey["pagerduty:U2"]; !slices.Contains(bob.Topics, "infra") {
		t.Errorf("bob topics = %v, want infra", bob.Topics)
	}
}
