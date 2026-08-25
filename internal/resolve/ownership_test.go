package resolve

import (
	"fmt"
	"slices"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/index"
)

// TestOwnershipDrift checks that a CODEOWNERS-declared owner who is not the
// strongest actual expert is reported as drift, and that a declared owner who is
// the top expert is not.
func TestOwnershipDrift(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		// Alice owns payments on paper (CODEOWNERS), but does little of the work.
		{Kind: connector.KindPerson, Name: "Alice", Topics: []string{"payments"}, Source: "codeowners"},
		// Bob actually does the payments work.
		{Kind: connector.KindPerson, Name: "Bob", Email: "bob@x.com", Topics: []string{"payments", "payments", "payments"}, Source: "slack"},
		// Carol owns and does the auth work: no drift there.
		{Kind: connector.KindPerson, Name: "Carol", Email: "carol@x.com", Topics: []string{"auth"}, Source: "codeowners"},
		{Kind: connector.KindPerson, Name: "Carol", Email: "carol@x.com", Topics: []string{"auth", "auth"}, Source: "slack"},
	})
	ix.Canonicalize()

	report := Ownership(ix)
	drift := report.Drift
	if len(drift) != 1 {
		t.Fatalf("drift = %+v, want exactly payments", drift)
	}
	d := drift[0]
	if d.Topic != "payments" || d.Actual != "Bob" || !slices.Equal(d.Declared, []string{"Alice"}) {
		t.Errorf("drift = %+v, want payments declared [Alice] actual Bob", d)
	}
}

// TestDriftIgnoresThePersonWhoTouchesEverything checks an area is reported as
// moving to whoever really works on it, not to the busiest person in the
// organization. Every code base has a few people who touch everything, and by
// raw share they out-hold the declared owner of nearly every area at once.
// Reporting that as a thousand ownership changes says nothing about ownership.
func TestDriftIgnoresThePersonWhoTouchesEverything(t *testing.T) {
	t.Parallel()
	recs := []connector.Record{
		// The owner of record, who has stopped doing the work.
		{Kind: connector.KindPerson, Name: "Alice", Email: "alice@x.com",
			Topics: []string{"payments"}, Source: "codeowners"},
		// The person the area has actually moved to: it is most of what they do.
		{Kind: connector.KindPerson, Name: "Bob", Email: "bob@x.com",
			Topics: []string{"payments", "payments", "payments", "payments"}, Source: "git"},
	}
	// And somebody who touches every area in the company, including this one,
	// slightly harder than Bob does.
	sweeper := []string{"payments", "payments", "payments", "payments", "payments"}
	for i := range 60 {
		sweeper = append(sweeper, fmt.Sprintf("area%d", i), fmt.Sprintf("area%d", i))
	}
	recs = append(recs, connector.Record{
		Kind: connector.KindPerson, Name: "Sweeper", Email: "sweeper@x.com",
		Topics: sweeper, Source: "git",
	})

	ix := index.New()
	ix.Build(recs)
	ix.Canonicalize()

	report := Ownership(ix)
	var payments *OwnerDrift
	for i := range report.Drift {
		if report.Drift[i].Topic == "payments" {
			payments = &report.Drift[i]
		}
	}
	if payments == nil {
		t.Fatalf("payments was not reported as drifted: %+v", report.Drift)
	}
	if payments.Actual != "Bob" {
		t.Errorf("actual owner = %q, want Bob, who does this work rather than all work", payments.Actual)
	}
}

// TestOwnershipSplitsTheThreeWaysAnOwnerCanDrift checks the report separates
// what a reader has to do something different about: an owner who has gone,
// one who owns an area on paper only, and one who is simply out-worked in their
// own area.
func TestOwnershipSplitsTheThreeWaysAnOwnerCanDrift(t *testing.T) {
	t.Parallel()
	ix := index.New()
	ix.Build([]connector.Record{
		// Declared owners, from a source of record. This assigns them their
		// areas without any of them having done a thing.
		{Kind: connector.KindPerson, Name: "Gone", Email: "gone@x.com",
			Topics: []string{"alpha"}, Source: "codeowners"},
		{Kind: connector.KindPerson, Name: "Elsewhere", Email: "elsewhere@x.com",
			Topics: []string{"beta"}, Source: "codeowners"},
		{Kind: connector.KindPerson, Name: "Trailing", Email: "trailing@x.com",
			Topics: []string{"gamma"}, Source: "codeowners"},
		// Elsewhere works, just never on what they own.
		{Kind: connector.KindPerson, Name: "Elsewhere", Email: "elsewhere@x.com",
			Topics: []string{"delta", "delta"}, Source: "git"},
		// Trailing does work on their own area, but less than Rival.
		{Kind: connector.KindPerson, Name: "Trailing", Email: "trailing@x.com",
			Topics: []string{"gamma"}, Source: "git"},
		// The people actually doing each area.
		{Kind: connector.KindPerson, Name: "Rival", Email: "rival@x.com",
			Topics: []string{"alpha", "alpha", "beta", "beta", "gamma", "gamma", "gamma"}, Source: "git"},
	})
	ix.Canonicalize()

	report := Ownership(ix)
	if report.Silent != 1 || report.Unworked != 1 || report.Trailing != 1 {
		t.Errorf("split = silent %d, unworked %d, trailing %d; want one of each (drift %+v)",
			report.Silent, report.Unworked, report.Trailing, report.Drift)
	}
	if report.Silent+report.Unworked+report.Trailing != len(report.Drift) {
		t.Errorf("the three buckets total %d but %d areas drifted",
			report.Silent+report.Unworked+report.Trailing, len(report.Drift))
	}
}
