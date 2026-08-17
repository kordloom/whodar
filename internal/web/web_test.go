package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/llm"
	"github.com/kordloom/whodar/internal/model"
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
