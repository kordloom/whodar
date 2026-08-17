package resolve

import (
	"context"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// TestKeywordResolve verifies the keyword resolver returns ranked matches.
func TestKeywordResolve(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		{Name: "Jane Roe", Email: "jane@x.com", Title: "Engineer",
			Team: "Billing", Topics: []string{"retries"}},
	})

	got, err := NewKeyword(ix).Resolve(context.Background(), "retries", 5)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.People) != 1 || got.People[0].Person.Email != "jane@x.com" {
		t.Fatalf("got %v, want one person match for jane@x.com", got.People)
	}
}

// TestNewKeywordNil verifies the constructor panics on a nil index.
func TestNewKeywordNil(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("NewKeyword(nil) did not panic")
		}
	}()
	NewKeyword(nil)
}
