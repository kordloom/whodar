package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssessProducesSealedDeliverable runs assess over the Slack export
// fixture and an org CSV, then checks every file of the deliverable exists,
// parses, and the seal verifies offline.
func TestAssessProducesSealedDeliverable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "assessment")
	csv := filepath.Join(dir, "people.csv")
	content := "name,email,title,team,topics\n" +
		"Alice Nguyen,alice@corp.com,Staff Engineer,Payments,kafka;billing\n" +
		"Bob Okafor,bob@corp.com,SRE,Infra,terraform\n"
	if err := os.WriteFile(csv, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	_, stderr, err := runCmd(t, "assess", "--data-dir", dir,
		"--org-csv", csv, "--slack-export", exportZip, "--out", out)
	if err != nil {
		t.Fatalf("assess: %v\n%s", err, stderr)
	}
	if !strings.Contains(string(stderr), "topics scored") {
		t.Errorf("no summary printed:\n%s", stderr)
	}

	for _, name := range []string{
		"summary.md", "report.html", "findings.json", "ownership.json", "departures.json",
		"assessment.loomseal", "README.txt",
	} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("deliverable lacks %s: %v", name, err)
		}
	}

	var findings []map[string]any
	data, err := os.ReadFile(filepath.Join(out, "findings.json"))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	if err := json.Unmarshal(data, &findings); err != nil {
		t.Fatalf("findings.json does not parse: %v", err)
	}
	if len(findings) == 0 {
		t.Error("no topics scored from two sources of real shape")
	}

	summary, err := os.ReadFile(filepath.Join(out, "summary.md"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	for _, want := range []string{"# Knowledge continuity summary", "subjects scored"} {
		if !strings.Contains(string(summary), want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
	if strings.Contains(string(summary), "%!") {
		t.Errorf("summary has a formatting error:\n%s", summary)
	}

	html, err := os.ReadFile(filepath.Join(out, "report.html"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(html), "<html") && !strings.Contains(string(html), "<!doctype") {
		t.Error("report.html does not look like a page")
	}

	// The seal must verify with the same install's key.
	verifyOut, _, err := runCmd(t, "attest", "verify", "--data-dir", dir,
		filepath.Join(out, "assessment.loomseal"))
	if err != nil {
		t.Fatalf("attest verify: %v", err)
	}
	if !strings.Contains(string(verifyOut), "{") {
		t.Errorf("verify output = %s", verifyOut)
	}
}

// TestAssessRequiresInput verifies assess refuses to run with nothing named.
func TestAssessRequiresInput(t *testing.T) {
	t.Parallel()
	if _, _, err := runCmd(t, "assess", "--data-dir", t.TempDir()); err == nil {
		t.Fatal("assess with no inputs: want error, got nil")
	}
}
