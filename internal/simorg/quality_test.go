package simorg

import (
	"strings"
	"testing"
)

// Quality floors. These are deliberately below where whodar scores today, so
// the test catches a real regression rather than failing on noise. Raise them
// when ranking genuinely improves; never lower one to make a build pass.
const (
	// minWhoKnowsTop1 is the share of who-knows questions the owner must win
	// outright. Measured at 0.92 to 1.00 across shapes when this was written.
	minWhoKnowsTop1 = 0.85
	// minWhoKnowsTop3 is the share where the owner must at least be visible.
	// Measured at 1.00 across every shape.
	minWhoKnowsTop3 = 0.95
	// minRecallTop1 is the share of recall questions whose own conversation
	// must come back first. Measured at 1.00 over 288 questions.
	minRecallTop1 = 0.95
	// minRecallTop3 is the share where it must be in the first three.
	minRecallTop3 = 0.99
	// minAnchoredTop1 is the share of questions asked with one remembered word
	// and the rest paraphrased that must still find the owner. This is how a
	// person actually asks, so it matters more than the friendly case above.
	minAnchoredTop1 = 0.80
)

// TestRankingQuality scores whodar against a company built with known answers.
// Without this, a ranking change can only be judged by eye on one query; with
// it, a change that helps one question and quietly breaks thirty is visible.
func TestRankingQuality(t *testing.T) {
	t.Parallel()
	built, err := Build(Spec{Seed: 7}, t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Logf("company: %d people, %d channels, %d messages, %d conversations",
		len(built.Index.Graph.People), len(built.Index.Graph.Channels),
		built.Org.Messages, built.Episodes.Len())

	who := built.ScoreWhoKnows(5)
	t.Logf("who knows: %s", who)
	if who.Precision1() < minWhoKnowsTop1 {
		t.Errorf("who-knows p@1 = %.2f, want at least %.2f. Missed: %s",
			who.Precision1(), minWhoKnowsTop1, strings.Join(who.Missed, "; "))
	}
	if who.Precision3() < minWhoKnowsTop3 {
		t.Errorf("who-knows p@3 = %.2f, want at least %.2f", who.Precision3(), minWhoKnowsTop3)
	}

	// How a person actually asks months later, and the hard case beyond it.
	anchored := built.ScoreAnchored(5)
	blind := built.ScoreBlind(5)
	t.Logf("anchored:  %s   (one remembered word, rest in their own words)", anchored)
	t.Logf("blind:     %s   (no shared vocabulary at all)", blind)
	if anchored.Precision1() < minAnchoredTop1 {
		t.Errorf("anchored p@1 = %.2f, want at least %.2f. Missed: %s",
			anchored.Precision1(), minAnchoredTop1, strings.Join(anchored.Missed, "; "))
	}

	rec := built.ScoreRecall(5)
	t.Logf("recall:    %s", rec)
	if rec.Precision1() < minRecallTop1 {
		t.Errorf("recall p@1 = %.2f, want at least %.2f. Missed: %s",
			rec.Precision1(), minRecallTop1, strings.Join(rec.Missed, "; "))
	}
	if rec.Precision3() < minRecallTop3 {
		t.Errorf("recall p@3 = %.2f, want at least %.2f", rec.Precision3(), minRecallTop3)
	}
}

// TestRankingQualityAcrossShapes verifies quality holds as the company changes
// shape, not just at one lucky size. A ranker tuned to a single fixture looks
// fine until the first real org arrives.
func TestRankingQualityAcrossShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Name string
		Spec Spec
	}{
		{Name: "small team", Spec: Spec{People: 12, Channels: 4, Topics: 4, Seed: 1}},
		{Name: "noisy", Spec: Spec{People: 60, Channels: 8, Topics: 8, ChatterPerChannel: 120, Seed: 2}},
		{Name: "many channels", Spec: Spec{People: 80, Channels: 16, Topics: 16, Seed: 3}},
		{Name: "few people many topics", Spec: Spec{People: 16, Channels: 12, Topics: 12, Seed: 4}},
	}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			built, err := Build(test.Spec, t.TempDir())
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			who := built.ScoreWhoKnows(5)
			rec := built.ScoreRecall(5)
			t.Logf("who knows: %s", who)
			t.Logf("recall:    %s", rec)
			if who.Precision3() < minWhoKnowsTop3 {
				t.Errorf("who-knows p@3 = %.2f, want at least %.2f. Missed: %s",
					who.Precision3(), minWhoKnowsTop3, strings.Join(who.Missed, "; "))
			}
			if rec.Precision3() < minRecallTop3 {
				t.Errorf("recall p@3 = %.2f, want at least %.2f. Missed: %s",
					rec.Precision3(), minRecallTop3, strings.Join(rec.Missed, "; "))
			}
		})
	}
}
