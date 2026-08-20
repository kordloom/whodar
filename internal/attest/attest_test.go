package attest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/kordloom/loomseal/jcs"
)

// TestSeal checks the bundle is well formed and its ed25519 signature verifies
// the same way an independent verifier would: canonicalize with the signatures
// cleared, then check the signature over that.
func TestSeal(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	when := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	raw, err := Seal(priv, "whodar", "test", InstallID(pub),
		"whodar.knowledge-risk/1", map[string]any{"id": "organization", "type": "fleet"},
		map[string]any{"critical": 2}, []byte(`[{"topic":"auth"}]`), when)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	parsed, err := jcs.Parse(raw)
	if err != nil {
		t.Fatalf("bundle is not JSON: %v", err)
	}
	b, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("bundle is not an object")
	}
	sigs, ok := b["signatures"].([]any)
	if !ok || len(sigs) != 1 {
		t.Fatalf("signatures = %v, want exactly one", b["signatures"])
	}
	sig0 := sigs[0].(map[string]any)
	sig, err := base64.StdEncoding.DecodeString(sig0["sig"].(string))
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}

	// Reproduce the signed message: the canonical bundle with signatures cleared.
	b["signatures"] = []any{}
	canonical, err := jcs.Serialize(b)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if !ed25519.Verify(pub, canonical, sig) {
		t.Error("bundle signature does not verify")
	}
}
