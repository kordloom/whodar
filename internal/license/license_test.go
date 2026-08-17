package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testNow pins the clock so terms are deterministic.
var testNow = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// signWith issues a license with a throwaway key and swaps the package's
// verification key to match, so the round trip can be tested without the
// licensor's real key.
func signWith(t *testing.T, l License) []byte {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	swapKey(t, base64.StdEncoding.EncodeToString(pub))
	data, err := Sign(l, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return data
}

// swapKey points verification at a test key for the duration of one test.
func swapKey(t *testing.T, key string) {
	t.Helper()
	original := verificationKey
	verificationKey = key
	t.Cleanup(func() { verificationKey = original })
}

// TestVerifyRoundTrip verifies a freshly signed license verifies and reports
// the tier it grants.
func TestVerifyRoundTrip(t *testing.T) {
	data := signWith(t, License{
		ID: "whodar-1", Org: "Acme", Tier: Memory,
		Issued: testNow, Expires: testNow.AddDate(1, 0, 0),
	})
	got, err := Verify(data, testNow)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Org != "Acme" || got.Tier != Memory || got.ID != "whodar-1" {
		t.Errorf("license = %+v, want Acme on the memory tier", got)
	}
}

// TestVerifyRejectsTampering verifies that editing any signed field breaks
// verification, which is what stops a customer from upgrading their own tier
// or extending their own term.
func TestVerifyRejectsTampering(t *testing.T) {
	tests := []struct {
		Name string
		Edit func(s *Signed)
	}{
		{Name: "tier", Edit: func(s *Signed) { s.License.Tier = Memory }},
		{Name: "org", Edit: func(s *Signed) { s.License.Org = "Someone Else" }},
		{Name: "expiry", Edit: func(s *Signed) { s.License.Expires = testNow.AddDate(50, 0, 0) }},
		{Name: "id", Edit: func(s *Signed) { s.License.ID = "whodar-other" }},
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			data := signWith(t, License{
				ID: "whodar-1", Org: "Acme", Tier: Free,
				Issued: testNow, Expires: testNow.AddDate(0, 1, 0),
			})
			var signed Signed
			if err := json.Unmarshal(data, &signed); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			test.Edit(&signed)
			edited, err := json.Marshal(signed)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if _, err := Verify(edited, testNow); !errors.Is(err, ErrInvalid) {
				t.Errorf("Verify on an edited license = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestVerifyExpired verifies an expired license is reported as expired and
// still names itself, so the message can say whose license lapsed and when.
func TestVerifyExpired(t *testing.T) {
	data := signWith(t, License{
		ID: "whodar-1", Org: "Acme", Tier: Memory,
		Issued: testNow.AddDate(-1, 0, 0), Expires: testNow.AddDate(0, 0, -1),
	})
	lic, err := Verify(data, testNow)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify = %v, want ErrExpired", err)
	}
	if lic.Org != "Acme" {
		t.Errorf("expired license = %+v, want it to still name the org", lic)
	}
}

// TestVerifyNeverExpires verifies a license with no end date stays valid.
func TestVerifyNeverExpires(t *testing.T) {
	data := signWith(t, License{ID: "whodar-1", Org: "Acme", Tier: Memory, Issued: testNow})
	if _, err := Verify(data, testNow.AddDate(20, 0, 0)); err != nil {
		t.Errorf("Verify twenty years on = %v, want no error", err)
	}
}

// TestVerifyGarbage verifies malformed input is rejected as invalid rather
// than panicking or being trusted.
func TestVerifyGarbage(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "{", "null", `{"license":{},"signature":"!!!"}`} {
		if _, err := Verify([]byte(in), testNow); !errors.Is(err, ErrInvalid) {
			t.Errorf("Verify(%q) = %v, want ErrInvalid", in, err)
		}
	}
}

// TestSignRejectsBadInput verifies a license cannot be issued without an
// organization, with an unknown tier, or with a malformed key.
func TestSignRejectsBadInput(t *testing.T) {
	t.Parallel()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if _, err := Sign(License{Tier: Memory}, priv); !errors.Is(err, ErrInvalid) {
		t.Errorf("Sign without an org = %v, want ErrInvalid", err)
	}
	if _, err := Sign(License{Org: "Acme", Tier: "platinum"}, priv); !errors.Is(err, ErrInvalid) {
		t.Errorf("Sign with an unknown tier = %v, want ErrInvalid", err)
	}
	if _, err := Sign(License{Org: "Acme", Tier: Memory}, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("Sign with no key = %v, want ErrInvalid", err)
	}
}

// TestResolveFallsBackToFree verifies every failure leaves the free tier in
// force with an explanation, and never an error a command has to handle.
func TestResolveFallsBackToFree(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvLicense, "")

	state := Resolve(dir, testNow)
	if state.Tier != Free || state.Err != nil {
		t.Errorf("no license = %+v, want the free tier with no error", state)
	}
	if !strings.Contains(state.Reason(), "free tier") {
		t.Errorf("reason = %q, want it to mention the free tier", state.Reason())
	}
	if !state.Has(Free) || state.Has(Memory) {
		t.Error("the free tier granted a paid feature")
	}

	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if state := Resolve(dir, testNow); state.Tier != Free || state.Err == nil {
		t.Errorf("corrupt license = %+v, want the free tier with a reason", state)
	}
}

// TestResolveFromEnvironment verifies a license can be supplied inline or by
// path, which is what a container needs.
func TestResolveFromEnvironment(t *testing.T) {
	data := signWith(t, License{ID: "whodar-1", Org: "Acme", Tier: Memory, Issued: testNow})
	dir := t.TempDir()

	t.Setenv(EnvLicense, string(data))
	if state := Resolve(dir, testNow); !state.Has(Memory) {
		t.Errorf("inline license = %+v, want the memory tier", state)
	}

	path := filepath.Join(dir, "elsewhere.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv(EnvLicense, path)
	state := Resolve(dir, testNow)
	if !state.Has(Memory) {
		t.Errorf("license by path = %+v, want the memory tier", state)
	}
	if !strings.Contains(state.Reason(), "Acme") {
		t.Errorf("reason = %q, want it to name the org", state.Reason())
	}
}

// TestResolveExpiredKeepsFreeTier verifies an expired license drops to the
// free tier and says so, without any suggestion that indexed data is affected.
func TestResolveExpiredKeepsFreeTier(t *testing.T) {
	data := signWith(t, License{
		ID: "whodar-1", Org: "Acme", Tier: Memory,
		Issued: testNow.AddDate(-2, 0, 0), Expires: testNow.AddDate(0, 0, -3),
	})
	t.Setenv(EnvLicense, string(data))
	state := Resolve(t.TempDir(), testNow)
	if state.Tier != Free || !errors.Is(state.Err, ErrExpired) {
		t.Fatalf("expired license = %+v, want the free tier and ErrExpired", state)
	}
	if !strings.Contains(state.Reason(), "stays on disk") {
		t.Errorf("reason = %q, want it to say indexed data is untouched", state.Reason())
	}
}
