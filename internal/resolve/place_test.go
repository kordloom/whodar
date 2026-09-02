package resolve

import (
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// TestPlaceLeadsDiscountsBreadth verifies the place model's whole reason to
// exist: a sweeper with more raw work in a directory ranks below the focused
// owner, identities fold to their canonical person, and quiet directories are
// dropped.
func TestPlaceLeadsDiscountsBreadth(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		{Kind: connector.KindPerson, Name: "Owner", Email: "owner@x.com",
			Topics: []string{"billing"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Sweeper", Email: "sweep@x.com",
			Topics: []string{"billing"}, Source: "git"},
	})
	ix.Canonicalize()

	dirWork := map[string]map[string]float64{
		"billing": {"owner@x.com": 30, "sweep@x.com": 40},
		"quiet":   {"owner@x.com": 1},
	}
	totals := map[string]float64{"owner@x.com": 35, "sweep@x.com": 900}

	places := PlaceLeads(ix, dirWork, totals, 10, 3)
	if len(places) != 1 || places[0].Dir != "billing" {
		t.Fatalf("places = %+v, want billing alone; quiet is below the floor", places)
	}
	holders := places[0].Holders
	if len(holders) != 2 || holders[0].Name != "Owner" {
		t.Errorf("holders = %+v, want Owner first: 40 commits from someone with 900 "+
			"everywhere is less ownership than 30 from someone with 35", holders)
	}
	if holders[0].ID == "" {
		t.Error("holder has no canonical id")
	}
}

// TestFuseRanksKeepsOrphanSignal verifies a person only one ranking can see
// still surfaces, which is the property review-only approvers depend on.
func TestFuseRanksKeepsOrphanSignal(t *testing.T) {
	t.Parallel()
	got := FuseRanks(3,
		[]string{"CommitHeavy", "CommitMid", "CommitLow"},
		[]string{"ReviewOnly", "CommitHeavy"},
	)
	if got[0] != "CommitHeavy" {
		t.Errorf("fused[0] = %q, want the person both signals agree on", got[0])
	}
	found := false
	for _, n := range got {
		if n == "ReviewOnly" {
			found = true
		}
	}
	if !found {
		t.Errorf("fused = %v; the review-only person was crowded out", got)
	}
}

// TestAddReviewCredit verifies review participation lands on the places a
// pull request changed rather than on the words of its title, and that a
// reviewer who never commits can therefore hold a directory.
func TestAddReviewCredit(t *testing.T) {
	t.Parallel()
	dirWork := map[string]map[string]float64{
		"pkg/scheduler": {"author@x.com": 20},
	}
	totals := map[string]float64{"author@x.com": 400}
	pullDirs := map[int][]string{7: {"pkg/scheduler"}, 9: {"pkg/other"}}
	pullPeople := map[int][]string{7: {"reviewer-only"}, 8: {"nobody"}}

	AddReviewCredit(dirWork, totals, pullDirs, pullPeople)

	got := dirWork["pkg/scheduler"]["github:reviewer-only"]
	if got != reviewWeight {
		t.Errorf("scheduler review credit = %v, want %v", got, reviewWeight)
	}
	if totals["github:reviewer-only"] != reviewWeight {
		t.Errorf("reviewer breadth = %v, want it counted", totals["github:reviewer-only"])
	}
	if _, ok := dirWork["pkg/other"]["github:nobody"]; ok {
		t.Error("a pull request with no recorded participants credited somebody")
	}
	if dirWork["pkg/scheduler"]["author@x.com"] != 20 {
		t.Error("existing commit work was disturbed")
	}
}
