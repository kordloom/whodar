package fact

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestFactValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Fact Fact
		Want error
	}{{ // Test 0: A known relation with subject and object is valid.
		Fact: Fact{Subject: "team:pay", Relation: "not_owned_by", Object: "svc:checkout"},
		Want: nil,
	}, { // Test 1: An unknown relation is rejected.
		Fact: Fact{Subject: "team:pay", Relation: "owns", Object: "svc:checkout"},
		Want: ErrBadFact,
	}, { // Test 2: An empty subject is rejected.
		Fact: Fact{Subject: "", Relation: "owned_by", Object: "svc:checkout"},
		Want: ErrBadFact,
	}, { // Test 3: An empty object is rejected.
		Fact: Fact{Subject: "team:pay", Relation: "owned_by", Object: ""},
		Want: ErrBadFact,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			if err := test.Fact.Valid(); !errors.Is(err, test.Want) {
				t.Errorf("Valid() = %v, want %v", err, test.Want)
			}
		})
	}
}

func TestStoreAddListForget(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "facts.json")
	store, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	adds := []Fact{
		{Subject: "team:pay", Relation: "owned_by", Object: "svc:checkout", Source: "catalog"},
		{Subject: "team:pay", Relation: "not_owned_by", Object: "svc:refunds", Source: "curated"},
		{Subject: "team:ship", Relation: "owned_by", Object: "svc:labels", Source: "catalog"},
	}
	for _, f := range adds {
		if err := store.Add(f); err != nil {
			t.Fatalf("add %v: %v", f, err)
		}
	}
	if got := len(store.List(Filter{})); got != 3 {
		t.Fatalf("list all = %d, want 3", got)
	}
	if got := len(store.List(Filter{Subject: "team:pay"})); got != 2 {
		t.Errorf("list team:pay = %d, want 2", got)
	}
	if got := len(store.List(Filter{Source: "catalog"})); got != 2 {
		t.Errorf("list source catalog = %d, want 2", got)
	}
	// An empty filter forgets nothing.
	if n, _ := store.Forget(Filter{}); n != 0 {
		t.Errorf("forget empty = %d, want 0", n)
	}
	// Forget a whole source.
	if n, err := store.Forget(Filter{Source: "catalog"}); err != nil || n != 2 {
		t.Errorf("forget source catalog = %d, %v, want 2, nil", n, err)
	}
	// The reloaded store reflects the forget.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(reloaded.List(Filter{})); got != 1 {
		t.Errorf("after forget, reloaded list = %d, want 1", got)
	}
}

func TestStoreImportReplace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "facts.json")
	store, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	first := `[{"subject":"team:pay","relation":"owned_by","object":"svc:a"},
	           {"subject":"team:pay","relation":"owned_by","object":"svc:b"}]`
	if n, err := store.Import(strings.NewReader(first), "catalog"); err != nil || n != 2 {
		t.Fatalf("import first = %d, %v, want 2, nil", n, err)
	}
	// Re-importing the source replaces its facts rather than appending.
	second := `[{"subject":"team:pay","relation":"owned_by","object":"svc:c"}]`
	if n, err := store.Import(strings.NewReader(second), "catalog"); err != nil || n != 1 {
		t.Fatalf("import second = %d, %v, want 1, nil", n, err)
	}
	if got := len(store.List(Filter{Source: "catalog"})); got != 1 {
		t.Errorf("catalog facts after replace = %d, want 1", got)
	}
	// An import with no replace source appends and defaults nothing away.
	third := `[{"subject":"team:ship","relation":"runs_on","object":"cloud:aws","source":"curated"}]`
	if n, err := store.Import(strings.NewReader(third), ""); err != nil || n != 1 {
		t.Fatalf("import third = %d, %v, want 1, nil", n, err)
	}
	if got := len(store.List(Filter{})); got != 2 {
		t.Errorf("total after appends = %d, want 2", got)
	}
}
