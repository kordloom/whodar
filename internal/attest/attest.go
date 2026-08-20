// Package attest turns a whodar finding into a LoomSeal evidence bundle: a
// signed, offline-verifiable claim with a digest of the evidence behind it, so a
// third party can trust the finding without trusting the machine that produced
// it. It uses the public LoomSeal producer primitives, so the bundle verifies
// with the loomseal verifier and nothing is sent anywhere.
package attest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kordloom/loomseal/seal"
)

// Seal builds and signs a one-claim LoomSeal bundle. The claim carries payload
// under claimType, about subject, with a sha256 digest of evidence as the record
// behind it. The returned bytes are a canonical signed bundle the loomseal
// verifier accepts offline.
func Seal(
	priv ed25519.PrivateKey, product, version, installID, claimType string,
	subject map[string]any, payload any, evidence []byte, now time.Time,
) ([]byte, error) {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("attest: private key has no ed25519 public key")
	}
	at := now.UTC().Format(time.RFC3339)

	sum := sha256.Sum256(evidence)
	claim := map[string]any{
		"type":    claimType,
		"at":      at,
		"payload": payload,
		"evidence": []any{map[string]any{
			"digest":     "sha256:" + hex.EncodeToString(sum[:]),
			"media_type": "application/json",
			"role":       "report",
			"present":    true,
			"location":   "whodar/risk.json",
		}},
	}
	link, err := seal.LinkV1(nil, installID, 1, "", claim)
	if err != nil {
		return nil, fmt.Errorf("attest: link: %w", err)
	}
	claim["chain"] = map[string]any{"seq": 1, "prev": "", "link": link}

	bundle := map[string]any{
		"loomseal":   "0.1",
		"bundle_id":  "lsb_" + bundleID(pub, at),
		"subject":    subject,
		"created_at": at,
		"producer": map[string]any{
			"install_id":      installID,
			"key_id":          seal.KeyID(pub),
			"product":         product,
			"product_version": version,
			"public_key":      base64.StdEncoding.EncodeToString(pub),
		},
		"chain": map[string]any{
			"profile": "loomseal-chain-v1",
			"keyed":   false,
			"params":  map[string]any{"install_id": installID},
			"head":    map[string]any{"seq": 1, "link": link},
		},
		"claims":  []any{claim},
		"anchors": []any{},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("attest: encode: %w", err)
	}
	signed, err := seal.SignBundle(raw, priv)
	if err != nil {
		return nil, fmt.Errorf("attest: sign: %w", err)
	}
	return signed, nil
}

// InstallID derives a stable install identifier from a public key, so every
// bundle from the same key shares one id without storing another secret.
func InstallID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "in_" + hex.EncodeToString(sum[:8])
}

// bundleID derives a bundle identifier from the key and timestamp.
func bundleID(pub ed25519.PublicKey, at string) string {
	sum := sha256.Sum256(append([]byte(at), pub...))
	return hex.EncodeToString(sum[:10])
}
