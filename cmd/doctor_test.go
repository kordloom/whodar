package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestDiagnose(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	healthy := doctorFacts{
		IndexPath: "/d/index.json", IndexLoaded: true, People: 100,
		BuiltAt: now.Add(-time.Hour), Now: now, Embeddings: true,
		IndexSources: []string{"slack"}, Configured: map[string]bool{"slack": true, "github": false},
		LicenseReason: "free",
	}
	tests := []struct {
		Facts     doctorFacts
		WantName  string
		WantLevel checkLevel
		WantFails int
	}{{ // Test 0: No index is a hard failure with a build fix.
		Facts: doctorFacts{
			IndexPath: "/d/index.json", IndexLoaded: false, IndexErr: fs.ErrNotExist, Now: now,
			Configured: map[string]bool{"slack": false}, LicenseReason: "free",
		},
		WantName: "index", WantLevel: levelFail, WantFails: 1,
	}, { // Test 1: A healthy index passes with no failures.
		Facts: healthy, WantName: "freshness", WantLevel: levelOK, WantFails: 0,
	}, { // Test 2: A stale index warns for a refresh.
		Facts:    func() doctorFacts { f := healthy; f.BuiltAt = now.Add(-60 * 24 * time.Hour); return f }(),
		WantName: "freshness", WantLevel: levelWarn, WantFails: 0,
	}, { // Test 3: An empty index warns.
		Facts:    func() doctorFacts { f := healthy; f.People = 0; return f }(),
		WantName: "content", WantLevel: levelWarn, WantFails: 0,
	}, { // Test 4: A source in the index whose credentials went missing warns.
		Facts: func() doctorFacts {
			f := healthy
			f.IndexSources = []string{"github"}
			f.Configured = map[string]bool{"github": false}
			return f
		}(),
		WantName: "source:github", WantLevel: levelWarn, WantFails: 0,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			findings := diagnose(test.Facts)
			fails := 0
			var found *finding
			for i := range findings {
				if findings[i].Level == levelFail {
					fails++
				}
				if findings[i].Name == test.WantName {
					found = &findings[i]
				}
			}
			if fails != test.WantFails {
				t.Errorf("fails = %d, want %d", fails, test.WantFails)
			}
			if found == nil {
				t.Fatalf("no finding named %q in %+v", test.WantName, findings)
			}
			if found.Level != test.WantLevel {
				t.Errorf("%q level = %d, want %d", test.WantName, found.Level, test.WantLevel)
			}
		})
	}
}

// TestDiagnoseTellsMissingFromBroken checks the two ways an index fails to load
// read differently. A first run has no index yet, which is expected and needs a
// build; an index that exists but cannot be parsed is a real fault, and telling
// somebody to build one would be the wrong advice.
func TestDiagnoseTellsMissingFromBroken(t *testing.T) {
	t.Parallel()
	base := doctorFacts{
		IndexPath: "/d/index.json", IndexLoaded: false, Now: time.Now(),
		Configured: map[string]bool{"slack": false}, LicenseReason: "free",
	}
	find := func(f doctorFacts) string {
		for _, got := range diagnose(f) {
			if got.Name == "index" {
				return got.Detail
			}
		}
		t.Fatal("no index finding")
		return ""
	}

	missing := base
	missing.IndexErr = fmt.Errorf("index: open: %w", fs.ErrNotExist)
	if got := find(missing); !strings.Contains(got, "no index at /d/index.json yet") {
		t.Errorf("missing index detail = %q, want it to say there is not one yet", got)
	}

	broken := base
	broken.IndexErr = fmt.Errorf("index: open: %w", errors.New("unexpected end of JSON input"))
	got := find(broken)
	if !strings.Contains(got, "unexpected end of JSON input") {
		t.Errorf("broken index detail = %q, want the actual cause", got)
	}
	// The wrapping the error collected on its way here is noise to the reader.
	if strings.Contains(got, "index: open:") {
		t.Errorf("broken index detail = %q, want the cause without its wrapping", got)
	}
}
