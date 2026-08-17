package index

import (
	"strings"

	"github.com/kordloom/whodar/internal/model"
)

// minHandleLen keeps trivially short handles from joining anyone.
const minHandleLen = 3

// AutoJoin unions each handle-only person, such as github:kim-doe or
// codeowners:kim-doe, with the one canonical person whose flattened name or
// email local-part matches the handle, so kim-doe, Kim Doe, and
// kim.doe@example.com become one node without an alias file. A handle that
// matches nobody or more than one person stays separate; the alias file
// remains the override for those. It returns how many handles joined; run
// Canonicalize afterward to merge the graph.
func (ix *Index) AutoJoin() int {
	r := ix.identityResolver()
	g := ix.Graph

	byFlat := make(map[string]model.ID)
	ambiguous := make(map[string]bool)
	add := func(key string, id model.ID) {
		if len(key) < minHandleLen {
			return
		}
		// Two entries collide only when they are genuinely distinct people; two
		// identifiers the resolver already unions are one person, not ambiguity.
		if have, ok := byFlat[key]; ok && r.Canonical(have) != r.Canonical(id) {
			ambiguous[key] = true
			return
		}
		byFlat[key] = id
	}
	for id, p := range g.People {
		if handleOnly(id) {
			continue
		}
		add(flatten(p.Name), id)
		if p.Email != "" {
			add(flatten(emailLocal(p.Email)), id)
		}
	}

	joined := 0
	for id := range g.People {
		if !handleOnly(id) {
			continue
		}
		key := flatten(handlePart(id))
		if len(key) < minHandleLen || ambiguous[key] {
			continue
		}
		target, ok := byFlat[key]
		if !ok || r.Canonical(target) == r.Canonical(id) {
			continue
		}
		r.Union(target, id)
		joined++
	}
	return joined
}

// handleOnly reports whether id is a source-prefixed handle, such as
// github:kim-doe, rather than an email or a name slug.
func handleOnly(id model.ID) bool {
	s := string(id)
	return strings.IndexByte(s, ':') > 0 && !strings.Contains(s, "@")
}

// handlePart returns the handle after the source prefix.
func handlePart(id model.ID) string {
	s := string(id)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// emailLocal returns the part of an email before the at sign.
func emailLocal(email string) string {
	if i := strings.IndexByte(email, '@'); i >= 0 {
		return email[:i]
	}
	return email
}

// flatten lowercases s and keeps only letters and digits, so kim-doe,
// kim.doe, and Kim Doe all compare equal.
func flatten(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
