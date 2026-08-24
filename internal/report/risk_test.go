package report

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/kordloom/whodar/internal/resolve"
)

// TestExposures checks per-topic risk is read back as per-person risk, which is
// the form the question gets asked in. A subject with one expert is theirs
// alone; one they merely lead still has cover behind them.
func TestExposures(t *testing.T) {
	t.Parallel()
	risks := []resolve.TopicRisk{
		{Topic: "billing", Level: "critical", Experts: []resolve.RiskExpert{{ID: "a", Name: "Ada", Share: 1}}},
		{Topic: "kafka", Level: "critical", Experts: []resolve.RiskExpert{{ID: "a", Name: "Ada", Share: 1}}},
		{Topic: "payroll", Level: "elevated", Experts: []resolve.RiskExpert{
			{ID: "b", Name: "Bo", Share: 0.7}, {ID: "a", Name: "Ada", Share: 0.3}}},
		{Topic: "docs", Level: "ok", Experts: []resolve.RiskExpert{
			{ID: "b", Name: "Bo", Share: 0.4}, {ID: "a", Name: "Ada", Share: 0.35}}},
	}
	want := []Exposure{
		{Name: "Ada", ID: "a", Sole: []string{"billing", "kafka"}},
		{Name: "Bo", ID: "b", Leading: []string{"payroll"}},
	}
	if diff := cmp.Diff(want, Exposures(risks), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("exposures mismatch (-want +got):\n%s", diff)
	}
}

// TestCountUsesEveryScoredSubject is the guard on the report's honesty: the
// headline figures are taken before the table is capped, so showing fewer rows
// never quietly shrinks the finding.
func TestCountUsesEveryScoredSubject(t *testing.T) {
	t.Parallel()
	var risks []resolve.TopicRisk
	for i := range 10 {
		risks = append(risks, resolve.TopicRisk{
			Topic: fmt.Sprintf("t%d", i), Level: "critical",
			Experts: []resolve.RiskExpert{{ID: "a", Name: "Ada", Share: 1}},
		})
	}
	got := Count(risks, Exposures(risks))
	want := Counts{Critical: 10, SinglePoint: 10, Exposed: 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("counts mismatch (-want +got):\n%s", diff)
	}
}

// TestWriteRiskIsSelfContained checks the report travels. It has to open with
// no network, no whodar, and no server behind it, so nothing may be fetched at
// open time.
func TestWriteRiskIsSelfContained(t *testing.T) {
	t.Parallel()
	risks := []resolve.TopicRisk{
		{Topic: "billing", Level: "critical", Concentration: 1, BusFactor: 1,
			Experts: []resolve.RiskExpert{{ID: "a", Name: "Ada Lovelace", Share: 1}}},
	}
	var buf bytes.Buffer
	err := WriteRisk(&buf, Brief{
		Generated: time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC),
		People:    12, Scored: 1, Sources: []string{"git", "slack"},
		Risks: risks, Totals: Count(risks, Exposures(risks)), Exposed: Exposures(risks),
	})
	if err != nil {
		t.Fatalf("WriteRisk: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"billing", "Ada Lovelace", "git, slack", "100%", "Knowledge risk brief"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q", want)
		}
	}
	// A fetch of any kind means the report stops working the moment it is
	// forwarded somewhere without the network it was written on.
	for _, external := range []string{"src=\"http", "href=\"http", "@import", "fetch(", "XMLHttpRequest"} {
		if strings.Contains(out, external) {
			t.Errorf("report reaches outside itself with %q", external)
		}
	}
}

// TestWriteRiskEscapes checks a person or subject named after markup cannot
// break the document, which matters because every name in it came from an
// indexed source rather than from whodar.
func TestWriteRiskEscapes(t *testing.T) {
	t.Parallel()
	risks := []resolve.TopicRisk{{
		Topic: `<img src=x onerror="alert(1)">`, Level: "critical", BusFactor: 1,
		Experts: []resolve.RiskExpert{{ID: "a", Name: `</td></tr><script>alert(2)</script>`, Share: 1}},
	}}
	var buf bytes.Buffer
	if err := WriteRisk(&buf, Brief{Generated: time.Now(), Risks: risks, Scored: 1}); err != nil {
		t.Fatalf("WriteRisk: %v", err)
	}
	out := buf.String()
	for _, bad := range []string{"<img src=x", "<script>alert(2)</script>", "</td></tr><script"} {
		if strings.Contains(out, bad) {
			t.Errorf("indexed text was rendered as markup: %q survived", bad)
		}
	}
	// Escaped is the point: the text still appears, inert.
	if !strings.Contains(out, "&lt;img src=x") {
		t.Error("the subject name is missing entirely, want it escaped rather than dropped")
	}
}
