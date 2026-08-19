package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// fixedTime pins a timestamp so round trips are deterministic.
var fixedTime = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

// TestRoundTrip verifies a watermark survives Save and Load unchanged.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.state.json")
	s := New()
	wm := Watermark{Source: "jira", Scope: "project:SEC,SUP", Cursor: fixedTime, Complete: true, RanAt: fixedTime}
	s.Set(wm)
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gotWM, ok := got.Get("jira", "project:SEC,SUP")
	if !ok {
		t.Fatal("watermark missing after round trip")
	}
	if diff := cmp.Diff(wm, gotWM); diff != "" {
		t.Errorf("watermark mismatch (-want +got):\n%s", diff)
	}
}

// TestLoadMissingIsEmpty verifies a missing file yields an empty state rather
// than an error, so a first run is a full index.
func TestLoadMissingIsEmpty(t *testing.T) {
	t.Parallel()
	s, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if s == nil || len(s.Watermarks) != 0 || s.Version != Version {
		t.Errorf("Load missing = %+v, want empty state at version %d", s, Version)
	}
}

// TestGetSetDelete verifies the in-memory watermark map operations.
func TestGetSetDelete(t *testing.T) {
	t.Parallel()
	s := New()
	if _, ok := s.Get("jira", "x"); ok {
		t.Error("empty state returned a watermark")
	}
	s.Set(Watermark{Source: "jira", Scope: "x", Cursor: fixedTime})
	if _, ok := s.Get("jira", "x"); !ok {
		t.Error("Set watermark not found by Get")
	}
	s.Delete("jira", "x")
	if _, ok := s.Get("jira", "x"); ok {
		t.Error("Delete did not remove the watermark")
	}
}

// TestKey verifies distinct source and scope pairs never share a key.
func TestKey(t *testing.T) {
	t.Parallel()
	if Key("jira", "a") == Key("jira", "b") {
		t.Error("different scopes produced the same key")
	}
	if Key("jira", "a") == Key("confluence", "a") {
		t.Error("different sources produced the same key")
	}
	if Key("a", "b") == Key("a\x00b", "") {
		t.Error("separator collision between source and scope")
	}
}
