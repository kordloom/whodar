package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPostMessage verifies the channel, text, and thread parameters are sent.
func TestPostMessage(t *testing.T) {
	t.Parallel()
	var gotChannel, gotText, gotThread string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.PostFormValue("channel")
		gotText = r.PostFormValue("text")
		gotThread = r.PostFormValue("thread_ts")
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)

	c := New("xoxb-test", WithBaseURL(srv.URL))
	if err := c.PostMessage(context.Background(), "C1", "123.1", "hello"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if gotChannel != "C1" || gotText != "hello" || gotThread != "123.1" {
		t.Errorf("params: channel=%q text=%q thread=%q", gotChannel, gotText, gotThread)
	}
}

// TestAuthTest verifies the bot user ID is returned.
func TestAuthTest(t *testing.T) {
	t.Parallel()
	srv := newServer(t, map[string][]canned{
		"auth.test": {{status: 200, body: `{"ok":true,"user_id":"U999"}`}},
	})
	c := New("xoxb-test", WithBaseURL(srv.URL))
	auth, err := c.AuthTest(context.Background())
	id := auth.UserID
	if err != nil || id != "U999" {
		t.Fatalf("AuthTest = %q, %v; want U999", id, err)
	}
}

// TestConnectionsOpen verifies the WebSocket URL is returned.
func TestConnectionsOpen(t *testing.T) {
	t.Parallel()
	srv := newServer(t, map[string][]canned{
		"apps.connections.open": {{status: 200, body: `{"ok":true,"url":"wss://example/link"}`}},
	})
	c := New("xapp-test", WithBaseURL(srv.URL))
	gotURL, err := c.ConnectionsOpen(context.Background())
	if err != nil || gotURL != "wss://example/link" {
		t.Fatalf("ConnectionsOpen = %q, %v; want wss://example/link", gotURL, err)
	}
}
