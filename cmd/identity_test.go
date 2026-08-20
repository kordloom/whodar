package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// joinFixture builds an index whose AutoJoin infers a unique-name join and a
// team-corroborated join, for the identity view and renderer tests.
func joinFixture(t *testing.T) *index.Index {
	t.Helper()
	ix := index.New()
	ix.Build([]connector.Record{
		{Kind: connector.KindPerson, Name: "Kevin Novak", Email: "kevin@corp.com", Team: "Payments", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "github:kevinnovak", Name: "@kevinnovak", Source: "github"},
		{Kind: connector.KindPerson, Name: "Angela Malone", Email: "angela@corp.com", Team: "Payments", Source: "t"},
		{Kind: connector.KindPerson, Name: "Angela Malone", Email: "angela@sales.com", Team: "Sales", Source: "t"},
		{Kind: connector.KindPerson, PersonID: "pagerduty:angela-malone", Name: "@angela-malone", Team: "Payments", Source: "pagerduty"},
	})
	ix.AutoJoin()
	ix.Canonicalize()
	return ix
}

// TestBuildIdentityView checks the view groups joins by person with the right
// confidence and evidence, in name order.
func TestBuildIdentityView(t *testing.T) {
	t.Parallel()
	v := buildIdentityView(joinFixture(t))
	if v.Merges != 2 {
		t.Fatalf("merges = %d, want 2", v.Merges)
	}
	if len(v.People) != 2 || v.People[0].Name != "Angela Malone" || v.People[1].Name != "Kevin Novak" {
		t.Fatalf("people = %+v, want Angela then Kevin", v.People)
	}
	kevin := v.People[1]
	if len(kevin.Joins) != 1 || kevin.Joins[0].Alias != "github:kevinnovak" ||
		kevin.Joins[0].Confidence != 0.9 || kevin.Joins[0].Reason != "unique name match" {
		t.Errorf("kevin joins = %+v, want the github unique-name join at 0.9", kevin.Joins)
	}
}

// TestRenderIdentity checks the human renderer shows the header, evidence, and
// aliases, and the clear message when there is nothing to show.
func TestRenderIdentity(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderIdentity(&buf, buildIdentityView(joinFixture(t)), style{})
	out := buf.String()
	for _, want := range []string{"Identity joins", "2 inferred across 2 people",
		"github:kevinnovak", "unique name match", "pagerduty:angela-malone", "name and shared team"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	var empty bytes.Buffer
	renderIdentity(&empty, identityView{}, style{})
	if !strings.Contains(empty.String(), "No inferred identity merges") {
		t.Errorf("empty output = %q, want the no-merges message", empty.String())
	}
}
