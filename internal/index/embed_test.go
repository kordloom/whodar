package index

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
)

// fakeEmbedder maps text to a small vector over a fixed vocabulary, so cosine
// similarity is deterministic in tests.
type fakeEmbedder struct{}

// Embed sets a dimension for each vocabulary word present in text.
func (fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vocab := []string{"retries", "kafka", "billing", "infra"}
	t := strings.ToLower(text)
	vec := make([]float32, len(vocab))
	for i, w := range vocab {
		if strings.Contains(t, w) {
			vec[i] = 1
		}
	}
	return vec, nil
}

// TestSemanticSearch verifies embedding and cosine ranking pick the right person.
func TestSemanticSearch(t *testing.T) {
	t.Parallel()
	ix := New()
	ix.Build([]connector.Record{
		{Name: "Jane Roe", Email: "jane@x.com", Team: "Billing", Topics: []string{"retries"}},
		{Name: "Bob Lee", Email: "bob@x.com", Team: "Infra", Topics: []string{"kafka"}},
	})
	if ix.HasEmbeddings() {
		t.Fatal("index should have no embeddings before Embed")
	}
	if err := ix.Embed(context.Background(), fakeEmbedder{}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !ix.HasEmbeddings() {
		t.Fatal("index should have embeddings after Embed")
	}

	q, _ := fakeEmbedder{}.Embed(context.Background(), "retries")
	got := ix.SemanticPeople(q, 5)
	if len(got) == 0 || got[0].Person.Email != "jane@x.com" {
		t.Fatalf("top semantic person = %v, want jane@x.com", got)
	}
	if got[0].Confidence <= 0 || got[0].Confidence != got[0].Score {
		t.Errorf("confidence = %v score = %v, want similarity as confidence", got[0].Confidence, got[0].Score)
	}
}

// TestEmbedSaveLoad verifies vectors survive a round trip to disk.
func TestEmbedSaveLoad(t *testing.T) {
	t.Parallel()
	ix := New()
	ix.Build([]connector.Record{{Name: "Jane", Email: "jane@x.com", Topics: []string{"retries"}}})
	if err := ix.Embed(context.Background(), fakeEmbedder{}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	path := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.HasEmbeddings() {
		t.Fatal("embeddings lost on save and load")
	}
	q, _ := fakeEmbedder{}.Embed(context.Background(), "retries")
	if got := loaded.SemanticPeople(q, 1); len(got) != 1 || got[0].Person.Email != "jane@x.com" {
		t.Fatalf("after load, semantic top = %v, want jane@x.com", got)
	}
}
