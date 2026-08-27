package index

import (
	"context"
	"fmt"
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
	if got[0].Strength <= 0 || got[0].Strength != got[0].Score {
		t.Errorf("strength = %v score = %v, want similarity as strength", got[0].Strength, got[0].Score)
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

// TestStandoutStrength checks that a similarity is reported as how far a match
// stands above its field, not as the raw number. A question that suits everyone
// equally must not come back with a confident name attached.
func TestStandoutStrength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Name      string
		Ranked    []scoredID
		WantFirst float64
		WantAny   bool
	}{{ // Test 0: A flat field distinguishes nobody, so nobody is named.
		Name: "flat field",
		Ranked: []scoredID{
			{id: "a", score: 0.638}, {id: "b", score: 0.637},
			{id: "c", score: 0.636}, {id: "d", score: 0.635},
			{id: "e", score: 0.634},
		},
		WantAny: false,
	}, { // Test 1: A clear standout keeps a high strength.
		Name: "one clear match",
		Ranked: []scoredID{
			{id: "a", score: 0.90}, {id: "b", score: 0.62},
			{id: "c", score: 0.61}, {id: "d", score: 0.60},
			{id: "e", score: 0.59},
		},
		WantFirst: 1, WantAny: true,
	}, { // Test 2: Too few results to form a field, so the score stands as it is.
		Name:      "no field to judge",
		Ranked:    []scoredID{{id: "a", score: 0.7}, {id: "b", score: 0.2}},
		WantFirst: 0.7, WantAny: true,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			t.Parallel()
			if got := discriminating(test.Ranked); got != test.WantAny {
				t.Errorf("discriminating = %v, want %v", got, test.WantAny)
			}
			if !test.WantAny {
				return
			}
			median, ok := fieldMedian(test.Ranked)
			if got := standout(test.Ranked[0].score, median, ok); got != test.WantFirst {
				t.Errorf("strength = %v, want %v", got, test.WantFirst)
			}
		})
	}
}
