package cmd

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/license"
)

// TestCheckBundleLicense pins the licensing-chain verdicts on a sealed
// finding: evaluation-only, garbage license, verified-but-unbound, and
// verified-and-bound, which is the only state that reads as provably issued.
func TestCheckBundleLicense(t *testing.T) {
	t.Parallel()
	bundle := func(payload map[string]any, producerKey string) []byte {
		b, err := json.Marshal(map[string]any{
			"producer": map[string]any{"public_key": producerKey},
			"claims":   []any{map[string]any{"payload": payload}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	good := license.License{ID: "L1", Org: "Acme", Tier: license.Risk, AttestKey: "SEALKEY"}
	swapped := verifyLicense
	verifyLicense = func(raw []byte, _ time.Time) (license.License, error) {
		var signed struct {
			License license.License `json:"license"`
		}
		if err := json.Unmarshal(raw, &signed); err != nil || signed.License.Org == "" {
			return license.License{}, fmt.Errorf("%w: stub", license.ErrInvalid)
		}
		return signed.License, nil
	}
	t.Cleanup(func() { verifyLicense = swapped })

	now := time.Now()
	tests := []struct {
		Name      string
		Raw       []byte
		WantEval  bool
		WantLic   bool
		WantBound bool
		WantIssue bool
	}{{
		Name: "evaluation only", WantEval: true,
		Raw: bundle(map[string]any{"evaluation": true}, "SEALKEY"),
	}, {
		Name: "garbage license", WantIssue: true,
		Raw: bundle(map[string]any{"licensee": map[string]any{"nonsense": 1}}, "SEALKEY"),
	}, {
		Name: "verified but unbound", WantLic: true, WantIssue: true,
		Raw: bundle(map[string]any{"licensee": map[string]any{"license": good}}, "OTHERKEY"),
	}, {
		Name: "verified and bound", WantLic: true, WantBound: true,
		Raw: bundle(map[string]any{"licensee": map[string]any{"license": good}}, "SEALKEY"),
	}, {
		Name: "neither mark nor license", WantIssue: true,
		Raw: bundle(map[string]any{}, "SEALKEY"),
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			t.Parallel()
			got := checkBundleLicense(test.Raw, now)
			if got.Evaluation != test.WantEval || got.Licensed != test.WantLic || got.Bound != test.WantBound {
				t.Errorf("verdict = %+v", got)
			}
			if (got.Problem != "") != test.WantIssue {
				t.Errorf("problem = %q, want issue %v", got.Problem, test.WantIssue)
			}
		})
	}
}
