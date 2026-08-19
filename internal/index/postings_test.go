package index

import (
	"math"
	"testing"

	"github.com/kordloom/whodar/internal/model"
)

// TestPostingsCodec verifies the binary postings encoding round-trips within
// float32 precision, handles an empty index, and rejects a truncated blob rather
// than panicking on corrupt input.
func TestPostingsCodec(t *testing.T) {
	t.Parallel()
	in := map[string]map[model.ID]float64{
		"billing": {"jane@x.com": 1.5, "bob@x.com": 0.25},
		"retries": {"jane@x.com": 2.0},
		"empty":   {},
	}
	blob := encodePostings(in)
	out, err := decodePostings(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("terms = %d, want %d", len(out), len(in))
	}
	for term, people := range in {
		if len(out[term]) != len(people) {
			t.Errorf("term %q has %d people, want %d", term, len(out[term]), len(people))
		}
		for id, w := range people {
			if got := out[term][id]; math.Abs(got-w) > 1e-6 {
				t.Errorf("%s/%s weight = %v, want %v", term, id, got, w)
			}
		}
	}

	// An empty blob is an empty index, not an error.
	if empty, err := decodePostings(nil); err != nil || len(empty) != 0 {
		t.Errorf("empty decode = %v, %v; want empty and no error", empty, err)
	}

	// A truncated blob errors rather than panicking on a bad slice index.
	if _, err := decodePostings(blob[:len(blob)/2]); err == nil {
		t.Error("a truncated blob did not error")
	}
}
