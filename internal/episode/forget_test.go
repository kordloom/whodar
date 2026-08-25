package episode

import (
	"fmt"
	"testing"
	"time"
)

// storeWith fills a store with n episodes, each with its own vocabulary, so the
// postings hold far more terms than any single episode contributed.
func storeWith(t *testing.T, n int) *Store {
	t.Helper()
	s := New()
	for i := range n {
		s.Add(Episode{
			ID:       fmt.Sprintf("ep%d", i),
			Title:    fmt.Sprintf("subject%d trouble", i),
			Body:     fmt.Sprintf("word%da word%db word%dc", i, i, i),
			Occurred: time.Now(),
		})
	}
	return s
}

// TestForgettingVisitsOnlyItsOwnTerms checks removing an episode does not walk
// the whole vocabulary. It used to, which made re-indexing cost the number of
// episodes times the size of the vocabulary: quadratic, and felt as a long
// silent pause on every re-run.
func TestForgettingVisitsOnlyItsOwnTerms(t *testing.T) {
	t.Parallel()
	s := storeWith(t, 200)
	before := len(s.postings)

	// The episode being removed knows its own terms.
	mine := len(s.termsOf["ep7"])
	if mine == 0 {
		t.Fatal("the episode recorded none of its own terms")
	}
	if mine >= before {
		t.Fatalf("one episode claims %d terms of a %d term vocabulary, which is not a subset",
			mine, before)
	}
	s.forget(s.episodes["ep7"])

	if _, ok := s.termsOf["ep7"]; ok {
		t.Error("the forgotten episode still claims terms")
	}
	// Its own terms are gone from the postings and nobody else's are.
	for term, posting := range s.postings {
		if _, ok := posting["ep7"]; ok {
			t.Errorf("term %q still points at the forgotten episode", term)
		}
	}
	if len(s.postings) >= before {
		t.Errorf("vocabulary is %d after forgetting, was %d: its terms were not removed",
			len(s.postings), before)
	}
	// Everybody else is untouched.
	if len(s.termsOf) != 199 {
		t.Errorf("%d episodes still know their terms, want 199", len(s.termsOf))
	}
}

// TestForgettingWorksAfterAReload checks a store restored from disk, which has
// postings but no record of which terms came from where, still removes an
// episode completely.
func TestForgettingWorksAfterAReload(t *testing.T) {
	t.Parallel()
	s := storeWith(t, 20)
	// A store as it arrives from disk, before its terms are derived.
	s.termsOf = nil
	s.forget(s.episodes["ep3"])
	for term, posting := range s.postings {
		if _, ok := posting["ep3"]; ok {
			t.Errorf("term %q still points at the forgotten episode after a reload", term)
		}
	}
}
