package index

import (
	"fmt"
	"slices"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/model"
)

// TestAutoJoin covers the unique-match join, the ambiguity guard, the
// no-match case, and short handles.
func TestAutoJoin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Records       []connector.Record
		WantAmbiguous []string // handle ids left unresolved by a name collision
		WantPeople    int
		WantJoined    int
		WantAlias     string // identity expected after the join; empty skips
		WantOn        string // email of the person carrying the alias
	}{{ // Test 0: A handle joins its unique person by flattened name.
		Records: []connector.Record{
			{Kind: connector.KindPerson, Email: "kim.doe@x.com", Name: "Kim Doe", Source: "t"},
			{Kind: connector.KindPerson, PersonID: "codeowners:kim-doe", Name: "@kim-doe", Source: "t"},
		},
		WantPeople: 1, WantJoined: 1, WantAlias: "codeowners:kim-doe", WantOn: "kim.doe@x.com",
	}, { // Test 1: A handle matching the email local-part joins too.
		Records: []connector.Record{
			{Kind: connector.KindPerson, Email: "kim.doe@x.com", Source: "t"},
			{Kind: connector.KindPerson, PersonID: "github:kimdoe", Name: "@kimdoe", Source: "t"},
		},
		WantPeople: 1, WantJoined: 1, WantAlias: "github:kimdoe", WantOn: "kim.doe@x.com",
	}, { // Test 2: Two candidates with the same flattened name block the join.
		Records: []connector.Record{
			{Kind: connector.KindPerson, Email: "kim.doe@x.com", Name: "Kim Doe", Source: "t"},
			{Kind: connector.KindPerson, Email: "kdoe@y.com", Name: "Kim-Doe", Source: "t"},
			{Kind: connector.KindPerson, PersonID: "codeowners:kim-doe", Name: "@kim-doe", Source: "t"},
		},
		WantPeople: 3, WantJoined: 0, WantAmbiguous: []string{"codeowners:kim-doe"},
	}, { // Test 3: A handle matching nobody stays separate.
		Records: []connector.Record{
			{Kind: connector.KindPerson, Email: "kim.doe@x.com", Name: "Kim Doe", Source: "t"},
			{Kind: connector.KindPerson, PersonID: "github:eve-dev", Name: "@eve-dev", Source: "t"},
		},
		WantPeople: 2, WantJoined: 0,
	}, { // Test 4: A too-short handle never joins.
		Records: []connector.Record{
			{Kind: connector.KindPerson, Email: "al@x.com", Name: "Al", Source: "t"},
			{Kind: connector.KindPerson, PersonID: "github:al", Name: "@al", Source: "t"},
		},
		WantPeople: 2, WantJoined: 0,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			ix := New()
			ix.Build(test.Records)
			res := ix.AutoJoin()
			joined := res.Joined
			ix.Canonicalize()

			if joined != test.WantJoined {
				t.Errorf("joined = %d, want %d", joined, test.WantJoined)
			}
			if !slices.Equal(res.Ambiguous, test.WantAmbiguous) {
				t.Errorf("ambiguous = %v, want %v", res.Ambiguous, test.WantAmbiguous)
			}
			if len(ix.Graph.People) != test.WantPeople {
				t.Errorf("people = %d, want %d: %v",
					len(ix.Graph.People), test.WantPeople, peopleIDs(ix))
			}
			if test.WantAlias == "" {
				return
			}
			var joinedPerson *model.Person
			for _, p := range ix.Graph.People {
				if p.Email == test.WantOn {
					joinedPerson = p
					break
				}
			}
			if joinedPerson == nil {
				t.Fatalf("person %s missing after join: %v", test.WantOn, peopleIDs(ix))
			}
			if !slices.Contains(joinedPerson.Identities, model.ID(test.WantAlias)) {
				t.Errorf("identities = %v, want containing %s", joinedPerson.Identities, test.WantAlias)
			}
		})
	}
}

// peopleIDs lists the graph's person ids for failure messages.
func peopleIDs(ix *Index) []model.ID {
	out := make([]model.ID, 0, len(ix.Graph.People))
	for id := range ix.Graph.People {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// TestAutoJoinCorroboration checks that an ambiguous handle (two people share a
// flattened name) merges into the one it corroborates with via a shared team.
func TestAutoJoinCorroboration(t *testing.T) {
	t.Parallel()
	recs := []connector.Record{
		{Kind: connector.KindPerson, Email: "alice@x.com", Name: "Alice Smith", Team: "Payments", Source: "t"},
		{Kind: connector.KindPerson, Email: "alice@y.com", Name: "Alice Smith", Team: "Sales", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "github:alice-smith", Name: "@alice-smith", Team: "Payments", Source: "t"},
	}
	ix := New()
	ix.Build(recs)
	res := ix.AutoJoin()
	ix.Canonicalize()
	if res.Joined != 1 {
		t.Errorf("joined = %d, want 1 (corroborated by shared team)", res.Joined)
	}
	if len(ix.Graph.People) != 2 {
		t.Errorf("people = %d, want 2: %v", len(ix.Graph.People), peopleIDs(ix))
	}
}

// TestAutoJoinAmbiguousNoCorroboration checks that an ambiguous handle with no
// shared signal stays unresolved and reported, never silently merged.
func TestAutoJoinAmbiguousNoCorroboration(t *testing.T) {
	t.Parallel()
	recs := []connector.Record{
		{Kind: connector.KindPerson, Email: "alice@x.com", Name: "Alice Smith", Team: "Payments", Source: "t"},
		{Kind: connector.KindPerson, Email: "alice@y.com", Name: "Alice Smith", Team: "Sales", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "github:alice-smith", Name: "@alice-smith", Team: "Marketing", Source: "t"},
	}
	ix := New()
	ix.Build(recs)
	res := ix.AutoJoin()
	if res.Joined != 0 {
		t.Errorf("joined = %d, want 0 (no corroboration)", res.Joined)
	}
	if len(res.Ambiguous) != 1 {
		t.Errorf("ambiguous = %v, want exactly the blocked handle", res.Ambiguous)
	}
}

// TestAutoJoinConfidence checks that each inferred join carries a confidence and
// evidence: a unique name match is strong, a team-corroborated ambiguous match
// is weaker, and JoinsFor surfaces the join under the person it merged into.
func TestAutoJoinConfidence(t *testing.T) {
	t.Parallel()

	// A unique name match scores confUniqueName.
	ix := New()
	ix.Build([]connector.Record{
		{Kind: connector.KindPerson, Email: "kim.doe@x.com", Name: "Kim Doe", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "github:kimdoe", Name: "@kimdoe", Source: "t"},
	})
	res := ix.AutoJoin()
	ix.Canonicalize()
	if len(res.Joins) != 1 {
		t.Fatalf("joins = %d, want 1", len(res.Joins))
	}
	if got := res.Joins[0]; got.Confidence != confUniqueName || got.Reason != "unique name match" {
		t.Errorf("join = %+v, want confidence %v reason %q", got, confUniqueName, "unique name match")
	}
	if fr := ix.JoinsFor("kim.doe@x.com"); len(fr) != 1 || fr[0].Alias != "github:kimdoe" {
		t.Errorf("JoinsFor = %+v, want the github alias", fr)
	}

	// A team-corroborated ambiguous match scores confSharedTeam with team evidence.
	ix2 := New()
	ix2.Build([]connector.Record{
		{Kind: connector.KindPerson, Email: "alice@x.com", Name: "Alice Smith", Team: "Payments", Source: "t"},
		{Kind: connector.KindPerson, Email: "alice@y.com", Name: "Alice Smith", Team: "Sales", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "github:alice-smith", Name: "@alice-smith", Team: "Payments", Source: "t"},
	})
	res2 := ix2.AutoJoin()
	ix2.Canonicalize()
	if len(res2.Joins) != 1 {
		t.Fatalf("joins = %d, want 1", len(res2.Joins))
	}
	if got := res2.Joins[0]; got.Confidence != confSharedTeam || got.Reason != "name and shared team" {
		t.Errorf("join = %+v, want confidence %v reason %q", got, confSharedTeam, "name and shared team")
	}
}
