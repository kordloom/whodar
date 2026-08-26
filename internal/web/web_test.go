package web

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kordloom/whodar/internal/report"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/llm"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/recall"
	"github.com/kordloom/whodar/internal/resolve"
)

// testHandler builds a handler whose Ask returns one canned person.
func testHandler(t *testing.T) http.Handler {
	t.Helper()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{
			Summary: "talk to jane",
			People: []model.Match{{
				Person:  &model.Person{Name: "Jane Roe", Email: "jane@x.com", Title: "Engineer"},
				Score:   1,
				Reasons: []string{"retries (topic)"},
			}},
		}, nil
	}
	h, err := Handler(Config{Ask: ask, Version: "test"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return h
}

// TestIndexPage verifies the root serves HTML with the version.
func TestIndexPage(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "whodar") || !strings.Contains(body, "test") {
		t.Errorf("index page missing whodar or version:\n%s", body)
	}
}

// TestAskAPI verifies the ask endpoint returns the answer as JSON.
func TestAskAPI(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ask?q=retries", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var ans resolve.JSONAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ans.Summary != "talk to jane" {
		t.Errorf("summary = %q", ans.Summary)
	}
	if len(ans.People) != 1 || ans.People[0].Email != "jane@x.com" {
		t.Errorf("people = %+v, want jane@x.com", ans.People)
	}
}

// TestAskMissingQuery verifies a missing q is a 400.
func TestAskMissingQuery(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ask", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestNilAskPanics verifies the handler guards a nil Ask function.
func TestNilAskPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("Handler with nil Ask did not panic")
		}
	}()
	_, _ = Handler(Config{})
}

// TestFeedbackAPI verifies the feedback endpoint records votes and rejects bad
// requests.
func TestFeedbackAPI(t *testing.T) {
	t.Parallel()
	var got feedback.Entry
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	h, err := Handler(Config{
		Ask:      ask,
		Feedback: func(e feedback.Entry) error { got = e; return nil },
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	body := `{"query":"billing retries","person":"jane@x.com","vote":"helpful","comment":"she owns it"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got.Query != "billing retries" || got.Person != "jane@x.com" ||
		got.Vote != feedback.Helpful || got.Comment != "she owns it" {
		t.Errorf("recorded entry = %+v", got)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/feedback", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/feedback",
		strings.NewReader(`{"query":"","vote":"helpful"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid entry status = %d, want 400", rec.Code)
	}
}

// TestUnauthorizedGetsSecurityHeaders verifies the 401 for a missing token
// still carries the hardening headers and a JSON content type, since
// securityHeaders wraps outermost.
func TestUnauthorizedGetsSecurityHeaders(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	h, err := Handler(Config{Ask: ask, Version: "test", AuthToken: "secret"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ask?q=x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff on a 401", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY on a 401", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("Content-Security-Policy = %q, want default-src 'self' on a 401", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json on a 401", got)
	}
}

// TestSameOrigin verifies the CSRF origin check matches host and web scheme and
// rejects opaque or foreign origins.
func TestSameOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Origin     string
		Host       string
		WantResult bool
	}{
		{Origin: "http://whodar.local", Host: "whodar.local", WantResult: true},
		{Origin: "https://whodar.local:8765", Host: "whodar.local:8765", WantResult: true},
		{Origin: "http://evil.example", Host: "whodar.local", WantResult: false},
		{Origin: "null", Host: "whodar.local", WantResult: false},
		{Origin: "file://whodar.local", Host: "whodar.local", WantResult: false},
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			if got := sameOrigin(test.Origin, test.Host); got != test.WantResult {
				t.Errorf("sameOrigin(%q, %q) = %v, want %v",
					test.Origin, test.Host, got, test.WantResult)
			}
		})
	}
}

// TestFeedbackTooLarge verifies an oversized feedback body is rejected with 413
// rather than being read into memory.
func TestFeedbackTooLarge(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	h, err := Handler(Config{
		Ask:      ask,
		Feedback: func(feedback.Entry) error { return nil },
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	big := `{"query":"q","person":"jane@x.com","vote":"helpful","comment":"` +
		strings.Repeat("a", 128<<10) + `"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(big)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for an oversized body", rec.Code)
	}
}

// TestFeedbackAPIDisabled verifies the endpoint is absent without a callback.
func TestFeedbackAPIDisabled(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/feedback", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when feedback is disabled", rec.Code)
	}
}

// TestPersonAPI verifies the person endpoint returns a profile and a 404 for
// unknown identifiers.
func TestPersonAPI(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	person := func(id string) (resolve.JSONProfile, bool) {
		if id != "jane@x.com" {
			return resolve.JSONProfile{}, false
		}
		return resolve.JSONProfile{ID: id, Name: "Jane Roe", Channels: []string{"payments"}}, true
	}
	h, err := Handler(Config{Ask: ask, Person: person, Version: "test"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/person?id=jane%40x.com", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got resolve.JSONProfile
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Jane Roe" || len(got.Channels) != 1 {
		t.Errorf("profile = %+v", got)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/person?id=ghost", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown person status = %d, want 404", rec.Code)
	}
}

// TestDirectoryAPI verifies the directory endpoint serves the inventory and
// is absent when not configured.
func TestDirectoryAPI(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	dir := resolve.Directory{People: []resolve.DirectoryPerson{{ID: "jane@x.com", Name: "Jane Roe"}}}
	h, err := Handler(Config{Ask: ask, Version: "test", Directory: &dir})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/directory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got resolve.Directory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.People) != 1 || got.People[0].Name != "Jane Roe" {
		t.Errorf("directory = %+v", got)
	}

	rec = httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/directory", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unconfigured status = %d, want 404", rec.Code)
	}
}

// TestModesAPI verifies the modes endpoint reports mode and provider
// readiness and is absent when not configured.
func TestModesAPI(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	modes := func(context.Context) ModesReport {
		return ModesReport{
			Modes: map[string]ModeInfo{"keyword": {Ready: true}},
			Providers: map[string]ModeInfo{
				"ollama": {Ready: false, Hint: "Ollama is not running on this machine."},
			},
			Provider: "ollama",
		}
	}
	h, err := Handler(Config{Ask: ask, Version: "test", Modes: modes})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/modes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got ModesReport
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Modes["keyword"].Ready || got.Providers["ollama"].Ready || got.Provider != "ollama" {
		t.Errorf("report = %+v", got)
	}

	rec = httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/modes", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unconfigured status = %d, want 404", rec.Code)
	}
}

// TestAskProviderParam verifies the provider query parameter reaches the ask
// function.
func TestAskProviderParam(t *testing.T) {
	t.Parallel()
	var gotProvider string
	ask := func(_ context.Context, _, _, provider string, _ int) (resolve.Answer, error) {
		gotProvider = provider
		return resolve.Answer{}, nil
	}
	h, err := Handler(Config{Ask: ask, Version: "test"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ask?q=x&mode=llm&provider=anthropic", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gotProvider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", gotProvider)
	}
}

// TestAuthToken verifies the token gate: header, query parameter, and cookie
// all admit; anything else is a 401; a query token sets the session cookie.
func TestAuthToken(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	h, err := Handler(Config{Ask: ask, Version: "test", AuthToken: "sekret"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	tests := []struct {
		Target     string
		Bearer     string
		Cookie     string
		WantCode   int
		WantCookie bool
	}{{ // Test 0: No credential is a 401.
		Target: "/api/ask?q=x", WantCode: http.StatusUnauthorized,
	}, { // Test 1: A wrong bearer token is a 401.
		Target: "/api/ask?q=x", Bearer: "nope", WantCode: http.StatusUnauthorized,
	}, { // Test 2: The right bearer token admits.
		Target: "/api/ask?q=x", Bearer: "sekret", WantCode: http.StatusOK,
	}, { // Test 3: The right query token admits and sets the session cookie.
		Target: "/?token=sekret", WantCode: http.StatusOK, WantCookie: true,
	}, { // Test 4: A wrong query token is a 401.
		Target: "/?token=nope", WantCode: http.StatusUnauthorized,
	}, { // Test 5: The session cookie admits.
		Target: "/api/ask?q=x", Cookie: "sekret", WantCode: http.StatusOK,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, test.Target, nil)
			if test.Bearer != "" {
				req.Header.Set("Authorization", "Bearer "+test.Bearer)
			}
			if test.Cookie != "" {
				req.AddCookie(&http.Cookie{Name: authCookie, Value: test.Cookie})
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != test.WantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, test.WantCode, rec.Body.String())
			}
			gotCookie := strings.Contains(rec.Header().Get("Set-Cookie"), authCookie+"=")
			if gotCookie != test.WantCookie {
				t.Errorf("set-cookie = %t, want %t", gotCookie, test.WantCookie)
			}
		})
	}
}

// TestFeedbackCrossOrigin verifies a cross-origin vote is rejected and a
// same-origin one is recorded.
func TestFeedbackCrossOrigin(t *testing.T) {
	t.Parallel()
	recorded := 0
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	h, err := Handler(Config{
		Ask:      ask,
		Feedback: func(feedback.Entry) error { recorded++; return nil },
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	body := `{"query":"billing","person":"jane@x.com","vote":"helpful"}`

	req := httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(body))
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || recorded != 0 {
		t.Errorf("cross-origin: status = %d recorded = %d, want 403 and 0", rec.Code, recorded)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || recorded != 1 {
		t.Errorf("same-origin: status = %d recorded = %d, want 200 and 1", rec.Code, recorded)
	}
}

// TestAskAPIModelDown verifies an unreachable model maps to guidance instead
// of a raw dial error.
func TestAskAPIModelDown(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, fmt.Errorf("llm resolve: %w: connection refused", llm.ErrModel)
	}
	h, err := Handler(Config{Ask: ask, Version: "test"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ask?q=x&mode=llm", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ollama.com") {
		t.Errorf("body = %s, want Ollama guidance", rec.Body.String())
	}
}

// TestHealthAndReady checks the liveness and readiness probes: /healthz is
// always 200 even behind a token, and /readyz reflects the readiness check
// while also bypassing the token.
func TestHealthAndReady(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	get := func(h http.Handler, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	// Liveness is 200 even behind a token and with no readiness check.
	tokened, err := Handler(Config{Ask: ask, AuthToken: "sekret"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if got := get(tokened, "/healthz"); got != http.StatusOK {
		t.Errorf("/healthz behind a token = %d, want 200", got)
	}
	if got := get(tokened, "/readyz"); got != http.StatusOK {
		t.Errorf("/readyz with no check = %d, want 200", got)
	}

	// Readiness reflects the check.
	notReady, _ := Handler(Config{Ask: ask, Ready: func() bool { return false }})
	if got := get(notReady, "/readyz"); got != http.StatusServiceUnavailable {
		t.Errorf("/readyz not ready = %d, want 503", got)
	}
	ready, _ := Handler(Config{Ask: ask, Ready: func() bool { return true }})
	if got := get(ready, "/readyz"); got != http.StatusOK {
		t.Errorf("/readyz ready = %d, want 200", got)
	}
}

// TestSearchAPI checks /api/search returns ranked results and rejects a missing
// query.
func TestSearchAPI(t *testing.T) {
	t.Parallel()
	ask := func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
		return resolve.Answer{}, nil
	}
	search := func(q string, _ int) []resolve.SearchResult {
		if q != "ada" {
			return nil
		}
		return []resolve.SearchResult{{Kind: "person", ID: "ada@x.io", Name: "Ada", Score: 40, Matched: []string{"name"}}}
	}
	h, err := Handler(Config{Ask: ask, Search: search})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?q=ada", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/search?q=ada = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ada@x.io") {
		t.Errorf("body missing the result: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("/api/search with no q = %d, want 400", rec.Code)
	}
}

// TestQueryLengthIsBounded checks an over-long question is refused rather than
// answered. Without the bound one request buys an unbounded amount of ranking
// work and an answer just as large, which a public instance pays for.
func TestQueryLengthIsBounded(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("kafka ", 200)
	h, err := Handler(Config{
		Ask: func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
			return resolve.Answer{}, nil
		},
		Search: func(_ string, _ int) []resolve.SearchResult { return nil },
		Recall: func(_ context.Context, _, _ string, _ int) (recall.Answer, error) {
			return recall.Answer{}, nil
		},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	tests := []struct {
		Name     string
		Target   string
		WantCode int
	}{{ // Test 0: A question anybody would actually type.
		Name: "normal ask", Target: "/api/ask?q=" + url.QueryEscape("who knows kafka"),
		WantCode: http.StatusOK,
	}, { // Test 1: Past the bound on ask.
		Name: "long ask", Target: "/api/ask?q=" + url.QueryEscape(long), WantCode: http.StatusBadRequest,
	}, { // Test 2: Past the bound on search.
		Name: "long search", Target: "/api/search?q=" + url.QueryEscape(long), WantCode: http.StatusBadRequest,
	}, { // Test 3: Past the bound on recall, which otherwise allows an empty query.
		Name: "long recall", Target: "/api/recall?me=jane@x.com&q=" + url.QueryEscape(long),
		WantCode: http.StatusBadRequest,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, test.Target, nil))
			if rec.Code != test.WantCode {
				t.Errorf("%s = %d, want %d (body %s)", test.Name, rec.Code, test.WantCode, rec.Body.String())
			}
		})
	}
}

// TestBriefEndpointServesAForwardableFile checks the served brief is the same
// self-contained artifact the CLI writes. The whole point of the page is that
// it survives being forwarded, so it must not depend on the server that made it.
func TestBriefEndpointServesAForwardableFile(t *testing.T) {
	t.Parallel()
	risks := []resolve.TopicRisk{{
		Topic: "billing", Level: "critical", Concentration: 1, BusFactor: 1,
		Experts: []resolve.RiskExpert{{ID: "ada@x.io", Name: "Ada", Share: 1}},
	}}
	h, err := Handler(Config{
		Ask: func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
			return resolve.Answer{}, nil
		},
		Brief: func() report.Brief {
			exposed := report.Exposures(risks)
			return report.Brief{
				Generated: time.Now(), People: 3, Scored: len(risks), Sources: []string{"git"},
				Risks: risks, Totals: report.Count(risks, exposed), Exposed: exposed,
			}
		},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report/risk.html", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("brief = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("content type = %q, want html", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("content disposition = %q, want it offered as a download", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "billing") || !strings.Contains(body, "Ada") {
		t.Error("the brief does not contain the finding it was built from")
	}
	for _, external := range []string{"src=\"http", "href=\"http", "fetch("} {
		if strings.Contains(body, external) {
			t.Errorf("the served brief reaches back to the server with %q", external)
		}
	}
}

// TestBriefRouteAbsentWithoutABriefFunc checks the route is not registered when
// the server was not given a way to build one, rather than answering with a
// broken page.
func TestBriefRouteAbsentWithoutABriefFunc(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	testHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report/risk.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("brief without a Brief function = %d, want 404", rec.Code)
	}
}

// TestExposureCarriesJoinedWork checks the web view is shown the same finding
// the command line and the brief report. Joined work is the heaviest thing in
// the report, and the exposure view is where somebody looks first, so leaving
// it out of one surface and not the others is how the three drift apart.
func TestExposureCarriesJoinedWork(t *testing.T) {
	t.Parallel()
	h, err := Handler(Config{
		Ask: func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
			return resolve.Answer{}, nil
		},
		Exposure: func() Exposure {
			return Exposure{Regions: []resolve.Region{{
				Lead: "Ada", LeadID: "ada@x.com", Topics: []string{"dlna", "dmr", "dms"},
			}}}
		},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/exposure", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("exposure = %d, want 200", rec.Code)
	}
	var got Exposure
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Regions) != 1 {
		t.Fatalf("regions = %+v, want the joined work carried through", got.Regions)
	}
	if got.Regions[0].Size() != 3 || got.Regions[0].Lead != "Ada" {
		t.Errorf("region = %+v, want Ada with three joined subjects", got.Regions[0])
	}
}

// TestExposureCarriesOnePersonConnections checks the web view is shown the
// connections that rest on one person, the same as the command line and the
// brief. This was missing from the web view alone for a while: the finding was
// computed, printed, and written into the report, and the one surface most
// people actually look at never mentioned it.
func TestExposureCarriesOnePersonConnections(t *testing.T) {
	t.Parallel()
	h, err := Handler(Config{
		Ask: func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
			return resolve.Answer{}, nil
		},
		Exposure: func() Exposure {
			return Exposure{Spans: []resolve.Span{{
				Topics: []string{"billing", "ledger"}, Person: "Ada", PersonID: "ada@x.com",
				Together: 0.25, Experts: 6,
			}}}
		},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/exposure", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("exposure = %d, want 200", rec.Code)
	}
	var got Exposure
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Spans) != 1 {
		t.Fatalf("spans = %+v, want the connection carried through", got.Spans)
	}
	if got.Spans[0].Person != "Ada" || got.Spans[0].Experts != 6 {
		t.Errorf("span = %+v, want Ada named with the six who hold the two subjects", got.Spans[0])
	}
}

// TestExposureViewHasSomewhereToShowEveryFinding checks the page carries a
// container for each thing the exposure payload can report. A finding that
// reaches the browser with nothing to draw it into is invisible, and that reads
// exactly like the finding not existing.
func TestExposureViewHasSomewhereToShowEveryFinding(t *testing.T) {
	t.Parallel()
	h, err := Handler(Config{
		Ask: func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
			return resolve.Answer{}, nil
		},
		Exposure: func() Exposure { return Exposure{} },
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, id := range []string{"exp-risk", "exp-drift", "exp-regions", "exp-spans"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("the page has nowhere to draw %s", id)
		}
	}
}

// TestOrgChartEndpointCarriesTheChart checks the chart reaches the browser with
// the knowledge on each seat, not just the reporting lines. The reporting lines
// alone are the part every company already has.
func TestOrgChartEndpointCarriesTheChart(t *testing.T) {
	t.Parallel()
	h, err := Handler(Config{
		Ask: func(_ context.Context, _, _, _ string, _ int) (resolve.Answer, error) {
			return resolve.Answer{}, nil
		},
		OrgChart: func() resolve.Chart {
			return resolve.Chart{
				People: 2, Unplaced: 3,
				Roots: []resolve.Seat{{
					ID: "boss@x.com", Name: "Boss", Title: "VP",
					Reports: []resolve.Seat{{
						ID: "ada@x.com", Name: "Ada", Team: "Payments",
						Knows: []string{"billing"}, Alone: []string{"billing"},
					}},
				}},
			}
		},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orgchart", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("orgchart = %d, want 200", rec.Code)
	}
	var got resolve.Chart
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Roots) != 1 || got.Roots[0].Name != "Boss" {
		t.Fatalf("chart = %+v, want the root carried through", got)
	}
	if got.Unplaced != 3 {
		t.Errorf("unplaced = %d, want it reported so a thin directory is visible", got.Unplaced)
	}
	ada := got.Roots[0].Reports
	if len(ada) != 1 || len(ada[0].Alone) != 1 {
		t.Errorf("seat = %+v, want the knowledge on it and not only the reporting line", ada)
	}
}
