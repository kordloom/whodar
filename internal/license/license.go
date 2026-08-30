// Package license verifies which whodar features an organization has bought.
// A license is a small signed file: whodar checks the signature with a public
// key compiled into the binary and never contacts a server, so a licensed
// install works on an air-gapped machine and nothing about the organization
// leaves it. Nothing here can be reached over the network, and no check ever
// deletes or withholds data the organization already has.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// publicKey verifies license signatures. The matching private key is held by
// the licensor and never ships.
const publicKey = "5cdDIgH/DWQHWpGF8yjfM9EbKE/7jwfGM+O/hmkigJM="

// keyID2026 names the launch signing key, so a license can say which key
// signed it and a compromised or retired key can be dropped from the list
// without invalidating licenses signed by the others.
const keyID2026 = "kordloom-2026"

// CurrentKeyID is the key new licenses are issued under.
const CurrentKeyID = keyID2026

// verificationKeys are the keys Verify checks against, by key id. Plural from
// day one on purpose: this key anchors more than the paid tier, so rotation
// and revocation must be a list edit, never a format migration. The same list
// is published out of band at whodar.dev/verify, and a relying party should
// prefer that copy over any binary's compiled-in one. Tests swap the map so
// the round trip can be exercised without the licensor's private key.
var verificationKeys = map[string]string{keyID2026: publicKey}

// Tier names a set of features.
type Tier string

const (
	// Free is the tier every install has without a license: the people graph,
	// and recall pointing back at past conversations.
	Free Tier = "free"
	// Risk adds the organizational intelligence layer as a licensed product:
	// knowledge risk, ownership drift, departure impact, and sealed findings
	// without the evaluation mark. Unlicensed installs still run all of it in
	// evaluation mode, clearly labeled, because a leader has to see the
	// finding before anyone budgets for it.
	Risk Tier = "risk"
	// Memory adds the archive on top of Risk: conversation content retained
	// on the organization's own machines, and answers that quote it with
	// citations.
	Memory Tier = "memory"
)

// Valid reports whether t is a tier this build knows.
func (t Tier) Valid() bool { return t == Free || t == Risk || t == Memory }

// rank orders the tiers as a ladder, so a higher license grants everything
// below it.
func (t Tier) rank() int {
	switch t {
	case Memory:
		return 2
	case Risk:
		return 1
	default:
		return 0
	}
}

// License is what an organization bought. It is signed as it stands, so any
// edit to any field invalidates it.
type License struct {
	// ID identifies this license in support conversations.
	ID string `json:"id"`
	// Kid names the signing key that issued this license. Empty on licenses
	// issued before keys had names; those verify against every listed key.
	Kid string `json:"kid,omitempty"`
	// AttestKey is the base64 ed25519 public key of the install's sealing key,
	// when the organization registered one at issuance. A license carrying it
	// rides inside every sealed finding that key produces, which is what makes
	// a licensed seal provably issued rather than merely unlabeled.
	AttestKey string `json:"attestKey,omitempty"`
	// Org is the organization the license was issued to.
	Org string `json:"org"`
	// Tier is the feature set granted.
	Tier Tier `json:"tier"`
	// Issued is when the license was signed.
	Issued time.Time `json:"issued"`
	// Expires is when it stops granting its tier. A zero time never expires.
	Expires time.Time `json:"expires"`
}

// Signed is the file form: the license and the signature over it.
type Signed struct {
	// License is the grant.
	License License `json:"license"`
	// Signature is the base64 ed25519 signature over the canonical license.
	Signature string `json:"signature"`
}

// Errors a license check can report. They are distinct because each one has a
// different answer: buy one, check the file, or renew.
var (
	// ErrNoLicense reports that no license is configured.
	ErrNoLicense = errors.New("no license")
	// ErrInvalid reports a license that is malformed or not signed by the
	// licensor.
	ErrInvalid = errors.New("invalid license")
	// ErrExpired reports a valid license whose term has ended.
	ErrExpired = errors.New("expired license")
)

// verifyAgainstKeys checks a signature against the named key, or against every
// listed key for a license issued before keys had names. A kid naming no
// listed key fails outright: that is what revocation looks like.
func verifyAgainstKeys(kid string, payload, sig []byte) bool {
	try := func(encoded string) bool {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return false
		}
		return ed25519.Verify(ed25519.PublicKey(key), payload, sig)
	}
	if kid != "" {
		encoded, ok := verificationKeys[kid]
		return ok && try(encoded)
	}
	for _, encoded := range verificationKeys {
		if try(encoded) {
			return true
		}
	}
	return false
}

// Expired reports whether the license term has ended at now.
func (l License) Expired(now time.Time) bool {
	return !l.Expires.IsZero() && now.After(l.Expires)
}

// Verify checks a license file's signature and term, returning the license it
// grants. An expired license is returned alongside ErrExpired so a caller can
// still name it and say when it lapsed.
func Verify(data []byte, now time.Time) (License, error) {
	var signed Signed
	if err := json.Unmarshal(data, &signed); err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	sig, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return License{}, fmt.Errorf("%w: signature is not base64: %w", ErrInvalid, err)
	}
	payload, err := canonical(signed.License)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !verifyAgainstKeys(signed.License.Kid, payload, sig) {
		return License{}, fmt.Errorf("%w: signature does not match the licensed details", ErrInvalid)
	}
	if !signed.License.Tier.Valid() {
		return License{}, fmt.Errorf("%w: unknown tier %q", ErrInvalid, signed.License.Tier)
	}
	if signed.License.Expired(now) {
		return signed.License, fmt.Errorf("%w: it ended on %s",
			ErrExpired, signed.License.Expires.Format(time.DateOnly))
	}
	return signed.License, nil
}

// Sign issues a license. Only the licensor can call it usefully, since it
// needs the private key that matches the compiled-in public key.
func Sign(l License, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: signing key is the wrong size", ErrInvalid)
	}
	if strings.TrimSpace(l.Org) == "" {
		return nil, fmt.Errorf("%w: a license needs an organization", ErrInvalid)
	}
	if !l.Tier.Valid() {
		return nil, fmt.Errorf("%w: unknown tier %q", ErrInvalid, l.Tier)
	}
	payload, err := canonical(l)
	if err != nil {
		return nil, err
	}
	signed := Signed{
		License:   l,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)),
	}
	out, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return append(out, '\n'), nil
}

// canonical renders the bytes that are signed and verified. Times are stamped
// to a fixed layout so a round trip through JSON cannot change them.
func canonical(l License) ([]byte, error) {
	out, err := json.Marshal(struct {
		ID      string `json:"id"`
		Org     string `json:"org"`
		Tier    string `json:"tier"`
		Issued  string `json:"issued"`
		Expires string `json:"expires"`
	}{
		ID:      l.ID,
		Org:     l.Org,
		Tier:    string(l.Tier),
		Issued:  stamp(l.Issued),
		Expires: stamp(l.Expires),
	})
	if err != nil {
		return nil, fmt.Errorf("canonical license: %w", err)
	}
	return out, nil
}

// stamp renders a time for signing, using an empty string for the zero time so
// a license without an end date signs consistently.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
