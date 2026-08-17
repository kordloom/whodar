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

	"github.com/kordloom/whodar/internal/slack"
)

// TestSlackFetch verifies the connector turns Slack data into person and
// channel records, joining message authors to channel membership by email.
func TestSlackFetch(t *testing.T) {
	t.Parallel()
	const (
		usersJSON = `{"ok":true,"members":[
			{"id":"U1","profile":{"real_name":"Jane Roe","email":"jane@x.com","title":"Staff Engineer"}},
			{"id":"U2","profile":{"real_name":"Bob Lee","email":"bob@x.com","title":"SRE"}}]}`
		channelsJSON = `{"ok":true,"channels":[
			{"id":"C1","name":"billing","topic":{"value":"retries and dunning"},
			 "purpose":{"value":"billing platform"}}]}`
		historyJSON = `{"ok":true,"has_more":false,"messages":[
			{"type":"message","user":"U1","text":"we fixed the retries bug","ts":"1.0"},
			{"type":"message","user":"U2","text":"kafka lag again","ts":"2.0"},
			{"type":"message","subtype":"channel_join","user":"U1","text":"joined","ts":"3.0"},
			{"type":"message","bot_id":"B1","text":"deploy done","ts":"4.0"}]}`
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/users.list", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, usersJSON)
	})
	mux.HandleFunc("/conversations.list", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, channelsJSON)
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, historyJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := slack.New("xoxb-test", slack.WithBaseURL(srv.URL))
	recs, err := NewSlackWithClient(client, SlackOptions{}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	var channel *Record
	people := make(map[string]Record)
	janeTalksRetries := false
	var janeTextTime time.Time
	for i := range recs {
		r := recs[i]
		switch r.Kind {
		case KindChannel:
			channel = &recs[i]
		case KindPerson:
			if r.Name != "" {
				people[r.Email] = r
			}
			if r.PersonID == "jane@x.com" && strings.Contains(r.Text, "retries") {
				janeTalksRetries = true
				janeTextTime = r.Time
			}
		}
	}

	if channel == nil {
		t.Fatal("no channel record emitted")
	}
	if channel.Name != "billing" || channel.Title != "retries and dunning" {
		t.Errorf("channel = %+v, want billing / retries and dunning", channel)
	}
	if !slices.Contains(channel.Members, "jane@x.com") || !slices.Contains(channel.Members, "bob@x.com") {
		t.Errorf("members = %v, want jane and bob", channel.Members)
	}
	if len(channel.Members) != 2 {
		t.Errorf("members = %v, want exactly 2 (bot and join skipped)", channel.Members)
	}
	if people["jane@x.com"].Email != "jane@x.com" || people["jane@x.com"].Title != "Staff Engineer" {
		t.Errorf("jane person record = %+v", people["jane@x.com"])
	}
	// The Slack user ID travels with the email so the indexer can join them,
	// which is what lets the bot tell who is asking.
	if got := people["jane@x.com"].PersonID; got != "slack:U1" {
		t.Errorf("jane person id = %q, want slack:U1", got)
	}
	if !janeTalksRetries {
		t.Error("expected a person record giving Jane retries affinity from her messages")
	}
	if want := time.Unix(1, 0).UTC(); !janeTextTime.Equal(want) {
		t.Errorf("jane text record time = %v, want %v", janeTextTime, want)
	}
	if want := time.Unix(2, 0).UTC(); !channel.Time.Equal(want) {
		t.Errorf("channel time = %v, want the latest user message time %v", channel.Time, want)
	}
}

// TestSlackFetchSkipsUnreadableChannels verifies one unreadable channel, such
// as a public channel the bot never joined, does not abort the run: the
// readable channel still indexes and the skip is logged.
func TestSlackFetchSkipsUnreadableChannels(t *testing.T) {
	t.Parallel()
	const (
		usersJSON = `{"ok":true,"members":[
			{"id":"U1","profile":{"real_name":"Jane Roe","email":"jane@x.com"}}]}`
		channelsJSON = `{"ok":true,"channels":[
			{"id":"C1","name":"locked-room"},
			{"id":"C2","name":"billing","topic":{"value":"retries"}}]}`
		historyJSON = `{"ok":true,"has_more":false,"messages":[
			{"type":"message","user":"U1","text":"retries fixed","ts":"1.0"}]}`
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/users.list", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, usersJSON)
	})
	mux.HandleFunc("/conversations.list", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, channelsJSON)
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("channel") == "C1" {
			io.WriteString(w, `{"ok":false,"error":"not_in_channel"}`)
			return
		}
		io.WriteString(w, historyJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var log strings.Builder
	client := slack.New("xoxb-test", slack.WithBaseURL(srv.URL))
	recs, err := NewSlackWithClient(client, SlackOptions{Log: &log}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	var names []string
	for _, r := range recs {
		if r.Kind == KindChannel {
			names = append(names, r.Name)
		}
	}
	if !slices.Contains(names, "billing") || slices.Contains(names, "locked-room") {
		t.Errorf("channels = %v, want billing indexed and locked-room skipped", names)
	}
	got := log.String()
	if !strings.Contains(got, "skipping #locked-room") || !strings.Contains(got, "not_in_channel") {
		t.Errorf("log missing skip line:\n%s", got)
	}
	// One channel read and one skipped is the invite case, not the missing
	// scope case, so the summary points at inviting the bot.
	if !strings.Contains(got, "skipped 1 channels the bot is not in") {
		t.Errorf("log missing skip summary:\n%s", got)
	}
}

// TestSlackAllUnreadableNamesTheScope verifies that when no channel can be read
// the summary points at the missing OAuth scope, not at inviting the bot to
// every channel. A token without channels:history fails all of them, and
// telling the user to send a hundred invites would be wrong advice.
func TestSlackAllUnreadableNamesTheScope(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":true,"user_id":"UBOT","url":"https://x.slack.com/","team":"X"}`)
	})
	mux.HandleFunc("/users.list", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":true,"members":[{"id":"U1","profile":{"real_name":"Jane","email":"jane@x.com"}}]}`)
	})
	mux.HandleFunc("/conversations.list", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":true,"channels":[{"id":"C1","name":"billing"},{"id":"C2","name":"platform"}]}`)
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"missing_scope"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var log strings.Builder
	client := slack.New("xoxb-test", slack.WithBaseURL(srv.URL))
	if _, err := NewSlackWithClient(client, SlackOptions{Log: &log}).Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := log.String()
	if !strings.Contains(got, "channels:history scope") {
		t.Errorf("log does not name the missing scope when every channel failed:\n%s", got)
	}
	if strings.Contains(got, "invite it to the ones") {
		t.Errorf("log wrongly told the user to invite the bot when the scope is missing:\n%s", got)
	}
}
