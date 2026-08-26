package resolve

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// TestOrgChartSeatsThePeopleASourcePlaced checks the chart is built from the
// stated management chain, carries what whodar learned on each seat, and leaves
// out the people no source placed rather than promoting them to the top.
func TestOrgChartSeatsThePeopleASourcePlaced(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Graph.Teams = map[model.ID]*model.Team{"t1": {ID: "t1", Name: "Payments"}}
	ix.Graph.Topics = map[model.ID]*model.Topic{
		"billing": {ID: "billing", Name: "billing", Curated: true},
		"ledger":  {ID: "ledger", Name: "ledger", Curated: true},
	}
	ix.Graph.People = map[model.ID]*model.Person{
		"boss@x.com": {ID: "boss@x.com", Name: "Boss", Title: "VP"},
		"ada@x.com": {
			ID: "ada@x.com", Name: "Ada", Title: "Engineer", TeamID: "t1",
			ManagerID: "boss@x.com",
			Topics:    map[model.ID]float64{"billing": 9, "ledger": 3},
			// Ada still knows ledger and has stopped touching it.
			Recent: map[model.ID]float64{"billing": 4},
		},
		"bo@x.com": {
			ID: "bo@x.com", Name: "Bo", ManagerID: "boss@x.com",
			Topics: map[model.ID]float64{"billing": 2},
			Recent: map[model.ID]float64{"billing": 2},
		},
		// Nobody placed this person and nobody reports to them.
		"loose@x.com": {ID: "loose@x.com", Name: "Loose"},
	}

	got := OrgChart(ix)
	if len(got.Roots) != 1 {
		t.Fatalf("roots = %d, want the one person nobody reports to", len(got.Roots))
	}
	if got.People != 3 || got.Unplaced != 1 {
		t.Errorf("people = %d, unplaced = %d; want 3 seated and 1 left out",
			got.People, got.Unplaced)
	}
	root := got.Roots[0]
	if root.Name != "Boss" || len(root.Reports) != 2 {
		t.Fatalf("root = %+v, want Boss with two reports", root)
	}
	// Reports come back by name, so the chart is stable to redraw.
	var names []string
	for _, r := range root.Reports {
		names = append(names, r.Name)
	}
	if diff := cmp.Diff([]string{"Ada", "Bo"}, names, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("reports (-want +got):\n%s", diff)
	}

	ada := root.Reports[0]
	if ada.Team != "Payments" {
		t.Errorf("team = %q, want the team name and not its id", ada.Team)
	}
	if diff := cmp.Diff([]string{"billing", "ledger"}, ada.Knows, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("knows (-want +got):\n%s", diff)
	}
	// Only Ada holds ledger, so it leaves with her.
	if diff := cmp.Diff([]string{"ledger"}, ada.Alone, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("alone (-want +got):\n%s", diff)
	}
	// She knows ledger and has stopped working on it.
	if diff := cmp.Diff([]string{"ledger"}, ada.Quiet, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("quiet (-want +got):\n%s", diff)
	}
}

// TestOrgChartSurvivesACycle checks a management chain that loops does not
// recurse forever. A hand-maintained directory eventually contains one.
func TestOrgChartSurvivesACycle(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Graph.People = map[model.ID]*model.Person{
		"a@x.com": {ID: "a@x.com", Name: "A", ManagerID: "b@x.com"},
		"b@x.com": {ID: "b@x.com", Name: "B", ManagerID: "a@x.com"},
	}
	// Neither is a root, so the chart is empty rather than hanging.
	got := OrgChart(ix)
	if len(got.Roots) != 0 {
		t.Errorf("roots = %+v, want none: a loop places nobody at the top", got.Roots)
	}
}
