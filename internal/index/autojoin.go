package index

import (
	"slices"
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/identity"

	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/util"
)

// minHandleLen keeps trivially short handles from joining anyone.
const minHandleLen = 3

// JoinResult reports what AutoJoin did: how many handle-only people it unioned
// to a canonical person, and which handles it left separate because their name
// or email collided across more than one distinct person.
type JoinResult struct {
	// Joined is how many handle-only people were unioned to a canonical person.
	Joined int
	// Joins records each merge with its confidence and evidence.
	Joins []Join
	// Ambiguous lists the handle ids left unresolved because they matched more
	// than one person, so a reader can add them to the alias file.
	Ambiguous []string
}

// Join records one inferred identity merge: a handle-only id folded into a
// canonical person, with how sure the merge is and the evidence for it. Joins
// by shared email or provider id are not inferences and are not recorded here.
type Join struct {
	// Alias is the handle-only id that was folded in, such as "github:kim-doe".
	Alias model.ID
	// Canonical is the person the alias was folded into.
	Canonical model.ID
	// Confidence is how sure the merge is, from 0 to 1. It is a heuristic prior
	// on the evidence, not a calibrated probability.
	Confidence float64
	// Reason names the evidence, such as "unique name match".
	Reason string
}

// Confidence priors for each kind of inferred join. A name that points at one
// person is strong; a colliding name rescued by corroboration is weaker, and a
// shared team corroborates more firmly than a couple of shared topics.
const (
	confUniqueName   = 0.9
	confEmailVariant = 0.85
	confSharedTeam   = 0.8
	confSharedTopics = 0.7
	// confSameName is the confidence in two records being one person on the
	// strength of a full name and the work they both do. It sits lowest of the
	// inferred joins because nothing identifies the two to each other directly.
	confSameName = 0.65
)

// AutoJoin unions each handle-only person, such as github:kim-doe or
// codeowners:kim-doe, with the one canonical person whose flattened name or
// email local-part matches the handle, so kim-doe, Kim Doe, and
// kim.doe@example.com become one node without an alias file. A handle that
// matches nobody or more than one person stays separate; the alias file
// remains the override for those. It also merges people who share an email
// local part under a dotted or cross-domain variant when their names agree. It
// returns a JoinResult with the join count and the handles left ambiguous; run
// Canonicalize afterward to merge the graph.
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

	var joins []Join
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
		conf, reason := confUniqueName, "unique name match"
		if len(cands) > 1 {
			// Ambiguous: an ambiguous name must never silently collapse two
			// people, so merge only when exactly one candidate corroborates by
			// a shared team or two shared topics.
			ok = false
			var matches []model.ID
			var mConf float64
			var mReason string
			for _, c := range cands {
				if cf, why := corroboration(hp, g.People[c]); cf > 0 {
					matches = append(matches, c)
					mConf, mReason = cf, why
				}
			}
			if len(matches) == 1 {
				target, ok = matches[0], true
				conf, reason = mConf, "name and "+mReason
			}
		}
		if !ok {
			blocked = append(blocked, string(id))
			continue
		}
		r.Union(target, id)
		joins = append(joins, Join{Alias: id, Canonical: target, Confidence: conf, Reason: reason})
	}

	// Merge people who are one person under an email variant: their email local
	// parts match once dots are dropped (first.last vs firstlast, or the same
	// mailbox on a primary and an onmicrosoft domain), confirmed by an identical
	// display name so two different people who share a local part are never
	// collapsed. Plus-tags are already folded by NormalizeEmail.
	byLocal := make(map[string][]model.ID)
	for id, p := range g.People {
		if p.Email == "" || util.IsRoleEmail(p.Email) {
			continue
		}
		key := dotStrip(emailLocal(strings.ToLower(p.Email)))
		if len(key) < minHandleLen {
			continue
		}
		canon := r.Canonical(id)
		if !slices.Contains(byLocal[key], canon) {
			byLocal[key] = append(byLocal[key], canon)
		}
	}
	for key, group := range byLocal {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				a, b := r.Canonical(group[i]), r.Canonical(group[j])
				if a == b {
					continue
				}
				pa, pb := g.People[a], g.People[b]
				if pa == nil || pb == nil {
					continue
				}
				name := flatten(pa.Name)
				// The local part must BE the full name (john.smith, not a bare
				// first name), so two different people who only share a first
				// name at different domains are never collapsed. A name of one
				// word is that bare first name however the address is spelled,
				// and two people called Michael at two domains are not evidence
				// of one person with two addresses.
				if name == "" || name != flatten(pb.Name) || name != key {
					continue
				}
				if !strings.Contains(strings.TrimSpace(pa.Name), " ") {
					continue
				}
				r.Union(a, b)
				target := r.Canonical(a)
				other := b
				if target == b {
					other = a
				}
				joins = append(joins, Join{Alias: other, Canonical: target, Confidence: confEmailVariant, Reason: "matching email variant"})
			}
		}
	}
	joins = append(joins, sameNameJoins(g, r)...)
	sort.Strings(blocked)
	ix.joins = pruneJoins(mergeJoins(ix.joins, joins), ix)
	return JoinResult{Joined: len(joins), Joins: joins, Ambiguous: blocked}
}

// mergeJoins overlays freshly inferred joins onto any restored from a prior
// index, keyed by alias: a re-inferred join takes the new confidence, while a
// restored join this run could not re-derive is kept so its confidence
// survives. The result is sorted by alias for a stable ledger.
func mergeJoins(restored, fresh []Join) []Join {
	if len(restored) == 0 {
		sort.Slice(fresh, func(i, j int) bool { return fresh[i].Alias < fresh[j].Alias })
		return fresh
	}
	byAlias := make(map[model.ID]Join, len(restored)+len(fresh))
	for _, j := range restored {
		byAlias[j.Alias] = j
	}
	for _, j := range fresh {
		byAlias[j.Alias] = j
	}
	out := make([]Join, 0, len(byAlias))
	for _, j := range byAlias {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// pruneJoins drops joins whose person is gone from the graph, which happens when
// a source is replaced and its people no longer exist, so a stale merge is not
// shown or re-persisted. It also refreshes each kept join's canonical to the
// current resolution so the persisted ledger does not go stale.
func pruneJoins(joins []Join, ix *Index) []Join {
	r := ix.identityResolver()
	out := make([]Join, 0, len(joins))
	for _, j := range joins {
		canon := r.Canonical(j.Alias)
		if _, ok := ix.Graph.People[canon]; !ok {
			continue
		}
		j.Canonical = canon
		out = append(out, j)
	}
	return out
}

// minSameNameSubjects is how many real subjects two records must both hold
// before a shared name is taken as evidence they are one person.
const minSameNameSubjects = 3

// sameNameJoins merges the records of one person who appears twice. A
// maintainer commits from work and from home, or from a GitHub noreply address
// and their own, and nothing in either record links the two: no shared handle,
// no matching local part. What is left is the name they sign with and the work
// they do, and when a full name and several real subjects both agree, the two
// records are one person.
//
// It is deliberately narrow. Collapsing two people who merely share a name is a
// worse failure than leaving one person split in two, so a single-word name
// never qualifies, a name held by more than two records is treated as a common
// name rather than a split identity, and scaffolding subjects that everybody
// holds do not count as agreement.
func sameNameJoins(g *model.Graph, r *identity.Resolver) []Join {
	byName := make(map[string][]model.ID)
	for id, p := range g.People {
		if handleOnly(id) {
			continue
		}
		name := strings.TrimSpace(strings.ToLower(p.Name))
		if name == "" || !strings.Contains(name, " ") {
			continue
		}
		c := r.Canonical(id)
		if !slices.Contains(byName[name], c) {
			byName[name] = append(byName[name], c)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []Join
	for _, name := range names {
		ids := byName[name]
		if len(ids) != 2 {
			continue
		}
		slices.Sort(ids)
		if sharedSubjects(g, g.People[ids[0]], g.People[ids[1]]) < minSameNameSubjects {
			continue
		}
		r.Union(ids[0], ids[1])
		target := r.Canonical(ids[0])
		other := ids[1]
		if target == ids[1] {
			other = ids[0]
		}
		out = append(out, Join{
			Alias: other, Canonical: target,
			Confidence: confSameName, Reason: "same name and shared subjects",
		})
	}
	return out
}

// sharedSubjects counts the real subjects two people both hold. Scaffolding
// that everybody in an organization holds agrees by default and proves nothing,
// so it does not count towards the total.
func sharedSubjects(g *model.Graph, a, b *model.Person) int {
	if a == nil || b == nil {
		return 0
	}
	n := 0
	for tid := range a.Topics {
		if _, ok := b.Topics[tid]; !ok {
			continue
		}
		if g.Topics[tid].Salient() {
			n++
		}
	}
	return n
}

// corroboration reports how firmly two people are the same despite an ambiguous
// name match, returning the confidence prior and the evidence, or (0, "") when
// nothing corroborates. A shared team counts for more than shared topics.
func corroboration(a, b *model.Person) (float64, string) {
	if a == nil || b == nil {
		return 0, ""
	}
	if a.TeamID != "" && a.TeamID == b.TeamID {
		return confSharedTeam, "shared team"
	}
	shared := 0
	for tid := range a.Topics {
		if _, ok := b.Topics[tid]; ok {
			shared++
			if shared >= 2 {
				return confSharedTopics, "shared topics"
			}
		}
	}
	return 0, ""
}

// Joins returns the inferred identity merges from the last AutoJoin, each with
// its confidence and evidence. Joins by shared email or provider id are not
// listed: they are identity, not inference.
func (ix *Index) Joins() []Join { return ix.joins }

// JoinsFor returns the inferred joins that resolve into the person canonical,
// so a caller can show how that person's identities were merged.
func (ix *Index) JoinsFor(canonical model.ID) []Join {
	r := ix.identityResolver()
	want := r.Canonical(canonical)
	var out []Join
	for _, j := range ix.joins {
		if r.Canonical(j.Alias) == want {
			out = append(out, j)
		}
	}
	return out
}

// CanonicalID returns the canonical identifier id resolves to, so a caller
// outside the package can group aliases under the person they belong to.
func (ix *Index) CanonicalID(id model.ID) model.ID {
	return ix.identityResolver().Canonical(id)
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

// dotStrip removes dots from an email local-part so first.last and firstlast
// compare equal.
func dotStrip(local string) string { return strings.ReplaceAll(local, ".", "") }

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
