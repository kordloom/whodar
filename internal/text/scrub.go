package text

import (
	"regexp"
	"strings"
)

// redactedMark replaces each secret found in ingested text. It is a plain
// word on purpose: it tokenizes cleanly, it is greppable in a stored index,
// and it never carries any part of what it replaced.
const redactedMark = "[redacted]"

// secretPatterns match credentials as they actually appear in pasted chat and
// ticket text. Each pattern must be specific enough that prose never matches:
// a missed secret is redacted at the next pattern down, but a false positive
// eats someone's sentence.
var secretPatterns = []*regexp.Regexp{
	// Private key blocks, PEM armor and everything inside.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|\z)`),
	// AWS access key ids, and their secret keys when labeled.
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	// GitHub tokens: classic ghp_/gho_/ghu_/ghs_/ghr_ and fine-grained.
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,255}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,255}\b`),
	// Slack bot, user, app, and legacy tokens.
	regexp.MustCompile(`\bxox[abeprs]-[A-Za-z0-9-]{10,255}\b`),
	// Stripe secret and restricted keys.
	regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,255}\b`),
	// Google API keys.
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	// JWTs: three dot-separated base64url parts, first decoding to '{"'.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	// Bearer and token authorization header values.
	regexp.MustCompile(`(?i)\b(bearer|authorization:)\s+[A-Za-z0-9._~+/=-]{16,512}`),
	// Labeled credentials: password=..., api_key: "...", export TOKEN=... The
	// label and separator survive; the value goes. The separator must be an
	// explicit = or :, because "my token expired" is a sentence, not a leak.
	regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|apikey|` +
		`access[_-]?key|client[_-]?secret|private[_-]?key|auth)\b(\s*[:=]+\s*)["']?[^\s"']{6,}["']?`),
}

// labeledValue is the index of the labeled-credential pattern, which keeps its
// label group when redacting so "password=hunter2" becomes "password=[redacted]"
// rather than vanishing.
var labeledValue = len(secretPatterns) - 1

// bearerValue is the index of the bearer pattern, which keeps the scheme word.
var bearerValue = len(secretPatterns) - 2

// Scrub replaces credential-shaped substrings with a redaction mark and
// reports how many were found. It runs over every piece of prose whodar
// ingests, because people paste keys into Slack and tickets, and a
// who-knows-what index must never become a where-the-secrets-are index.
func Scrub(s string) (string, int) {
	if s == "" {
		return s, 0
	}
	found := 0
	for i, pat := range secretPatterns {
		if !pat.MatchString(s) {
			continue
		}
		switch i {
		case labeledValue:
			s = pat.ReplaceAllStringFunc(s, func(m string) string {
				found++
				sub := pat.FindStringSubmatch(m)
				return sub[1] + strings.TrimRight(sub[2], `"'`) + redactedMark
			})
		case bearerValue:
			s = pat.ReplaceAllStringFunc(s, func(m string) string {
				found++
				sub := pat.FindStringSubmatch(m)
				return sub[1] + " " + redactedMark
			})
		default:
			s = pat.ReplaceAllStringFunc(s, func(string) string {
				found++
				return redactedMark
			})
		}
	}
	return s, found
}
