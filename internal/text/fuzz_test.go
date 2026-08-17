package text

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzNormalizers feeds the tokenizer family arbitrary bytes, since it runs
// over every message, name, and title whodar ever ingests, in whatever
// encoding a source emits. Properties: nothing panics, tokens are never empty,
// a slug never contains a space, and normalizing twice equals normalizing
// once, so re-indexing cannot drift.
func FuzzNormalizers(f *testing.F) {
	for _, seed := range []string{
		"", " ", "billing retries", "José Zoë Søren Müller",
		"кириллица 漢字 عربى", "a\x00b\xff\xfe", "🎉🎉 emoji only",
		strings.Repeat("kafka ", 500), "under_score-hyphen.dot@sign",
		"́́ bare combining marks", "ＦＵＬＬＷＩＤＴＨ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		tokens := Tokenize(s)
		for _, tok := range tokens {
			if tok == "" {
				t.Fatalf("Tokenize(%q) produced an empty token", s)
			}
		}
		terms := Terms(s)
		for _, term := range terms {
			if term == "" {
				t.Fatalf("Terms(%q) produced an empty term", s)
			}
		}
		slug := Slug(s)
		if strings.ContainsAny(slug, " \t\n") {
			t.Fatalf("Slug(%q) = %q contains whitespace", s, slug)
		}
		if slug != "" && !utf8.ValidString(slug) {
			t.Fatalf("Slug(%q) = %q is not valid UTF-8", s, slug)
		}
		if again := Slug(slug); again != slug {
			t.Fatalf("Slug is not idempotent: %q -> %q -> %q", s, slug, again)
		}
		folded := Fold(s)
		if Fold(folded) != folded {
			t.Fatalf("Fold is not idempotent on %q", s)
		}
	})
}
