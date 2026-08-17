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
		Records    []connector.Record
		WantPeople int
		WantJoined int
		WantAlias  string // identity expected after the join; empty skips
		WantOn     string // email of the person carrying the alias
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
		WantPeople: 3, WantJoined: 0,
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
			joined := ix.AutoJoin()
			ix.Canonicalize()

			if joined != test.WantJoined {
				t.Errorf("joined = %d, want %d", joined, test.WantJoined)
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
