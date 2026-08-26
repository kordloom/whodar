package resolve

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// TestBuildDirectory verifies the directory lists people, channels, teams,
// and topics with counts, sorted for browsing.
func TestBuildDirectory(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{{
		Kind: connector.KindPerson, Email: "zoe@x.com", Name: "Zoe Lang",
		Title: "Engineer", Team: "Billing", Org: "Payments",
		Topics: []string{"billing", "retries"}, Source: "t",
	}, {
		Kind: connector.KindPerson, Email: "al@x.com", Name: "Al Ono",
		Team: "Billing", Org: "Payments", Topics: []string{"billing"}, Source: "t",
	}, {
		Kind: connector.KindChannel, Name: "pay-help", Title: "billing questions",
		Members: []string{"zoe@x.com"}, Source: "t",
	}})

	d := BuildDirectory(ix)

	if len(d.People) != 2 || d.People[0].Name != "Al Ono" || d.People[1].Name != "Zoe Lang" {
		t.Errorf("people = %+v, want Al then Zoe by name", d.People)
	}
	if d.People[1].Team != "Billing" || d.People[1].Org != "Payments" || len(d.People[1].Topics) != 2 {
		t.Errorf("zoe row = %+v", d.People[1])
	}
	if len(d.Channels) != 1 || d.Channels[0].Name != "pay-help" || d.Channels[0].Members != 1 {
		t.Errorf("channels = %+v", d.Channels)
	}
	if len(d.Teams) != 1 || d.Teams[0].Name != "Billing" ||
		d.Teams[0].People != 2 || d.Teams[0].Org != "Payments" {
		t.Errorf("teams = %+v", d.Teams)
	}
	// A subject row says who holds the most of it, so the list answers without
	// another click, and how many still work on it, so a subject several people
	// know and nobody touches is visible rather than hidden behind a headcount.
	want := []DirectoryTopic{
		{Name: "billing", People: 2, Lead: "Al Ono"},
		{Name: "retries", People: 1, Lead: "Zoe Lang"},
	}
	if diff := cmp.Diff(want, d.Topics); diff != "" {
		t.Errorf("topics mismatch (-want +got):\n%s", diff)
	}
}
