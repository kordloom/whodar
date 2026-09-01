package text

import (
	"fmt"
	"strings"
	"testing"
)

// TestScrub covers every credential family the scrubber knows and the prose
// that must survive it untouched.
func TestScrub(t *testing.T) {
	t.Parallel()
	tests := []struct {
		In        string
		WantOut   string
		WantFound int
	}{{ // Test 0: AWS access key id.
		In:      "creds are AKIAIOSFODNN7EXAMPLE for staging",
		WantOut: "creds are [redacted] for staging", WantFound: 1,
	}, { // Test 1: GitHub classic token.
		In:      "use ghp_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789 for the API",
		WantOut: "use [redacted] for the API", WantFound: 1,
	}, { // Test 2: GitHub fine-grained token.
		In:      "github_pat_11AAAAAAA0abcdefghijklmnopqrstuvwxyz set",
		WantOut: "[redacted] set", WantFound: 1,
	}, { // Test 3: Slack bot token.
		In:      "xoxb-1234567890-9876543210-AbCdEfGhIjKl",
		WantOut: "[redacted]", WantFound: 1,
	}, { // Test 4: Stripe live secret key. Assembled at runtime so the fake
		// key never appears as a literal; a literal here trips the very push
		// protection scanners this scrubber exists to complement.
		In:      "sk_live" + "_AbCdEf1234567890GhIjKlMn",
		WantOut: "[redacted]", WantFound: 1,
	}, { // Test 5: Google API key.
		In:      "maps key AIzaSyA1bC2dE3fG4hI5jK6lM7nO8pQ9rS0tU1v done",
		WantOut: "maps key [redacted] done", WantFound: 1,
	}, { // Test 6: JWT.
		In:      "session eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.SflKxwRJSMeKKF2QT4fwpM expired",
		WantOut: "session [redacted] expired", WantFound: 1,
	}, { // Test 7: Bearer header keeps the scheme word.
		In:      "curl -H 'Authorization: Bearer abcdef0123456789abcdef' api",
		WantOut: "curl -H 'Authorization: Bearer [redacted]' api", WantFound: 1,
	}, { // Test 8: Labeled password keeps the label.
		In:      "password=hunter2secret worked",
		WantOut: "password=[redacted] worked", WantFound: 1,
	}, { // Test 9: Labeled api key with colon and quotes.
		In:      `set api_key: "sk-abc123def456" in the config`,
		WantOut: "set api_key: [redacted] in the config", WantFound: 1,
	}, { // Test 10: PEM private key block, all of it.
		In: "here -----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKC\nabc\n" +
			"-----END RSA PRIVATE KEY----- thanks",
		WantOut: "here [redacted] thanks", WantFound: 1,
	}, { // Test 11: Truncated PEM block still goes, to the end.
		In:      "paste: -----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk",
		WantOut: "paste: [redacted]", WantFound: 1,
	}, { // Test 12: Plain prose survives.
		In:      "the billing retries fail when the ledger drifts",
		WantOut: "the billing retries fail when the ledger drifts",
	}, { // Test 13: Credential words in sentences survive.
		In:      "my token expired so auth failed and the password reset email went out",
		WantOut: "my token expired so auth failed and the password reset email went out",
	}, { // Test 14: Several secrets in one message all go.
		In:        "AKIAIOSFODNN7EXAMPLE and xoxb-1234567890-abcDEFghiJKL together",
		WantOut:   "[redacted] and [redacted] together",
		WantFound: 2,
	}, { // Test 15: Empty input.
		In: "", WantOut: "",
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			out, found := Scrub(test.In)
			if out != test.WantOut {
				t.Errorf("Scrub(%q) = %q, want %q", test.In, out, test.WantOut)
			}
			if found != test.WantFound {
				t.Errorf("found = %d, want %d", found, test.WantFound)
			}
			if test.WantFound > 0 && strings.Contains(out, test.In) {
				t.Error("the original secret survived")
			}
		})
	}
}
