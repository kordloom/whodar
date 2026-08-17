package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/resolve"
	"github.com/kordloom/whodar/internal/slack"
)

// fakeConn yields canned frames then EOF, recording writes.
type fakeConn struct {
	reads  [][]byte
	idx    int
	writes [][]byte
}

// Read returns the next canned frame or io.EOF.
func (f *fakeConn) Read(context.Context) ([]byte, error) {
	if f.idx >= len(f.reads) {
		return nil, io.EOF
	}
	b := f.reads[f.idx]
	f.idx++
	return b, nil
}

// Write records the sent frame.
func (f *fakeConn) Write(_ context.Context, data []byte) error {
	f.writes = append(f.writes, append([]byte(nil), data...))
	return nil
}

// Close is a no-op.
func (f *fakeConn) Close() error { return nil }

// stubApp returns a client with a dummy app token; session does not call it.
func stubApp(t *testing.T) *slack.Client {
	t.Helper()
	return slack.New("xapp-test")
}

// okEngine returns an engine whose ask always answers.
func okEngine() *Engine {
	return New(func(context.Context, string, string, int) (resolve.Answer, error) {
		return sampleAnswer(), nil
	}, "keyword", "UBOT", 5)
}

// TestSocketSessionDispatchesAndAcks verifies a mention is answered and acked.
func TestSocketSessionDispatchesAndAcks(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	s := NewSocketRunner(stubApp(t), okEngine(), rec, "UBOT")
	conn := &fakeConn{reads: [][]byte{
		[]byte(`{"type":"hello"}`),
		[]byte(`{"type":"events_api","envelope_id":"e1","payload":{"event":{` +
			`"type":"app_mention","text":"<@UBOT> billing","channel":"C1","user":"U2","ts":"5.5"}}}`),
	}}

	_ = s.session(context.Background(), conn)

	if rec.calls != 1 || rec.channel != "C1" {
		t.Fatalf("expected one reply to C1, got %+v", rec)
	}
	if rec.thread != "5.5" {
		t.Errorf("mention reply should thread on ts, got %q", rec.thread)
	}
	acked := false
	for _, w := range conn.writes {
		var m map[string]string
		if json.Unmarshal(w, &m) == nil && m["envelope_id"] == "e1" {
			acked = true
		}
	}
	if !acked {
		t.Error("envelope e1 was not acked")
	}
}

// blockingConn feeds canned frames, then blocks Read until hold closes, so a
// session stays open while a test observes acks. Writes land on a channel so a
// test can watch them arrive concurrently.
type blockingConn struct {
	reads  [][]byte
	idx    int
	writes chan []byte
	hold   chan struct{}
}

// Read returns the next canned frame, then waits for hold before signaling EOF.
func (c *blockingConn) Read(ctx context.Context) ([]byte, error) {
	if c.idx < len(c.reads) {
		b := c.reads[c.idx]
		c.idx++
		return b, nil
	}
	select {
	case <-c.hold:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Write publishes the frame to the writes channel.
func (c *blockingConn) Write(_ context.Context, data []byte) error {
	c.writes <- append([]byte(nil), data...)
	return nil
}

// Close is a no-op.
func (c *blockingConn) Close() error { return nil }

// safeRecorder counts replies under a mutex, for tests that answer from more
// than one goroutine at once.
type safeRecorder struct {
	mu    sync.Mutex
	calls int
}

// Reply records one reply.
func (r *safeRecorder) Reply(_ context.Context, _, _, _ string) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return nil
}

// mentionFrame builds an app_mention events_api envelope with the given id.
func mentionFrame(id, text string) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"events_api","envelope_id":%q,"payload":{"event":{`+
			`"type":"app_mention","text":%q,"channel":"C1","user":"U2","ts":"5.5"}}}`, id, text))
}

// TestSocketReadLoopNotBlockedBySlowAnswer verifies a slow answer does not stall
// the read loop: the second envelope must be acked while the first answer is
// still in flight. An inline handler would never reach the second Read.
func TestSocketReadLoopNotBlockedBySlowAnswer(t *testing.T) {
	t.Parallel()
	var once sync.Once
	started := make(chan struct{})
	release := make(chan struct{})
	eng := New(func(ctx context.Context, _, _ string, _ int) (resolve.Answer, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return resolve.Answer{}, ctx.Err()
		}
		return sampleAnswer(), nil
	}, "keyword", "UBOT", 5)

	s := NewSocketRunner(stubApp(t), eng, &safeRecorder{}, "UBOT")
	conn := &blockingConn{
		writes: make(chan []byte, 8),
		hold:   make(chan struct{}),
		reads:  [][]byte{mentionFrame("e1", "billing"), mentionFrame("e2", "kafka")},
	}

	done := make(chan struct{})
	go func() { _ = s.session(context.Background(), conn); close(done) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first answer never started")
	}

	acked := map[string]bool{}
	for len(acked) < 2 {
		select {
		case w := <-conn.writes:
			var m map[string]string
			if json.Unmarshal(w, &m) == nil && m["envelope_id"] != "" {
				acked[m["envelope_id"]] = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("read loop blocked by slow answer: acked only %v", acked)
		}
	}
	if !acked["e1"] || !acked["e2"] {
		t.Errorf("acked = %v, want both e1 and e2", acked)
	}

	close(release)
	close(conn.hold)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not return after release")
	}
}

// TestSocketIgnoresOwnAndOther verifies self, bot, and non-mention messages are
// not answered.
func TestSocketIgnoresOwnAndOther(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	s := NewSocketRunner(stubApp(t), okEngine(), rec, "UBOT")
	conn := &fakeConn{reads: [][]byte{
		[]byte(`{"type":"events_api","envelope_id":"e1","payload":{"event":{` +
			`"type":"app_mention","text":"hi","channel":"C1","user":"UBOT","ts":"1"}}}`),
		[]byte(`{"type":"events_api","envelope_id":"e2","payload":{"event":{` +
			`"type":"message","text":"hi","channel":"C1","bot_id":"B1"}}}`),
		[]byte(`{"type":"events_api","envelope_id":"e3","payload":{"event":{` +
			`"type":"message","channel_type":"channel","text":"hi","channel":"C1","user":"U2"}}}`),
	}}

	_ = s.session(context.Background(), conn)

	if rec.calls != 0 {
		t.Errorf("should ignore self, bot, and non-mention messages, calls=%d", rec.calls)
	}
}
