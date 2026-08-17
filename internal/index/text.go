package index

import (
	"strings"
	"unicode"

	"github.com/kljensen/snowball"
	"golang.org/x/text/unicode/norm"
)

// stopwords are common words ignored during tokenization. They include the
// filler words found in questions like "who do I talk to about X".
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "who": true, "are": true,
	"with": true, "about": true, "talk": true, "owns": true, "own": true,
	"what": true, "how": true, "our": true, "you": true, "your": true,
	"this": true, "that": true, "from": true, "does": true, "can": true,
	"do": true, "to": true, "of": true, "in": true, "on": true, "or": true,
	"is": true, "it": true, "we": true, "me": true, "my": true, "an": true,
	"at": true, "be": true, "as": true, "by": true, "us": true, "need": true,
	"help": true, "have": true, "has": true, "get": true, "got": true,
	"know": true, "knows": true, "handle": true, "handles": true,
	"where": true, "when": true, "which": true, "why": true,
}

// tokenize lowercases and folds text, then splits it into searchable tokens,
// dropping stopwords and tokens shorter than two bytes. Folding removes
// diacritics so "josé" and "jose" share a token, and letters of any script are
// kept so a non-ASCII name is not mangled or dropped.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(fold(strings.ToLower(s)), func(r rune) bool {
		return !isWordRune(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) < 2 || stopwords[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Terms splits s into the stemmed tokens the scorer matches on. Indexes built
// outside this package use it so their posting keys and query terms are
// produced by exactly the same normalization.
func Terms(s string) []string {
	tokens := tokenize(s)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, stem(t))
	}
	return out
}

// isWordRune reports whether r can be part of a token: a letter or digit from
// any script. Folding removes diacritics before this runs, so accented letters
// arrive as their base form.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// fold removes diacritics by decomposing to NFKD and dropping the combining
// marks, so "josé" folds to "jose". Letters from scripts without combining
// marks, such as CJK, pass through unchanged. It is safe for concurrent use.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// slug normalizes text into a lowercase identifier with single hyphens between
// runs of word runes. Diacritics fold so accented and unaccented spellings
// produce the same slug. Unlike tokenize, it keeps every word.
func slug(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range fold(strings.ToLower(strings.TrimSpace(s))) {
		if isWordRune(r) {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if !hyphen && b.Len() > 0 {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// stem reduces a token to its root for matching, so "scans", "scan", and
// "scanning" share a posting. It is applied only to posting keys and query
// terms, never to displayed text, so reasons and names stay readable.
func stem(token string) string {
	s, err := snowball.Stem(token, "english", true)
	if err != nil || s == "" {
		return token
	}
	return s
}

// stemMatches reports whether the stem want equals the stem of any token of
// the given texts. It mirrors the scorer, which compares stems, so reasons
// and confidence agree with what actually scored, including fuzzy hits that
// resolved to a different stem than the raw query term.
func stemMatches(want string, texts ...string) bool {
	for _, txt := range texts {
		for _, tok := range tokenize(txt) {
			if stem(tok) == want {
				return true
			}
		}
	}
	return false
}
