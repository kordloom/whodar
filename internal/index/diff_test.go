package index

import (
	"slices"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
)

// TestDiff verifies joiners, leavers, and channel changes are detected.
func TestDiff(t *testing.T) {
	t.Parallel()
	prev := New()
	prev.Build([]connector.Record{
		{Name: "Jane Roe", Email: "jane@x.com", Team: "Billing"},
		{Name: "Old Guy", Email: "old@x.com", Team: "Legacy"},
		{Kind: connector.KindChannel, Name: "oldchan"},
	})
	cur := New()
	cur.Build([]connector.Record{
		{Name: "Jane Roe", Email: "jane@x.com", Team: "Billing"},
		{Name: "New Person", Email: "new@x.com", Team: "Billing"},
		{Kind: connector.KindChannel, Name: "newchan"},
	})

	c := Diff(prev.Graph, cur.Graph)
	if !slices.Contains(c.PeopleJoined, "New Person") {
		t.Errorf("PeopleJoined = %v, want New Person", c.PeopleJoined)
	}
	if !slices.Contains(c.PeopleLeft, "Old Guy") {
		t.Errorf("PeopleLeft = %v, want Old Guy", c.PeopleLeft)
	}
	if !slices.Contains(c.ChannelsAdded, "newchan") || !slices.Contains(c.ChannelsRemoved, "oldchan") {
		t.Errorf("channels added=%v removed=%v", c.ChannelsAdded, c.ChannelsRemoved)
	}
	if !slices.Contains(c.TeamsRemoved, "Legacy") {
		t.Errorf("TeamsRemoved = %v, want Legacy", c.TeamsRemoved)
	}
	if c.Empty() {
		t.Error("changes should not be empty")
	}
}

// TestDiffEmpty verifies an unchanged graph reports no changes.
func TestDiffEmpty(t *testing.T) {
	t.Parallel()
	ix := New()
	ix.Build([]connector.Record{{Name: "A", Email: "a@x.com"}})
	if !Diff(ix.Graph, ix.Graph).Empty() {
		t.Error("identical graph should diff to empty")
	}
}
