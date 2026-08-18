package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
)

// TestSavedIndexHoldsNoReadableText verifies a saved index contains the stemmed
// search terms but not the readable message text they came from. This is the
// promise the pricing page makes about the free tier: the graph and a search
// index live on disk, the conversations do not.
func TestSavedIndexHoldsNoReadableText(t *testing.T) {
	t.Parallel()
	const phrase = "the quarterly reconciliation ledger drifted by seventeen cents"
	ix := New()
	ix.Build([]connector.Record{
		{Source: "slack", Kind: connector.KindPerson, Email: "jane@x.com", Name: "Jane",
			Text: phrase},
		{Source: "slack", Kind: connector.KindChannel, Name: "finance",
			Text: "channel where " + phrase},
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	if err := ix.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	on := string(raw)

	// No readable prose: not the phrase, nor any multi-word run from it. An
	// inverted search index legitimately contains individual stemmed words,
	// but the conversation itself, its word order and sentences, must be gone.
	if strings.Contains(on, phrase) {
		t.Error("the saved index contains the readable message phrase verbatim")
	}
	for _, run := range []string{
		"quarterly reconciliation", "reconciliation ledger", "ledger drifted",
		"drifted by seventeen", "channel where",
	} {
		if strings.Contains(on, run) {
			t.Errorf("the saved index contains the readable word run %q", run)
		}
	}
	// The stemmed search term is present, so keyword search still works.
	if !strings.Contains(on, "reconcil") {
		t.Error("the saved index is missing the stemmed search term")
	}

	// Reloaded, the person is still findable by the words they used.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	hits := reloaded.Search("reconciliation ledger", 3)
	if len(hits) == 0 || hits[0].Person.Email != "jane@x.com" {
		t.Fatalf("reloaded index no longer finds the person: %+v", hits)
	}
}

// TestRedactedIndexStillMergesIdempotently verifies a source re-read after a
// save-and-reload replaces its own contribution rather than stacking, even
// though the reloaded records carry only stemmed terms and no readable text.
func TestRedactedIndexStillMergesIdempotently(t *testing.T) {
	t.Parallel()
	recs := []connector.Record{
		{Source: "slack", Kind: connector.KindPerson, Email: "jane@x.com", Name: "Jane",
			Text: "billing retries kept failing on the payment worker"},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")

	first := New()
	first.Build(recs)
	if err := first.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := first.Search("billing retries", 1)

	// Reload the redacted index, then re-read the same source and re-save.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	reloaded.Add(recs)
	got := reloaded.Search("billing retries", 1)

	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("search after reload+merge = %d, want %d", len(got), len(want))
	}
	if got[0].Person.Email != want[0].Person.Email {
		t.Errorf("merge changed the answer: %q vs %q", got[0].Person.Email, want[0].Person.Email)
	}
	// The weight must match the single-index weight, not double it.
	if diff := got[0].Score - want[0].Score; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("re-merging a redacted source inflated the score: %.6f vs %.6f",
			got[0].Score, want[0].Score)
	}
}
