package vector

import (
	"math"
	"testing"
)

// TestCosine verifies the similarity for aligned, orthogonal, opposite, and
// degenerate inputs. Every semantic and recall ranking in the program depends
// on this function, so a silent error here would corrupt all of them.
func TestCosine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Name string
		A    []float32
		B    []float32
		Want float64
	}{
		{"identical", []float32{1, 2, 3}, []float32{1, 2, 3}, 1},
		{"parallel", []float32{1, 0}, []float32{2, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"empty a", []float32{}, []float32{1, 2}, 0},
		{"length mismatch", []float32{1, 2, 3}, []float32{1, 2}, 0},
		{"zero vector", []float32{0, 0}, []float32{1, 1}, 0},
		{"both nil", nil, nil, 0},
	}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			got := Cosine(test.A, test.B)
			if math.Abs(got-test.Want) > 1e-9 {
				t.Errorf("Cosine(%v, %v) = %v, want %v", test.A, test.B, got, test.Want)
			}
		})
	}
}
