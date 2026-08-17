package index

import (
	"github.com/kordloom/whodar/internal/text"
)

// The index shares one normalizer with every other searchable store in the
// program, so a posting key here and a query term elsewhere are produced by
// identical rules. These wrappers keep the package's own call sites short.

// tokenize splits s into searchable tokens, dropping stopwords.
func tokenize(s string) []string { return text.Tokenize(s) }

// Terms splits s into the stemmed tokens the scorer matches on. Indexes built
// outside this package use it so their posting keys and query terms are
// produced by exactly the same normalization.
func Terms(s string) []string { return text.Terms(s) }

// fold removes diacritics so accented and unaccented spellings match.
func fold(s string) string { return text.Fold(s) }

// slug normalizes text into a lowercase hyphenated identifier.
func slug(s string) string { return text.Slug(s) }

// stem reduces a token to its root for matching.
func stem(token string) string { return text.Stem(token) }

// stemMatches reports whether want equals the stem of any token of texts.
func stemMatches(want string, texts ...string) bool { return text.StemMatches(want, texts...) }
