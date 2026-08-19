package index

import (
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/model"
)

// minHandleLen keeps trivially short handles from joining anyone.
const minHandleLen = 3

// JoinResult reports what AutoJoin did: how many handle-only people it unioned
// to a canonical person, and which handles it left separate because their name
// or email collided across more than one distinct person.
type JoinResult struct {
	// Joined is how many handle-only people were unioned to a canonical person.
	Joined int
	// Ambiguous lists the handle ids left unresolved because they matched more
	// than one person, so a reader can add them to the alias file.
	Ambiguous []string
}

// AutoJoin unions each handle-only person, such as github:kim-doe or
// codeowners:kim-doe, with the one canonical person whose flattened name or
// email local-part matches the handle, so kim-doe, Kim Doe, and
// kim.doe@example.com become one node without an alias file. A handle that
// matches nobody or more than one person stays separate; the alias file
// remains the override for those. It returns a JoinResult with the join count
// and the handles left ambiguous; run Canonicalize afterward to merge the graph.
func (ix *Index) AutoJoin() JoinResult {
	r := ix.identityResolver()
	g := ix.Graph

	// Index every canonical person by all of their flattened forms: display
	// name, every email local-part, and each joined identity (email or handle),
	// so a source-prefixed handle can match on any of them, not just one name.
	byKey := make(map[string]map[model.ID]bool)
	addKey := func(key string, id model.ID) {
		if len(key) < minHandleLen {
			return
		}
		set := byKey[key]
		if set == nil {
			set = make(map[model.ID]bool)
			byKey[key] = set
		}
		set[r.Canonical(id)] = true
	}
	for id, p := range g.People {
		if handleOnly(id) {
			continue
		}
		addKey(flatten(p.Name), id)
		if p.Email != "" {
			addKey(flatten(emailLocal(p.Email)), id)
		}
		for _, alt := range p.Identities {
			addKey(flatten(altKeyPart(alt)), id)
		}
	}

	// distinct returns the distinct canonical people sharing key, minus self.
	distinct := func(key string, self model.ID) []model.ID {
		set := byKey[key]
		if set == nil {
			return nil
		}
		seen := make(map[model.ID]bool)
		var out []model.ID
		for c := range set {
			cc := r.Canonical(c)
			if cc == self || seen[cc] {
				continue
			}
			seen[cc] = true
			out = append(out, cc)
		}
		return out
	}

	joined := 0
	var blocked []string
	for id, hp := range g.People {
		if !handleOnly(id) {
			continue
		}
		key := flatten(handlePart(id))
		if len(key) < minHandleLen {
			continue
		}
		cands := distinct(key, r.Canonical(id))
		if len(cands) == 0 {
			continue
		}
		target, ok := cands[0], true
		if len(cands) > 1 {
			// Ambiguous: an ambiguous name must never silently collapse two
			// people, so merge only when exactly one candidate corroborates by
			// a shared team or two shared topics.
			ok = false
			var matches []model.ID
			for _, c := range cands {
				if corroborate(hp, g.People[c]) {
					matches = append(matches, c)
				}
			}
			if len(matches) == 1 {
				target, ok = matches[0], true
			}
		}
		if !ok {
			blocked = append(blocked, string(id))
			continue
		}
		r.Union(target, id)
		joined++
	}
	sort.Strings(blocked)
	return JoinResult{Joined: joined, Ambiguous: blocked}
}

// corroborate reports whether two people share enough signal (the same team, or
// at least two topics) to treat an otherwise ambiguous handle match as one
// person rather than a coincidence of names.
func corroborate(a, b *model.Person) bool {
	if a == nil || b == nil {
		return false
	}
	if a.TeamID != "" && a.TeamID == b.TeamID {
		return true
	}
	shared := 0
	for tid := range a.Topics {
		if _, ok := b.Topics[tid]; ok {
			shared++
			if shared >= 2 {
				return true
			}
		}
	}
	return false
}

// altKeyPart returns the flattenable part of an alternate identity: the email
// local-part, the handle after a source prefix, or the whole slug.
func altKeyPart(alt model.ID) string {
	s := string(alt)
	if strings.Contains(s, "@") {
		return emailLocal(s)
	}
	if strings.IndexByte(s, ':') > 0 {
		return handlePart(alt)
	}
	return s
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
