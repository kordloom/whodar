package resolve

import (
	"slices"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// TestOwnershipDrift checks that a CODEOWNERS-declared owner who is not the
// strongest actual expert is reported as drift, and that a declared owner who is
// the top expert is not.
func TestOwnershipDrift(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		// Alice owns payments on paper (CODEOWNERS), but does little of the work.
		{Kind: connector.KindPerson, Name: "Alice", Topics: []string{"payments"}, Source: "codeowners"},
		// Bob actually does the payments work.
		{Kind: connector.KindPerson, Name: "Bob", Email: "bob@x.com", Topics: []string{"payments", "payments", "payments"}, Source: "slack"},
		// Carol owns and does the auth work: no drift there.
		{Kind: connector.KindPerson, Name: "Carol", Email: "carol@x.com", Topics: []string{"auth"}, Source: "codeowners"},
		{Kind: connector.KindPerson, Name: "Carol", Email: "carol@x.com", Topics: []string{"auth", "auth"}, Source: "slack"},
	})
	ix.Canonicalize()

	drift := OwnershipDrift(ix)
	if len(drift) != 1 {
		t.Fatalf("drift = %+v, want exactly payments", drift)
	}
	d := drift[0]
	if d.Topic != "payments" || d.Actual != "Bob" || !slices.Equal(d.Declared, []string{"Alice"}) {
		t.Errorf("drift = %+v, want payments declared [Alice] actual Bob", d)
	}
}
