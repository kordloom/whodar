package invindex

import (
	"math"
	"testing"
)

// TestPostingsCodec verifies the encoding round-trips within float32 precision,
// handles an empty index, and rejects a truncated blob rather than panicking.
func TestPostingsCodec(t *testing.T) {
	t.Parallel()
	in := map[string]map[string]float64{
		"billing": {"jane@x.com": 1.5, "bob@x.com": 0.25},
		"retries": {"jane@x.com": 2.0},
		"empty":   {},
	}
	blob := EncodePostings(in)
	out, err := DecodePostings[string](blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("terms = %d, want %d", len(out), len(in))
	}
	for term, keys := range in {
		for k, w := range keys {
			if got := out[term][k]; math.Abs(got-w) > 1e-6 {
				t.Errorf("%s/%s = %v, want %v", term, k, got, w)
			}
		}
	}
	if empty, err := DecodePostings[string](nil); err != nil || len(empty) != 0 {
		t.Errorf("empty decode = %v, %v; want empty and no error", empty, err)
	}
	if _, err := DecodePostings[string](blob[:len(blob)/2]); err == nil {
		t.Error("a truncated blob did not error")
	}
}
