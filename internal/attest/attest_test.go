package attest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/kordloom/loomseal/jcs"
	"github.com/kordloom/loomseal/seal"
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
		"whodar.knowledge-risk/1", map[string]any{"id": "org:test", "type": "org"},
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

// TestSealedLinkRecomputesTheWayAVerifierDoes reproduces, in process, the single check that decides
// whether a sealed finding is worth anything.
//
// A verifier does not trust the link a bundle declares. It strips the members the format excludes
// from the commitment and recomputes the link from what is left, and the bundle is only chained if
// the two agree. This seal committed to the whole claim including its evidence packaging, which the
// format excludes, so every bundle it produced declared a link no verifier could reproduce: the
// signature checked out, the chain did not, and the verdict was NOT VERIFIED. That is the exact
// verdict the feature exists to avoid, and nothing here noticed because nothing recomputed the link.
//
// This does not call the reference verifier, which cannot be imported. It does what the reference
// verifier does, using the one exported definition of what a link covers.
func TestSealedLinkRecomputesTheWayAVerifierDoes(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	install := InstallID(pub)
	raw, err := Seal(priv, "whodar", "test", install,
		"whodar.knowledge-risk/1", map[string]any{"id": "org:test", "type": "org"},
		map[string]any{"critical": 2}, []byte(`[{"topic":"auth"}]`),
		time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	parsed, err := jcs.Parse(raw)
	if err != nil {
		t.Fatalf("bundle is not JSON: %v", err)
	}
	bundle, _ := parsed.(map[string]any)
	claims, ok := bundle["claims"].([]any)
	if !ok || len(claims) != 1 {
		t.Fatalf("claims = %v, want exactly one", bundle["claims"])
	}
	claim, _ := claims[0].(map[string]any)
	chain, _ := claim["chain"].(map[string]any)
	declared, _ := chain["link"].(string)
	if declared == "" {
		t.Fatal("the claim declares no link")
	}

	// Exactly what a verifier does: take the claim, keep only what a link commits to, recompute.
	recomputed, err := seal.LinkV1(nil, install, 1, "", seal.ClaimContent(claim))
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if recomputed != declared {
		t.Fatalf("the declared link is not the one a verifier recomputes:\n  declared   %s\n"+
			"  recomputed %s\nA bundle in this state verifies its signature and fails its chain.",
			declared, recomputed)
	}
}

// TestEvidencePackagingDoesNotChangeTheLink is the property behind the check above.
//
// present and location say whether and where the report travels beside this copy. If they entered
// the commitment, the same finding delivered with its report attached and without it would carry
// different links, and at most one of the two could ever verify.
func TestEvidencePackagingDoesNotChangeTheLink(t *testing.T) {
	t.Parallel()
	base := map[string]any{
		"type": "whodar.knowledge-risk/1", "at": "2026-08-20T12:00:00Z",
		"payload": map[string]any{"critical": 2},
	}
	withPackaging := map[string]any{}
	without := map[string]any{}
	for k, v := range base {
		withPackaging[k] = v
		without[k] = v
	}
	withPackaging["evidence"] = []any{map[string]any{
		"digest": "sha256:aa", "role": "report",
		"present": true, "location": "whodar/risk.json",
	}}
	without["evidence"] = []any{map[string]any{
		"digest": "sha256:aa", "role": "report",
	}}

	a, err := seal.LinkV1(nil, "install", 1, "", seal.ClaimContent(withPackaging))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	b, err := seal.LinkV1(nil, "install", 1, "", seal.ClaimContent(without))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if a != b {
		t.Error("packaging changed the link, so the same finding packaged two ways cannot both " +
			"verify against the head it was sealed under")
	}
}
