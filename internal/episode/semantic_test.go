package episode

import (
	"testing"
)

// TestSearchSemantic verifies meaning-based recall ranks the closest
// conversation first and still refuses to leave the asker's own history.
func TestSearchSemantic(t *testing.T) {
	t.Parallel()
	s := newTestStore()
	s.Add(withBody(testEpisode("close", 30, "me@x.com"), "kafka lag"))
	s.Add(withBody(testEpisode("far", 30, "me@x.com"), "billing invoice"))
	s.Add(withBody(testEpisode("theirs", 30, "someone@x.com"), "kafka lag"))
	s.SetVector("close", []float32{1, 0, 0})
	s.SetVector("far", []float32{0, 1, 0})
	s.SetVector("theirs", []float32{1, 0, 0})

	got := s.SearchSemantic([]float32{0.9, 0.1, 0}, Query{Person: "me@x.com"})
	if len(got) != 2 {
		t.Fatalf("results = %+v, want the asker's two conversations", got)
	}
	if got[0].Episode.ID != "close" {
		t.Errorf("top = %q, want close", got[0].Episode.ID)
	}
	for _, r := range got {
		if r.Episode.ID == "theirs" {
			t.Error("a conversation the asker was not in came back")
		}
	}
	if got[0].Confidence <= 0 {
		t.Errorf("confidence = %v, want above zero", got[0].Confidence)
	}
}

// TestSearchSemanticWithoutVectors verifies meaning-based recall returns
// nothing rather than guessing when no conversation was embedded.
func TestSearchSemanticWithoutVectors(t *testing.T) {
	t.Parallel()
	s := newTestStore()
	s.Add(withBody(testEpisode("a", 1, "me@x.com"), "kafka lag"))
	if s.HasVectors() {
		t.Fatal("HasVectors = true with nothing embedded")
	}
	if got := s.SearchSemantic([]float32{1, 0}, Query{Person: "me@x.com"}); len(got) != 0 {
		t.Errorf("results = %+v, want none", got)
	}
	s.SetVector("a", []float32{1, 0})
	if !s.HasVectors() {
		t.Error("HasVectors = false after embedding one conversation")
	}
	if got := s.SearchSemantic(nil, Query{Person: "me@x.com"}); len(got) != 0 {
		t.Errorf("results for an empty query vector = %+v, want none", got)
	}
}

// TestVectorDroppedWithEpisode verifies replacing a conversation clears its old
// vector, so a stale embedding cannot outlive the text it came from.
func TestVectorDroppedWithEpisode(t *testing.T) {
	t.Parallel()
	s := newTestStore()
	s.Add(withBody(testEpisode("a", 1, "me@x.com"), "kafka lag"))
	s.SetVector("a", []float32{1, 0})
	s.Add(withBody(testEpisode("a", 1, "me@x.com"), "billing invoice"))
	if _, ok := s.Vector("a"); ok {
		t.Error("the old vector survived a replacement")
	}
}
