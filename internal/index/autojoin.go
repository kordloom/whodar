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

// minTruncation is how many characters a handle and a name must agree on before
// one is read as a shortening of the other. Measured on a real project: at six
// the rule claims andrew-codechimp for somebody displayed as Andre W., and at
// eight every match it makes is the right one.
const minTruncation = 8

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
	// confHandleDomain is the confidence in a handle that matches the domain of
	// exactly one person's email, such as frenck owning git@frenck.dev. A domain
	// somebody registered identifies them about as firmly as a matching mailbox
	// does, and public providers can never match, since hundreds of people share
	// them and a pointer to hundreds of people points at nobody. It sits below a
	// name match because a company domain with a single person indexed against
	// it will match that company's handle, which is right when the handle is how
	// that vendor maintains its own code and wrong when it is not. The merge is
	// listed with its evidence either way, which is what makes it correctable.
	confHandleDomain = 0.85
	// confHandleTruncation is the confidence in a handle and a full name where
	// one spelling starts the other, such as gjohansson-st and G Johansson, or
	// milanmeu and Milan Meulemans. It needs minTruncation characters of
	// agreement, because six is reached by coincidence and eight is not, and it
	// needs a full name on the other side, since a lone given name would claim
	// every handle that happens to start with it.
	confHandleTruncation = 0.8
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
	// Handles already accounted for, so each later pass only considers the ones
	// still unexplained.
	settled := make(map[model.ID]bool)
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
		settled[id] = true
		joins = append(joins, Join{Alias: id, Canonical: target, Confidence: conf, Reason: reason})
	}

	// A handle nobody's name matched may still be the domain of somebody's
	// address: a maintainer who commits as git@frenck.dev is the owner writing
	// themselves down as frenck, and no name or mailbox ties the two together.
	// This is what leaves declared owners looking like they do no work at all.
	byDomain := make(map[string]map[model.ID]bool)
	for id, p := range g.People {
		if handleOnly(id) || p.Email == "" {
			continue
		}
		key := flatten(emailDomainLabel(p.Email))
		if len(key) < minHandleLen {
			continue
		}
		set := byDomain[key]
		if set == nil {
			set = make(map[model.ID]bool)
			byDomain[key] = set
		}
		set[r.Canonical(id)] = true
	}
	for id := range g.People {
		if !handleOnly(id) || settled[id] {
			continue
		}
		self := r.Canonical(id)
		key := flatten(handlePart(id))
		if len(key) < minHandleLen {
			continue
		}
		var target model.ID
		n := 0
		for c := range byDomain[key] {
			if cc := r.Canonical(c); cc != self {
				target = cc
				n++
			}
		}
		// More than one person on that domain means it is a provider or a
		// company, and points at nobody in particular.
		if n != 1 {
			continue
		}
		r.Union(target, id)
		settled[id] = true
		joins = append(joins, Join{
			Alias: id, Canonical: target,
			Confidence: confHandleDomain, Reason: "handle matches email domain",
		})
	}

	// A handle is often the person's name with the end cut off, or their name
	// with something stuck on it. Neither spelling matches whole, so nothing
	// above sees them as one person.
	type named struct {
		id   model.ID
		flat string
	}
	var fullNames []named
	for id, p := range g.People {
		// A full name only. A lone given name would claim every handle starting
		// with it, and there is usually exactly one person called Michael.
		if handleOnly(id) || !strings.Contains(strings.TrimSpace(p.Name), " ") {
			continue
		}
		key := flatten(p.Name)
		if len(key) < minTruncation {
			continue
		}
		fullNames = append(fullNames, named{id: r.Canonical(id), flat: key})
	}
	for id := range g.People {
		if !handleOnly(id) || settled[id] {
			continue
		}
		self := r.Canonical(id)
		key := flatten(handlePart(id))
		if len(key) < minTruncation {
			continue
		}
		var target model.ID
		n := 0
		for _, cand := range fullNames {
			if cand.id == self {
				continue
			}
			if !strings.HasPrefix(key, cand.flat) && !strings.HasPrefix(cand.flat, key) {
				continue
			}
			if cand.id != target {
				target = cand.id
				n++
			}
		}
		if n != 1 {
			continue
		}
		r.Union(target, id)
		joins = append(joins, Join{
			Alias: id, Canonical: target,
			Confidence: confHandleTruncation, Reason: "handle is a shortened name",
		})
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
	joins = append(joins, sameSurnameJoins(g, r)...)
	joins = append(joins, sameLocalJoins(g, r)...)
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

// minSurname is how long a surname must be before it is distinctive enough to
// join two records on. Short ones are held by too many unrelated people.
const minSurname = 5

// sameSurnameJoins merges one person whose two records disagree about their
// given name. A commit signed György Krajcsovits and a CODEOWNERS entry
// resolving to George Krajcsovits are the same maintainer, and nothing joins
// them: the addresses share no local part, the handle matches neither name, and
// sameNameJoins needs the whole name to agree.
//
// It is worth catching because of what it costs. Ownership drift is a claim
// that the person written down is no longer the one doing the job, so a split
// owner holds a fraction of their own work and any passer-by clears the bar. On
// prometheus/prometheus this exact split put tsdb, an area its owner had
// committed to fifty-three times, down as having moved to somebody else.
//
// Three guards, and the third is the one that matters. The surname must be
// distinctive and held by exactly two records, or it is a common name. They
// must share several real subjects. And at least one side must be a private
// commit address rather than a real mailbox, because two colleagues who share a
// surname have two real addresses, and they are also the pair most likely to
// share subjects: they work together. Without that guard, shared subjects argue
// FOR merging the very people who must not be merged.
//
// Measured across home-assistant/core, prometheus/prometheus and cli/cli, this
// makes exactly one merge, the correct one. The candidates it declines share no
// subjects at all: Dan and Jan Cermak, Guillaume and Kai Winter, Miko and Eric
// Stern.
func sameSurnameJoins(g *model.Graph, r *identity.Resolver) []Join {
	bySurname := make(map[string][]model.ID)
	for id, p := range g.People {
		if handleOnly(id) {
			continue
		}
		fields := strings.Fields(strings.ToLower(strings.TrimSpace(p.Name)))
		if len(fields) < 2 {
			continue
		}
		surname := fields[len(fields)-1]
		if len(surname) < minSurname {
			continue
		}
		c := r.Canonical(id)
		if !slices.Contains(bySurname[surname], c) {
			bySurname[surname] = append(bySurname[surname], c)
		}
	}
	surnames := make([]string, 0, len(bySurname))
	for surname := range bySurname {
		surnames = append(surnames, surname)
	}
	sort.Strings(surnames)

	var out []Join
	for _, surname := range surnames {
		ids := bySurname[surname]
		if len(ids) != 2 {
			continue
		}
		slices.Sort(ids)
		if !privateCommitAddress(ids[0]) && !privateCommitAddress(ids[1]) {
			continue
		}
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
			Confidence: confSameName, Reason: "same surname, one private address, and shared subjects",
		})
	}
	return out
}

// minLocal is how long an email local-part must be before it is distinctive
// enough to join two records on.
const minLocal = 5

// sameLocalJoins merges one person who writes from two mailboxes that share a
// distinctive local part: bwplotka@gmail.com and bwplotka@google.com are one
// maintainer, and nothing else connects them — the names may differ, the
// handle join refuses precisely because BOTH candidates match it, and the
// work splits across the two so each looks smaller than the person is. On a
// real project that split handed an area to a colleague whose records had
// merged, over the maintainer whose records had not.
//
// The guards mirror sameSurnameJoins: the local must be long enough to be a
// chosen name rather than a common word, held by exactly two email records,
// not a role mailbox, and the two must share several real subjects. Unlike the
// surname rule both sides here are real mailboxes on purpose: sharing a
// distinctive local ACROSS domains is the signature of one person's personal
// and work addresses, where sharing a surname is the signature of a family.
func sameLocalJoins(g *model.Graph, r *identity.Resolver) []Join {
	byLocal := make(map[string][]model.ID)
	for id, p := range g.People {
		if handleOnly(id) {
			continue
		}
		local := dotStrip(emailLocal(strings.ToLower(p.Email)))
		if len(local) < minLocal || util.IsRoleEmail(p.Email) {
			continue
		}
		c := r.Canonical(id)
		if !slices.Contains(byLocal[local], c) {
			byLocal[local] = append(byLocal[local], c)
		}
	}
	locals := make([]string, 0, len(byLocal))
	for local := range byLocal {
		locals = append(locals, local)
	}
	sort.Strings(locals)

	var out []Join
	for _, local := range locals {
		ids := byLocal[local]
		if len(ids) != 2 {
			continue
		}
		slices.Sort(ids)
		a, b := g.People[ids[0]], g.People[ids[1]]
		// When both records display the same bare single-word name, the local
		// is that name over again and adds nothing: michael@a.com and
		// michael@b.com are two Michaels until something better says
		// otherwise, and single-word names are exactly what the name rules
		// refuse. The join earns its confidence when the mailboxes agree while
		// the names could not have: bwplotka@gmail.com signs as bwplotka and
		// bwplotka@google.com signs as Bartek Plotka, and only the local
		// connects them.
		an, bn := strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)
		if strings.EqualFold(an, bn) && !strings.Contains(an, " ") {
			continue
		}
		if sharedSubjects(g, a, b) < minSameNameSubjects {
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
			Confidence: confSameName, Reason: "same distinctive mailbox name at two domains, and shared subjects",
		})
	}
	return out
}

// privateCommitAddress reports whether an id is one GitHub issues in place of a
// real mailbox, which is the shape a second record of one person usually takes.
func privateCommitAddress(id model.ID) bool {
	_, ok := util.GitHubNoreplyLogin(string(id))
	return ok
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

// emailDomainLabel returns the registrable label of an address's domain, so
// git@frenck.dev gives frenck. Anything more of the domain would never match a
// handle, and anything less would match half the addresses in the graph.
func emailDomainLabel(email string) string {
	i := strings.IndexByte(email, '@')
	if i < 0 {
		return ""
	}
	parts := strings.Split(strings.ToLower(email[i+1:]), ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
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
