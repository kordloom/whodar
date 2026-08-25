package resolve

import (
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// TestSoleSpansFindOnePersonConnections checks whodar reports a connection
// between two subjects that only one person has ever made. Counting experts per
// subject cannot find these: both areas are well covered, and what rests on one
// person is not either subject but the knowledge that they belong together.
func TestSoleSpansFindOnePersonConnections(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		// Both subjects have several experts each.
		{Kind: connector.KindPerson, Name: "Ada", Email: "ada@x.com",
			Topics: []string{"billing", "billing"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Bo", Email: "bo@x.com",
			Topics: []string{"billing"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Cy", Email: "cy@x.com",
			Topics: []string{"ledger", "ledger"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Di", Email: "di@x.com",
			Topics: []string{"ledger"}, Source: "git"},
		// But only one person has ever worked across the two.
		{Kind: connector.KindPerson, Name: "Bridge", Email: "bridge@x.com",
			Topics: []string{"billing", "ledger"}, Source: "git"},
		{Kind: connector.KindTopic, Name: "billing", Source: "git",
			Links: []connector.TopicLink{{To: "ledger", Weight: 0.5, Witnesses: 1, Sole: "bridge@x.com"}}},
		// And a connection several people have made, which is not a finding.
		{Kind: connector.KindTopic, Name: "ledger", Source: "git",
			Links: []connector.TopicLink{{To: "billing", Weight: 0.5, Witnesses: 1, Sole: "bridge@x.com"},
				{To: "payroll", Weight: 0.4, Witnesses: 4}}},
		{Kind: connector.KindPerson, Name: "Ez", Email: "ez@x.com",
			Topics: []string{"payroll", "payroll"}, Source: "git"},
	})
	ix.Canonicalize()

	got := SoleSpans(ix, 0)
	if len(got) != 1 {
		t.Fatalf("spans = %+v, want only the connection one person has made", got)
	}
	if got[0].Topic != "billing" || got[0].With != "ledger" {
		t.Errorf("span = %s + %s, want billing + ledger", got[0].Topic, got[0].With)
	}
	if got[0].Person != "Bridge" {
		t.Errorf("person = %q, want the one who has worked across both", got[0].Person)
	}
	// The point of the finding is that the subjects are not short of experts.
	if got[0].Experts < 4 {
		t.Errorf("experts = %d, want the people holding either subject counted", got[0].Experts)
	}
}

// TestSoleSpansIgnoreUnestablishedSubjects checks a tie to something nobody
// holds is not reported. Being changed alongside a subject does not make a word
// into one, and a finding about a word helps nobody.
func TestSoleSpansIgnoreUnestablishedSubjects(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		{Kind: connector.KindPerson, Name: "Ada", Email: "ada@x.com",
			Topics: []string{"billing"}, Source: "git"},
		{Kind: connector.KindTopic, Name: "billing", Source: "git",
			Links: []connector.TopicLink{{To: "scratchpad", Weight: 0.9, Witnesses: 1, Sole: "ada@x.com"}}},
	})
	ix.Canonicalize()
	if got := SoleSpans(ix, 0); len(got) != 0 {
		t.Errorf("spans = %+v, want nothing: scratchpad is not a subject anybody holds", got)
	}
}
