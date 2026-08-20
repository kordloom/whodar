package resolve

import (
	"slices"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// searchFixture builds a small index across people and a channel for the search
// tests: two Payments engineers who know billing, a designer, and a channel.
func searchFixture() *index.Index {
	ix := index.New()
	ix.Build([]connector.Record{
		{Kind: connector.KindPerson, Name: "Kevin Novak", Email: "kevin@corp.com", Title: "Software Engineer", Team: "Payments", Topics: []string{"retries", "billing"}, Source: "t"},
		{Kind: connector.KindPerson, Name: "Angela Malone", Email: "angela@corp.com", Title: "Staff Engineer", Team: "Payments", Topics: []string{"billing"}, Source: "t"},
		{Kind: connector.KindPerson, Name: "Dana Reed", Email: "dana@corp.com", Title: "Designer", Team: "Design", Source: "t"},
		{Kind: connector.KindChannel, Name: "payments-help", Title: "billing questions", Source: "t"},
	})
	ix.Canonicalize()
	return ix
}

// idSet lists the result ids for presence checks.
func idSet(rs []SearchResult) map[string]bool {
	out := make(map[string]bool, len(rs))
	for _, r := range rs {
		out[r.ID] = true
	}
	return out
}

// TestSearch checks name, topic, team, and channel matches, ranking, empties,
// and the limit.
func TestSearch(t *testing.T) {
	t.Parallel()
	ix := searchFixture()

	// A name query ranks the person first, matched on name.
	kev := Search(ix, "kevin", 0)
	if len(kev) != 1 || kev[0].ID != "kevin@corp.com" || !slices.Contains(kev[0].Matched, "name") {
		t.Fatalf("search kevin = %+v, want the person matched on name", kev)
	}

	// A topic query finds people by topic and the channel by its topic.
	bill := idSet(Search(ix, "billing", 0))
	for _, want := range []string{"kevin@corp.com", "angela@corp.com", "payments-help"} {
		if !bill[want] {
			t.Errorf("search billing missing %s: %v", want, bill)
		}
	}

	// A team query finds the two Payments people and the channel by its name.
	if pay := Search(ix, "payments", 0); len(pay) != 3 {
		t.Errorf("search payments = %d results, want 3", len(pay))
	}

	// No match is empty, and the limit caps results.
	if r := Search(ix, "zzzz", 0); len(r) != 0 {
		t.Errorf("search zzzz = %+v, want empty", r)
	}
	if r := Search(ix, "e", 1); len(r) > 1 {
		t.Errorf("limit not applied: %d results", len(r))
	}
}

// TestProfileViewJoins checks the profile view carries the inferred identity
// merges with their confidence.
func TestProfileViewJoins(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		{Kind: connector.KindPerson, Name: "Kevin Novak", Email: "kevin@corp.com", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "github:kevinnovak", Name: "@kevinnovak", Source: "github"},
	})
	ix.AutoJoin()
	ix.Canonicalize()
	profile, ok := ix.Profile("kevin@corp.com")
	if !ok {
		t.Fatal("profile not found")
	}
	view := ProfileView(profile)
	if len(view.Joins) != 1 || view.Joins[0].Alias != "github:kevinnovak" || view.Joins[0].Confidence != 0.9 {
		t.Errorf("view.Joins = %+v, want the github join at 0.9", view.Joins)
	}
}
