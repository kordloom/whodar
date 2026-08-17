package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/policy"
)

// TestGuardLLMHost verifies the non-redacting model paths (semantic and Ollama)
// only reach a non-loopback host under the open policy. Redacted must not admit
// a known provider here: those paths ship full profiles, so redacted's
// known-provider allowance would leak PII to a third party.
func TestGuardLLMHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Name string
		Mode policy.Mode
		URL  string
		Want error
	}{
		{Name: "loopback under strict", Mode: policy.Strict, URL: "http://localhost:11434"},
		{Name: "loopback ip under strict", Mode: policy.Strict, URL: "http://127.0.0.1:11434"},
		{Name: "alternate loopback ip", Mode: policy.Strict, URL: "http://127.0.0.2:11434"},
		{Name: "ipv6 loopback", Mode: policy.Strict, URL: "http://[::1]:11434"},
		{Name: "loopback under redacted", Mode: policy.Redacted, URL: "http://localhost:11434"},
		{Name: "loopback under open", Mode: policy.Open, URL: "http://localhost:11434"},

		{Name: "remote under strict", Mode: policy.Strict,
			URL: "https://api.openai.com", Want: policy.ErrEgressDenied},
		{Name: "known provider under redacted", Mode: policy.Redacted,
			URL: "https://api.openai.com", Want: policy.ErrEgressDenied},
		{Name: "arbitrary host under redacted", Mode: policy.Redacted,
			URL: "https://evil.example.com", Want: policy.ErrEgressDenied},
		{Name: "remote under open", Mode: policy.Open, URL: "https://api.openai.com"},
		{Name: "arbitrary host under open", Mode: policy.Open, URL: "https://evil.example.com"},

		{Name: "no scheme has no host", Mode: policy.Open,
			URL: "api.openai.com", Want: ErrBadArgs},
		{Name: "opaque url has no host", Mode: policy.Open,
			URL: "http:api.openai.com", Want: ErrBadArgs},
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			t.Parallel()
			err := guardLLMHost(policy.New(test.Mode, false), test.URL)
			if !errors.Is(err, test.Want) {
				t.Errorf("guardLLMHost(%s, %q) = %v, want %v", test.Mode, test.URL, err, test.Want)
			}
		})
	}
}

// TestPickResolverRedactedLeak reproduces the exploit: whodar ask --mode llm
// --ollama-url <remote> under a locked redacted policy would build a
// non-redacting resolver against the remote host and ship full PII. The Ollama
// and semantic paths must be denied for any non-loopback host unless open.
func TestPickResolverRedactedLeak(t *testing.T) {
	t.Parallel()
	ix := index.New()
	lockedRedacted := &options{pol: policy.New(policy.Redacted, true)}

	// The Ollama chat path pointed at a known provider host must be denied.
	if _, err := pickResolver(
		ix, lockedRedacted, "llm", "", "", "https://api.openai.com", "ollama", ""); err == nil {
		t.Error("redacted + remote --ollama-url (known provider) was allowed; PII would leak")
	}
	// An arbitrary remote host on the Ollama path must be denied too.
	if _, err := pickResolver(
		ix, lockedRedacted, "llm", "", "", "https://evil.example.com", "ollama", ""); err == nil {
		t.Error("redacted + remote --ollama-url (arbitrary host) was allowed")
	}
	// The semantic path is the same non-redacting leak.
	if _, err := pickResolver(
		ix, lockedRedacted, "semantic", "", "", "https://api.openai.com", "ollama", ""); err == nil {
		t.Error("redacted + remote semantic host was allowed")
	}

	// Open accepts a remote Ollama host: the operator has taken responsibility.
	open := &options{pol: policy.New(policy.Open, false)}
	if _, err := pickResolver(
		ix, open, "llm", "", "", "https://api.openai.com", "ollama", ""); err != nil {
		t.Errorf("open + remote --ollama-url was denied: %v", err)
	}
}
