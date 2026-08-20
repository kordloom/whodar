package cmd

import (
	"fmt"
	"testing"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/index"
)

// TestLoopbackAddr verifies which listen addresses count as loopback-only.
func TestLoopbackAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		In   string
		Want bool
	}{
		{In: "127.0.0.1:8765", Want: true},  // Test 0: IPv4 loopback.
		{In: "localhost:8765", Want: true},  // Test 1: Localhost by name.
		{In: "[::1]:8765", Want: true},      // Test 2: IPv6 loopback.
		{In: "0.0.0.0:8765", Want: false},   // Test 3: Every interface.
		{In: ":8765", Want: false},          // Test 4: Empty host binds everything.
		{In: "10.0.0.5:8765", Want: false},  // Test 5: A LAN address.
		{In: "host.corp:8765", Want: false}, // Test 6: A non-loopback hostname.
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			if got := loopbackAddr(test.In); got != test.Want {
				t.Errorf("test %d: loopbackAddr(%q) = %t, want %t", testNum, test.In, got, test.Want)
			}
		})
	}
}

// TestRecallFnPublicExemption checks that the demo's public flag lets recall
// serve off loopback, while a normal off-loopback bind or any serve token keeps
// it off. The demo's history is sample data, so it has nothing private to gate.
func TestRecallFnPublicExemption(t *testing.T) {
	t.Parallel()
	store := episode.New()
	store.Add(episode.Episode{ID: "e1"})
	ix := index.New()
	tests := []struct {
		Addr   string
		Token  string
		Public bool
		Want   bool
	}{
		{Addr: "127.0.0.1:8765", Public: false, Want: true},           // Test 0: loopback, no token.
		{Addr: "0.0.0.0:8765", Public: true, Want: true},              // Test 1: public demo off loopback.
		{Addr: "0.0.0.0:8765", Public: false, Want: false},            // Test 2: off loopback, not public.
		{Addr: "0.0.0.0:8765", Token: "x", Public: true, Want: false}, // Test 3: token gates it regardless.
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			cfg := webConfig{addr: test.Addr, public: test.Public, episodes: store}
			if got := recallFn(ix, cfg, test.Token) != nil; got != test.Want {
				t.Errorf("recallFn present = %t, want %t", got, test.Want)
			}
		})
	}
}
