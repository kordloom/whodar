// Package index builds an on-disk, searchable map of expertise from connector
// records and ranks people and channels for a query without an LLM.
package index

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agnivade/levenshtein"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/identity"
	"github.com/kordloom/whodar/internal/invindex"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/text"
	"github.com/kordloom/whodar/internal/util"
	"github.com/kordloom/whodar/internal/vault"
)

// DefaultHalfLife is the age at which a dated record's weight halves.
const DefaultHalfLife = 180 * 24 * time.Hour

// Field weights scale how strongly each signal contributes to a score. An
// explicit topic or a channel name outweighs a title word, which outweighs a
// team word, which outweighs free text.
const (
	// weightTopic is the affinity weight of an explicit topic tag.
	weightTopic = 3.0
	// weightChannelName is the affinity weight of the channel's own name.
	weightChannelName = 3.0
	// weightTitle is the affinity weight of a job-title word.
	weightTitle = 2.0
	// weightTeam is the affinity weight of a team-name word.
	weightTeam = 1.0
	// weightText is the affinity weight of a free-text word.
	weightText = 0.5
)

// Evidence strengths grade how convincing a matched field is when estimating
// confidence: an explicit topic is proof, a passing mention is a hint.
const (
	// evidenceTopic is the strength of an explicit topic or channel-name hit.
	evidenceTopic = 1.0
	// evidenceTitle is the strength of a job-title hit.
	evidenceTitle = 0.85
	// evidenceTeam is the strength of a team-name hit.
	evidenceTeam = 0.7
	// evidenceMention is the strength of a free-text mention.
	evidenceMention = 0.5
)

// personText holds the normalized field text for a person, used to explain why
// the person matched a query.
type personText struct {
	// Titles are the lowercased job titles seen across sources, accumulated so a
	// title a later record lacks is not lost to last-write-wins.
	Titles []string `json:"titles"`
	// Teams are the lowercased team names seen across sources.
	Teams []string `json:"teams"`
	// Topics are the lowercased explicit topic names.
	Topics []string `json:"topics"`
	// Text is the accumulated lowercased free text. It is readable message
	// content used only in memory, to build embeddings and to merge two people
	// on a join, so it is never written to disk.
	Text string `json:"-"`
}

// channelText holds the normalized field text for a channel, used to explain
// why the channel matched a query.
type channelText struct {
	// Name is the lowercased channel name.
	Name string `json:"name"`
	// Topic is the lowercased channel topic.
	Topic string `json:"topic"`
	// Topics are the lowercased explicit topic names.
	Topics []string `json:"topics"`
}

// Index is a searchable expertise index over people and channels.
type Index struct {
	// Graph is the entity graph this index was built from.
	Graph *model.Graph
	// postings maps a token to per-person accumulated weight.
	postings map[string]map[model.ID]float64
	// texts holds normalized per-person field text for reason lookup.
	texts map[model.ID]*personText
	// channelPostings maps a token to per-channel accumulated weight.
	channelPostings map[string]map[model.ID]float64
	// channelTexts holds normalized per-channel field text for reason lookup.
	channelTexts map[model.ID]*channelText
	// personVecs holds per-person embedding vectors when present.
	personVecs map[model.ID][]float32
	// channelVecs holds per-channel embedding vectors when present.
	channelVecs map[model.ID][]float32
	// topicVecs holds per-topic embedding vectors when present. A topic vector
	// describes a subject rather than a person, which is what a question
	// paraphrased in someone's own words is really about.
	topicVecs map[model.ID][]float32
	// resolver maps the identifiers a person accumulates across sources to one
	// canonical identifier.
	resolver *identity.Resolver
	// joins records the inferred identity merges AutoJoin made, with the
	// confidence and evidence for each.
	joins []Join
	// halfLife is the age at which a dated record's weight halves; zero or
	// negative disables recency decay.
	halfLife time.Duration
	// now returns the current time; tests pin it for deterministic decay.
	now func() time.Time
	// fbRules are preprocessed user votes applied during ranking, held
	// atomically so a running server can apply new votes mid-flight.
	fbRules atomic.Pointer[[]fbRule]
	// fbStep is the per-vote score multiplier; zero means the default.
	fbStep float64
	// fbMax clamps net votes per result; zero means the default, negative off.
	fbMax int
	// embedProgress, when set, is called after each entity is embedded.
	embedProgress util.Progress
	// sources holds the raw records per source, needed only to rebuild on a
	// merge. It is nil in an index loaded to answer a query, since the records
	// live in a sidecar file that queries never read. A non-nil map marks an
	// index whose sources are in hand and safe to save back to the sidecar.
	sources map[string][]connector.Record
	// sourceCounts is how many records each source contributed, kept in the main
	// index so status can report it without loading the sidecar.
	sourceCounts map[string]int
	// builtAt is when a loaded index was last written, empty for a fresh one.
	builtAt time.Time
	// personLens and channelLens cache BM25 document lengths, refreshed when
	// postings change so a query never rescans every posting.
	personLens  entityLens
	channelLens entityLens
	// personVocab and channelVocab bucket posting keys by length so a fuzzy term
	// scans only candidates within its edit-distance band, not the whole
	// vocabulary.
	personVocab  vocabIndex
	channelVocab vocabIndex
}

// New returns an empty index with initialized maps.
func New() *Index {
	ix := &Index{
		Graph:           model.NewGraph(),
		postings:        make(map[string]map[model.ID]float64),
		texts:           make(map[model.ID]*personText),
		channelPostings: make(map[string]map[model.ID]float64),
		channelTexts:    make(map[model.ID]*channelText),
		personVecs:      make(map[model.ID][]float32),
		channelVecs:     make(map[model.ID][]float32),
		topicVecs:       make(map[model.ID][]float32),
		resolver:        identity.NewResolver(),
		halfLife:        DefaultHalfLife,
		now:             time.Now,
	}
	ix.refreshStats()
	return ix
}

// refreshStats recomputes the cached length tables and vocabulary buckets from
// the current postings. It runs after any operation that changes postings, so
// serving reads never rescan the full posting set. It must not run concurrently
// with a search, matching the index's build-then-serve lifecycle.
func (ix *Index) refreshStats() {
	ix.personLens = lengthsOf(ix.postings)
	ix.channelLens = lengthsOf(ix.channelPostings)
	ix.personVocab = newVocabIndex(ix.postings)
	ix.channelVocab = newVocabIndex(ix.channelPostings)
}

// SetHalfLife sets the age at which a dated record's weight halves. Zero or
// negative disables recency decay.
func (ix *Index) SetHalfLife(d time.Duration) { ix.halfLife = d }

// decay returns the recency multiplier for a record dated t: one for undated
// records, future dates, or disabled decay, halving per half-life of age.
func (ix *Index) decay(t time.Time) float64 {
	if t.IsZero() || ix.halfLife <= 0 {
		return 1
	}
	age := ix.now().Sub(t)
	if age <= 0 {
		return 1
	}
	return math.Exp2(-float64(age) / float64(ix.halfLife))
}

// Build replaces the index contents with data derived from records, forgetting
// every source read before.
func (ix *Index) Build(records []connector.Record) {
	ix.sources = make(map[string][]connector.Record)
	ix.sourceCounts = make(map[string]int)
	ix.personVecs = make(map[model.ID][]float32)
	ix.channelVecs = make(map[model.ID][]float32)
	ix.topicVecs = make(map[model.ID][]float32)
	ix.take(records)
	ix.rebuild()
}

// Add merges records into the current index. Each source named in records
// replaces whatever that source contributed before, so re-reading a source is
// idempotent: indexing Slack twice leaves the same index as indexing it once,
// and a scheduled merge does not inflate its own weights over time. Sources
// not named are left as they are. Person records merge by email or id, channel
// records by name. Embeddings are left alone; call Embed to refresh vectors.
func (ix *Index) Add(records []connector.Record) {
	ix.take(records)
	ix.rebuild()
}

// take files records under the source that produced them, replacing that
// source's previous contribution. Records that name no source cannot be told
// apart from the ones already held, so they accumulate instead: every
// connector names itself, and refusing to guess keeps a hand-assembled record
// set from silently erasing another.
func (ix *Index) take(records []connector.Record) {
	if ix.sources == nil {
		ix.sources = make(map[string][]connector.Record)
	}
	incoming := make(map[string][]connector.Record)
	for _, rec := range records {
		incoming[rec.Source] = append(incoming[rec.Source], rec)
	}
	for name, recs := range incoming {
		if name == "" {
			ix.sources[name] = append(ix.sources[name], recs...)
			continue
		}
		ix.sources[name] = recs
	}
	if ix.sourceCounts == nil {
		ix.sourceCounts = make(map[string]int)
	}
	for name := range incoming {
		ix.sourceCounts[name] = len(ix.sources[name])
	}
}

// redactedSources returns a copy of the stored records with readable Text
// replaced by its stemmed Terms, so a saved index holds a search index rather
// than the messages themselves. A record already carrying Terms and no Text,
// which is one read back from a saved index, is passed through untouched.
func redactedSources(sources map[string][]connector.Record) map[string][]connector.Record {
	if sources == nil {
		return nil
	}
	out := make(map[string][]connector.Record, len(sources))
	for name, recs := range sources {
		clean := make([]connector.Record, len(recs))
		for i, rec := range recs {
			clean[i] = redactRecord(rec)
		}
		out[name] = clean
	}
	return out
}

// redactRecord replaces a record's readable Text with its sorted stemmed Terms,
// so a stored or merged record holds a search index rather than the message
// text. A record already carrying only Terms is returned unchanged.
func redactRecord(rec connector.Record) connector.Record {
	if rec.Text != "" {
		// Sort the stemmed terms so word order is destroyed: the stored set
		// reveals no more than the inverted index already does, and cannot be
		// read back as a sentence.
		terms := text.Terms(rec.Text)
		sort.Strings(terms)
		rec.Terms = terms
		rec.Text = ""
	}
	return rec
}

// MergeIncremental folds records from an incremental fetch into a source's
// existing records instead of replacing them, so a fetch that returns only what
// changed since the last run updates the graph without dropping the people and
// topics it did not re-read. A record for an identity already held is summed
// into it; an identity not in the fetch is left untouched. Call LoadSources
// first so the prior records are present to fold into. Both the held and the
// incoming records are reduced to stemmed Terms, so no readable message text is
// kept, and the folded set stays one record per identity, bounded across runs.
//
// Folding matches what a full read produces: a connector's full run already
// emits one record per person carrying that person's whole activity at their
// most recent time, which is exactly what summing the batches and taking the
// latest time yields here. An item edited after the watermark is counted in both
// the held record and the delta; the double count is bounded harmless because
// topic and term weight saturate, and a periodic full re-index recompacts.
func (ix *Index) MergeIncremental(records []connector.Record) {
	if ix.sources == nil {
		ix.sources = make(map[string][]connector.Record)
	}
	incoming := make(map[string][]connector.Record)
	for _, rec := range records {
		incoming[rec.Source] = append(incoming[rec.Source], redactRecord(rec))
	}
	if ix.sourceCounts == nil {
		ix.sourceCounts = make(map[string]int)
	}
	for name, recs := range incoming {
		if name == "" {
			// A record that names no source cannot be matched to prior held
			// records, so it accumulates rather than risk erasing another set,
			// exactly as take treats the same case.
			ix.sources[name] = append(ix.sources[name], recs...)
		} else {
			ix.sources[name] = foldRecords(redactSlice(ix.sources[name]), recs)
		}
		ix.sourceCounts[name] = len(ix.sources[name])
	}
	ix.rebuild()
}

// redactSlice reduces every record in recs to stemmed Terms, so a held set
// loaded with readable Text still folds without keeping that text.
func redactSlice(recs []connector.Record) []connector.Record {
	out := make([]connector.Record, len(recs))
	for i, rec := range recs {
		out[i] = redactRecord(rec)
	}
	return out
}

// foldRecords folds delta records into base by identity, summing the affinity of
// an identity already present and appending a genuinely new one. It keeps one
// record per identity so a source stays bounded across incremental runs.
func foldRecords(base, delta []connector.Record) []connector.Record {
	at := make(map[string]int, len(base))
	out := make([]connector.Record, len(base))
	copy(out, base)
	for i, rec := range out {
		at[foldKey(rec)] = i
	}
	for _, rec := range delta {
		key := foldKey(rec)
		if i, ok := at[key]; ok {
			out[i] = foldRecord(out[i], rec)
			continue
		}
		at[key] = len(out)
		out = append(out, rec)
	}
	return out
}

// foldLinks merges two sets of subject ties, keeping the strongest claim for
// each. Ties are not counts and must not accumulate: how much of the time two
// subjects move together is already a share, and adding two shares together
// would climb past one on nothing more than being re-read.
func foldLinks(base, add []connector.TopicLink) []connector.TopicLink {
	if len(add) == 0 {
		return base
	}
	at := make(map[string]int, len(base)+len(add))
	for i, l := range base {
		at[l.To] = i
	}
	for _, l := range add {
		if i, ok := at[l.To]; ok {
			if l.Weight > base[i].Weight {
				// The whole claim moves together. Keeping the stronger weight
				// beside an older witness count would describe a tie nobody
				// observed.
				base[i] = l
			}
			continue
		}
		at[l.To] = len(base)
		base = append(base, l)
	}
	return base
}

// foldKey identifies the record a later record accumulates into: a person by
// their per-source id, a channel by its slug.
func foldKey(rec connector.Record) string {
	switch rec.Kind {
	case connector.KindChannel:
		return "c\x00" + slug(rec.Name)
	case connector.KindTopic:
		// A subject is keyed as a subject. Folded as a person it would key on
		// the same name slug people do, so a subject called billing and a
		// person whose identity resolves to billing would accumulate into each
		// other.
		return "t\x00" + slug(rec.Name)
	default:
		return "p\x00" + string(personID(rec))
	}
}

// foldRecord sums add into base: it concatenates the topic and term lists that
// rebuild turns into weight, unions channel members, advances the time to the
// most recent activity, and fills any identity field base is missing.
func foldRecord(base, add connector.Record) connector.Record {
	base.Topics = append(base.Topics, add.Topics...)
	base.RecentTopics = append(base.RecentTopics, add.RecentTopics...)
	base.Terms = append(base.Terms, add.Terms...)
	base.Links = foldLinks(base.Links, add.Links)
	for _, m := range add.Members {
		if !slices.Contains(base.Members, m) {
			base.Members = append(base.Members, m)
		}
	}
	for _, a := range add.AltIDs {
		if !slices.Contains(base.AltIDs, a) {
			base.AltIDs = append(base.AltIDs, a)
		}
	}
	if add.Time.After(base.Time) {
		base.Time = add.Time
	}
	if base.Link == "" {
		base.Link = add.Link
	}
	// Keep the better display name rather than always taking the delta's, so an
	// incremental record carrying a handle placeholder never overwrites a real
	// name already held, matching how a full read resolves identity.
	if betterName(base.Name, add.Name) {
		base.Name = add.Name
	}
	base.Email = firstNonEmpty(base.Email, add.Email)
	base.Title = firstNonEmpty(add.Title, base.Title)
	base.Team = firstNonEmpty(add.Team, base.Team)
	base.Org = firstNonEmpty(add.Org, base.Org)
	base.Manager = firstNonEmpty(add.Manager, base.Manager)
	base.PersonID = firstNonEmpty(base.PersonID, add.PersonID)
	if add.Weight > base.Weight {
		base.Weight = add.Weight
	}
	return base
}

// firstNonEmpty returns a when it is non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// rebuild derives the graph, postings, and texts from every retained record.
// Sources are replayed in name order so the same set of records always
// produces the same index, whatever order the runs happened in.
func (ix *Index) rebuild() {
	ix.Graph = model.NewGraph()
	ix.postings = make(map[string]map[model.ID]float64)
	ix.texts = make(map[model.ID]*personText)
	ix.channelPostings = make(map[string]map[model.ID]float64)
	ix.channelTexts = make(map[model.ID]*channelText)
	for _, name := range slices.Sorted(maps.Keys(ix.sources)) {
		for _, rec := range ix.sources[name] {
			switch rec.Kind {
			case connector.KindChannel:
				ix.buildChannel(rec)
			case connector.KindTopic:
				ix.buildTopicLinks(rec)
			default:
				ix.buildPerson(rec)
			}
		}
	}
	ix.refreshStats()
}

// BuiltAt reports when a loaded index was last written, so a caller can show how
// stale it is. It is the zero time for an index that has never been saved.
func (ix *Index) BuiltAt() time.Time { return ix.builtAt }

// PostingCount returns the number of distinct terms in the person index, a
// measure of the searchable vocabulary size.
func (ix *Index) PostingCount() int { return len(ix.postings) }

// SourceNames returns the names of the sources that contributed to the index,
// sorted, so a status view can list each one with its size. It reads the counts
// kept in the main index, so it works without loading the sources sidecar.
func (ix *Index) SourceNames() []string {
	names := make([]string, 0, len(ix.sourceCounts))
	for name := range ix.sourceCounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SourceSize reports how many records the named source contributed to this
// index, so a caller can tell a full read from a truncated one. It reads the
// count kept in the main index, so it works without loading the sources sidecar.
func (ix *Index) SourceSize(name string) int { return ix.sourceCounts[name] }

// LoadAliases merges a JSON alias file into the index's identity resolver so
// records indexed afterward key by their canonical identifier. Call
// Canonicalize to also join people already in the graph.
func (ix *Index) LoadAliases(path string) error {
	return ix.identityResolver().LoadFile(path)
}

// Alias records that two identifiers belong to the same person. Records
// indexed afterward key by the canonical identifier; call Canonicalize to
// also join people already in the graph.
func (ix *Index) Alias(a, b model.ID) {
	ix.identityResolver().Union(a, b)
}

// identityResolver returns the index's resolver, initializing it on first use.
func (ix *Index) identityResolver() *identity.Resolver {
	if ix.resolver == nil {
		ix.resolver = identity.NewResolver()
	}
	return ix.resolver
}

// Canonical resolves an identifier a source used, such as a Slack user ID or
// a GitHub login, to the person it belongs to. An identifier the index has
// never seen comes back unchanged.
func (ix *Index) Canonical(id model.ID) model.ID {
	return ix.identityResolver().Canonical(id)
}

// buildPerson merges one person record into the graph and postings.
func (ix *Index) buildPerson(rec connector.Record) {
	g, postings, texts, r := ix.Graph, ix.postings, ix.texts, ix.identityResolver()
	raw := personID(rec)
	if raw == "" {
		return
	}
	if rec.PersonID != "" && rec.Email != "" && !util.IsRoleEmail(rec.Email) {
		r.Union(model.ID(util.NormalizeEmail(rec.Email)), model.ID(strings.ToLower(strings.TrimSpace(rec.PersonID))))
	}
	for _, alt := range rec.AltIDs {
		if key := identityKey(alt); key != "" && model.ID(key) != raw {
			r.Union(model.ID(key), raw)
		}
	}
	pid := r.Canonical(raw)
	w := rec.Weight
	if w == 0 {
		w = 1
	}
	w *= ix.decay(rec.Time)
	p := g.People[pid]
	if p == nil {
		p = &model.Person{ID: pid, Topics: make(map[model.ID]float64)}
		g.People[pid] = p
	}
	if raw != pid && !slices.Contains(p.Identities, raw) {
		p.Identities = append(p.Identities, raw)
	}
	fillIdentity(p, rec)
	if p.ManagerID != "" {
		p.ManagerID = r.Canonical(p.ManagerID)
	}
	linkOrg(g, p, rec)

	pt := texts[pid]
	if pt == nil {
		pt = &personText{}
		texts[pid] = pt
	}
	addKey := func(key string, fieldWeight float64) {
		if postings[key] == nil {
			postings[key] = make(map[model.ID]float64)
		}
		postings[key][pid] += fieldWeight * w
	}
	add := func(text string, fieldWeight float64) {
		for _, tok := range tokenize(text) {
			addKey(stem(tok), fieldWeight)
		}
	}
	if rec.Title != "" {
		pt.Titles = appendUnique(pt.Titles, strings.ToLower(rec.Title))
		add(rec.Title, weightTitle)
	}
	if rec.Team != "" {
		pt.Teams = appendUnique(pt.Teams, strings.ToLower(rec.Team))
		add(rec.Team, weightTeam)
	}
	addTopic := func(top string, curated bool) {
		tid := topicID(top)
		if tid == "" {
			return
		}
		noteTopic(g, tid, top, rec.Source, curated)
		p.Topics[tid] += weightTopic * w
		if statesOwnership(rec.Source) {
			if p.Stated == nil {
				p.Stated = make(map[model.ID]float64)
			}
			p.Stated[tid] += weightTopic * w
		}
		pt.Topics = append(pt.Topics, strings.ToLower(top))
		add(top, weightTopic)
		if curated && rec.Source == "codeowners" && !slices.Contains(p.Owns, tid) {
			p.Owns = append(p.Owns, tid)
		}
	}
	for _, top := range rec.Topics {
		addTopic(top, true)
	}
	for _, top := range rec.WeakTopics {
		addTopic(top, false)
	}
	// What they have worked on lately, which is a subset of what they know.
	// Kept apart so a subject somebody knows best but has stopped touching can
	// be told from one they are still in.
	for _, top := range rec.RecentTopics {
		tid := topicID(top)
		if tid == "" {
			continue
		}
		if p.Recent == nil {
			p.Recent = make(map[model.ID]float64)
		}
		p.Recent[tid] += weightTopic * w
	}
	// Fresh ingest carries readable Text, kept in memory for embedding and
	// merge and tokenized into postings. A record rebuilt from a saved index
	// carries only the stemmed Terms, which reproduce the same postings without
	// any readable text having touched disk.
	if rec.Text != "" {
		pt.Text = strings.TrimSpace(pt.Text + " " + strings.ToLower(rec.Text))
		add(rec.Text, weightText)
	} else {
		for _, term := range rec.Terms {
			addKey(term, weightText)
		}
	}
}

// buildChannel merges one channel record into the graph and channel postings.
func (ix *Index) buildChannel(rec connector.Record) {
	g, postings, texts, r := ix.Graph, ix.channelPostings, ix.channelTexts, ix.identityResolver()
	cid := model.ID(slug(rec.Name))
	if cid == "" {
		return
	}
	d := ix.decay(rec.Time)
	ch := g.Channels[cid]
	if ch == nil {
		ch = &model.Channel{ID: cid, Name: rec.Name, Topics: make(map[model.ID]float64)}
		g.Channels[cid] = ch
	}
	if rec.Link != "" && ch.URL == "" {
		ch.URL = rec.Link
	}
	if rec.Title != "" {
		ch.Topic = rec.Title
	}
	for _, m := range rec.Members {
		mid := r.Canonical(model.ID(strings.ToLower(m)))
		if !slices.Contains(ch.Members, mid) {
			ch.Members = append(ch.Members, mid)
		}
	}

	ct := texts[cid]
	if ct == nil {
		ct = &channelText{Name: strings.ToLower(rec.Name)}
		texts[cid] = ct
	}
	if rec.Title != "" {
		ct.Topic = strings.ToLower(rec.Title)
	}
	addKey := func(key string, fieldWeight float64) {
		if postings[key] == nil {
			postings[key] = make(map[model.ID]float64)
		}
		postings[key][cid] += fieldWeight * d
	}
	add := func(text string, fieldWeight float64) {
		for _, tok := range tokenize(text) {
			addKey(stem(tok), fieldWeight)
		}
	}
	add(rec.Name, weightChannelName)
	if rec.Title != "" {
		add(rec.Title, weightTopic)
	}
	addTopic := func(top string, curated bool) {
		tid := topicID(top)
		if tid == "" {
			return
		}
		noteTopic(g, tid, top, rec.Source, curated)
		ch.Topics[tid] += weightTopic * d
		ct.Topics = append(ct.Topics, strings.ToLower(top))
		add(top, weightTopic)
	}
	for _, top := range rec.Topics {
		addTopic(top, true)
	}
	for _, top := range rec.WeakTopics {
		addTopic(top, false)
	}
	// Fresh ingest tokenizes readable Text; a rebuilt record replays its
	// stored stemmed Terms, so a channel's sampled chatter never reaches disk.
	if rec.Text != "" {
		add(rec.Text, weightText)
	} else {
		for _, term := range rec.Terms {
			addKey(term, weightText)
		}
	}
}

// evidenceBoost turns the kind of evidence behind a match into a score
// multiplier. Accumulated weight measures how often the words appear, which a
// prolific poster can supply endlessly; the kind of field they appear in is
// what repetition cannot buy. A passing mention stays where its weight put it,
// and a declared topic, title, or team lifts the score, so the person who owns
// a subject beats the person who merely talks about it by construction rather
// than by accident of the tuning constants.
func evidenceBoost(evidence float64) float64 {
	return 0.4 + 0.9*evidence
}

// boostWindowCap bounds how many leading candidates are re-ranked by evidence,
// so an unlimited search does not pay the reason lookup for every match.
const boostWindowCap = 64

// expandedQuery grows the tokenized query with its synonyms and returns the
// full term list, the per-term coverage back to the words actually asked, and
// the asked expression behind each expansion for the reason line. Every
// original term covers itself; an expansion covers the words that triggered it,
// so a hit through a synonym still counts toward how much of the question was
// answered instead of diluting it.
func expandedQuery(query string) (terms []string, covers map[string][]string, asked map[string]string) {
	ordered := tokenize(query)
	terms = distinct(ordered)
	if len(terms) == 0 {
		return nil, nil, nil
	}
	covers = make(map[string][]string, len(terms))
	asked = make(map[string]string)
	for _, t := range terms {
		covers[t] = []string{t}
	}
	for _, e := range expandTerms(query) {
		if _, dup := covers[e.term]; dup {
			continue
		}
		// An expansion may cover a word the tokenizer dropped, such as the "in"
		// of "sign in". Coverage is measured over the searched terms, so only
		// those count; a phrase whose every word was dropped still covers
		// something by standing in for the whole question.
		kept := make([]string, 0, len(e.covers))
		for _, orig := range e.covers {
			if _, real := covers[orig]; real {
				kept = append(kept, orig)
			}
		}
		terms = append(terms, e.term)
		covers[e.term] = kept
		asked[e.term] = e.asked
	}
	return terms, covers, asked
}

// applyExpansionPenalty discounts the resolved expansions, so a synonym never
// outranks the words the person actually used.
func applyExpansionPenalty(resolved map[string]termHit, asked map[string]string) {
	for term := range asked {
		if hit, ok := resolved[term]; ok {
			hit.penalty *= expansionPenalty
			resolved[term] = hit
		}
	}
}

// coveredShare is how much of the asked question a match answered: the share of
// original terms covered by anything it matched, synonyms included.
func coveredShare(matched map[string]bool, covers map[string][]string, originals int) float64 {
	if originals == 0 {
		return 0
	}
	hit := make(map[string]bool, originals)
	for term := range matched {
		for _, orig := range covers[term] {
			hit[orig] = true
		}
	}
	return float64(len(hit)) / float64(originals)
}

// Search ranks people for query and returns up to limit matches. A non-positive
// limit returns all matches.
func (ix *Index) Search(query string, limit int) []model.Match {
	terms, covers, asked := expandedQuery(query)
	if len(terms) == 0 {
		return nil
	}
	originals := 0
	for _, t := range terms {
		if _, isExp := asked[t]; !isExp {
			originals++
		}
	}
	resolved := resolveTerms(ix.postings, ix.personVocab, terms)
	applyExpansionPenalty(resolved, asked)
	scores, matched := scoreByTerms(ix.postings, terms, resolved, len(ix.Graph.People), ix.personLens)
	nets := ix.feedbackNets(terms, false)

	matches := make([]model.Match, 0, len(scores))
	for pid, sc := range scores {
		p := ix.Graph.People[pid]
		if p == nil {
			continue
		}
		var team *model.Team
		if p.TeamID != "" {
			team = ix.Graph.Teams[p.TeamID]
		}
		if net := nets[pid]; net != 0 {
			sc *= ix.feedbackFactor(net)
		}
		matches = append(matches, model.Match{Person: p, Team: team, Score: sc})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Person.ID < matches[j].Person.ID
	})
	// Re-rank the leading candidates by the kind of evidence behind them, so
	// declared ownership outranks accumulated chatter no matter how the raw
	// weights land. Only the window that could plausibly hold the answer is
	// examined, since judging evidence means reading each candidate's fields.
	window := min(len(matches), boostWindowCap)
	for i := range window {
		pid := matches[i].Person.ID
		_, evidence := ix.reasons(pid, matched[pid], resolved, asked)
		matches[i].Score *= evidenceBoost(evidence)
	}
	sort.SliceStable(matches[:window], func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Person.ID < matches[j].Person.ID
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	// Build the reasons and confidence only for the people that survived the
	// limit. Each reason stems the person's whole topic set against every query
	// term, so doing it for every candidate before ranking was the query's cost,
	// and only the returned people need it. Confidence does not affect the
	// ranking, which is by score, so deferring it changes no result.
	for i := range matches {
		pid := matches[i].Person.ID
		reasons, evidence := ix.reasons(pid, matched[pid], resolved, asked)
		if net := nets[pid]; net != 0 {
			reasons = append(reasons, feedbackReason(net))
		}
		matches[i].Reasons = reasons
		matches[i].Confidence = evidence * coveredShare(matched[pid], covers, originals)
	}
	return matches
}

// SearchChannels ranks channels for query and returns up to limit matches, each
// carrying the most relevant active members. A non-positive limit returns all.
func (ix *Index) SearchChannels(query string, limit int) []model.ChannelMatch {
	terms, covers, asked := expandedQuery(query)
	if len(terms) == 0 {
		return nil
	}
	originals := 0
	for _, t := range terms {
		if _, isExp := asked[t]; !isExp {
			originals++
		}
	}
	resolved := resolveTerms(ix.channelPostings, ix.channelVocab, terms)
	applyExpansionPenalty(resolved, asked)
	scores, matched := scoreByTerms(
		ix.channelPostings, terms, resolved, len(ix.Graph.Channels), ix.channelLens)
	nets := ix.feedbackNets(terms, true)

	matches := make([]model.ChannelMatch, 0, len(scores))
	for cid, sc := range scores {
		ch := ix.Graph.Channels[cid]
		if ch == nil {
			continue
		}
		reasons, evidence := ix.channelReasons(cid, matched[cid], resolved, asked)
		if net := nets[cid]; net != 0 {
			sc *= ix.feedbackFactor(net)
			reasons = append(reasons, feedbackReason(net))
		}
		coverage := coveredShare(matched[cid], covers, originals)
		matches = append(matches, model.ChannelMatch{
			Channel:    ch,
			Score:      sc,
			Confidence: evidence * coverage,
			Reasons:    reasons,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Channel.ID < matches[j].Channel.ID
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	// Rank the members only for the channels that survived the limit. Scoring
	// every person and sorting every channel's whole membership up front, only to
	// keep a handful of channels, was the bulk of a query at scale.
	if len(matches) > 0 {
		personResolved := resolveTerms(ix.postings, ix.personVocab, terms)
		personScores, _ := scoreByTerms(
			ix.postings, terms, personResolved, len(ix.Graph.People), ix.personLens)
		for i := range matches {
			matches[i].TopMembers = ix.topMembers(matches[i].Channel, personScores, 3)
		}
	}
	return matches
}

// topMembers returns up to n of a channel's members, most relevant to the query
// first, using precomputed person scores. It selects the top n in a single pass
// with one score lookup per member, rather than sorting the whole membership
// with a lookup inside the comparator, which was the dominant cost of a query at
// scale.
func (ix *Index) topMembers(ch *model.Channel, scores map[model.ID]float64, n int) []*model.Person {
	if n <= 0 {
		return nil
	}
	type scored struct {
		person *model.Person
		score  float64
	}
	best := make([]scored, 0, n+1)
	for _, id := range ch.Members {
		p := ix.Graph.People[id]
		if p == nil {
			continue
		}
		cand := scored{person: p, score: scores[id]}
		pos := len(best)
		for pos > 0 && best[pos-1].score < cand.score {
			pos--
		}
		if pos >= n {
			continue
		}
		best = append(best, scored{})
		copy(best[pos+1:], best[pos:])
		best[pos] = cand
		if len(best) > n {
			best = best[:n]
		}
	}
	out := make([]*model.Person, len(best))
	for i, b := range best {
		out[i] = b.person
	}
	return out
}

// BM25-style ranking parameters. k1 governs how quickly repeated weight for
// one term saturates, b governs how strongly an entity whose accumulated
// profile is far larger than average is discounted, and termWeightCap bounds
// one term's weight before saturation so unbounded repetition in free text
// cannot outrank an explicit topic tag.
const (
	bm25K1        = 1.2
	bm25B         = 0.75
	termWeightCap = 4.0
	// normCap bounds the verbosity discount. A term's weight is capped, so
	// letting the length normalizer grow unchecked eventually divides an
	// explicit topic tag below a single passing mention, which would mean the
	// more work someone does, the worse they rank on what they own.
	//
	// The bound is low on purpose, and the number is measured rather than
	// chosen: against a real repository's own CODEOWNERS, accuracy rises all
	// the way down from 4.5 to here and falls off again below it, because the
	// people who own the most are also the people who appear the most, and
	// discounting them for it hands their subjects to whoever passed through
	// once. Removing the discount entirely is worse again: then whoever appears
	// most often wins everything. See the ranking section of docs/REFERENCE.md before changing it.
	normCap = 1.75
)

// Fuzzy matching bounds. Terms shorter than fuzzyMinLen never fuzz, one edit
// is allowed from fuzzyMinLen, two from fuzzyTwoEditLen, and each edit
// multiplies the term's contribution by fuzzyPenalty so an exact match always
// outranks a corrected one. Two edits are held to longer words: two edits in a
// short word is a large distortion that turns a real miss such as "postgis"
// into a confident wrong match on "postgres", so the second edit is allowed
// only once a word is long enough that two edits stay a small share of it.
const (
	fuzzyMinLen     = 4
	fuzzyTwoEditLen = 9
	fuzzyPenalty    = 0.7
)

// termHit is the resolution of one query term against a posting vocabulary:
// the key to score with and the penalty its edits cost.
type termHit struct {
	// key is the posting key the term scores against.
	key string
	// penalty scales the term's contribution; one for an exact match.
	penalty float64
}

// fuzzy reports whether the resolution corrected the term.
func (h termHit) fuzzy() bool { return h.penalty < 1 }

// vocabIndex groups posting keys by byte length so a fuzzy lookup scans only
// the keys whose length is within the edit-distance band of the query term,
// rather than the entire vocabulary.
type vocabIndex struct {
	// byLen maps a key's byte length to the keys of that length.
	byLen map[int][]string
}

// newVocabIndex buckets a posting vocabulary by key length.
func newVocabIndex(postings map[string]map[model.ID]float64) vocabIndex {
	byLen := make(map[int][]string)
	for k := range postings {
		byLen[len(k)] = append(byLen[len(k)], k)
	}
	return vocabIndex{byLen: byLen}
}

// resolveTerms maps each query term to its posting key, fuzzily when the
// exact stem has no posting. Terms that resolve to nothing are absent.
func resolveTerms(
	postings map[string]map[model.ID]float64, vocab vocabIndex, terms []string,
) map[string]termHit {
	out := make(map[string]termHit, len(terms))
	for _, term := range terms {
		if hit, ok := resolveTerm(postings, vocab, term); ok {
			out[term] = hit
		}
	}
	return out
}

// resolveTerm returns the posting key for one term: its own stem when a
// posting exists, otherwise the closest vocabulary key within the allowed
// edit distance. Only keys whose length is within that distance of the stem
// can match, so the search is confined to those length buckets.
//
// Equally close corrections break to the one the organization actually uses,
// measured by how many people hold it. A real code base contains its own
// misspellings, and they sit exactly as close to a typo as the correct word
// does: asked about "blutooth", whodar answered with a directory misspelled
// "blueooth" rather than bluetooth, purely because it sorted first.
func resolveTerm(
	postings map[string]map[model.ID]float64, vocab vocabIndex, term string,
) (termHit, bool) {
	key := stem(term)
	if len(postings[key]) > 0 {
		return termHit{key: key, penalty: 1}, true
	}
	runes := len([]rune(term))
	if runes < fuzzyMinLen {
		return termHit{}, false
	}
	maxDist := 1
	if runes >= fuzzyTwoEditLen {
		maxDist = 2
	}
	best, bestDist, bestHeld := "", maxDist+1, -1
	for l := len(key) - maxDist; l <= len(key)+maxDist; l++ {
		for _, cand := range vocab.byLen[l] {
			d := levenshtein.ComputeDistance(key, cand)
			if d > bestDist {
				continue
			}
			held := len(postings[cand])
			if d < bestDist || held > bestHeld || (held == bestHeld && cand < best) {
				best, bestDist, bestHeld = cand, d, held
			}
		}
	}
	if best == "" || bestDist > maxDist {
		return termHit{}, false
	}
	return termHit{key: best, penalty: math.Pow(fuzzyPenalty, float64(bestDist))}, true
}

// entityLens holds the accumulated posting mass per entity and its average,
// the document-length inputs to BM25 length normalization.
type entityLens struct {
	// byID is the summed posting weight per entity across all tokens.
	byID map[model.ID]float64
	// avg is the mean of byID, at least one so normalization never divides
	// by zero.
	avg float64
}

// lengthsOf sums posting weight per entity across all tokens. It runs per
// query so the index needs no cache invalidation and stays safe for
// concurrent readers.
func lengthsOf(postings map[string]map[model.ID]float64) entityLens {
	byID := make(map[model.ID]float64, len(postings))
	total := 0.0
	for _, posting := range postings {
		for id, w := range posting {
			byID[id] += w
			total += w
		}
	}
	avg := 1.0
	if len(byID) > 0 {
		avg = total / float64(len(byID))
	}
	if avg <= 0 {
		avg = 1
	}
	return entityLens{byID: byID, avg: avg}
}

// scoreByTerms accumulates per-entity scores over the resolved terms. Each
// term is weighted by inverse document frequency so rarer terms count for
// more, its accumulated weight is capped and saturated so a person who
// repeats a word endlessly cannot outrank the explicit owner, entities with
// far more accumulated text than average are discounted, and a fuzzily
// corrected term contributes at its resolution penalty. It returns the
// scores and, per entity, the set of terms that matched.
func scoreByTerms(
	postings map[string]map[model.ID]float64,
	terms []string,
	resolved map[string]termHit,
	universe int,
	lens entityLens,
) (map[model.ID]float64, map[model.ID]map[string]bool) {
	scores := make(map[model.ID]float64)
	matched := make(map[model.ID]map[string]bool)
	scored := make(map[string]bool)
	for _, term := range terms {
		hit, ok := resolved[term]
		if !ok {
			continue
		}
		posting := postings[hit.key]
		if len(posting) == 0 {
			continue
		}
		// Record the term's coverage for every entity it hit, even if another
		// term already scored this key, so coverage counts distinct terms.
		for id := range posting {
			if matched[id] == nil {
				matched[id] = make(map[string]bool)
			}
			matched[id][term] = true
		}
		// Score each resolved key once: two query terms that stem to the same
		// key describe one piece of evidence, not two.
		if scored[hit.key] {
			continue
		}
		scored[hit.key] = true
		idf := 1.0
		if universe > 0 {
			idf = 1 + math.Log(float64(universe)/float64(len(posting)))
		}
		for id, w := range posting {
			// Weight saturates past the cap logarithmically instead of being cut
			// off. A hard cut made every strong profile identical up there, and
			// the verbosity normalizer below then handed the win to whoever had
			// the thinnest profile overall: the person with one line in a
			// CODEOWNERS file outranked the owner with years of work. Log growth
			// keeps the guarantee both directions: more real evidence never
			// scores less, and no amount of repetition can run away with it.
			if w > termWeightCap {
				w = termWeightCap * (1 + math.Log(w/termWeightCap))
			}
			// The normalizer floors at one: an above-average profile is
			// discounted for verbosity, but a sparse or decayed profile gets
			// no boost, since its raw weight already says how little is there.
			// Past normCap it grows logarithmically rather than stopping, for
			// the reason normCap gives: a hard stop treats somebody who touched
			// a hundred subjects the same as somebody who touched ten thousand,
			// and in a real organization that spread is the whole question. It
			// is the same saturation the term weight above uses, and it keeps
			// the same guarantee: breadth always costs something, and no amount
			// of it divides an owner's evidence away entirely.
			norm := min(max(1, 1-bm25B+bm25B*(lens.byID[id]/lens.avg)), normCap)
			scores[id] += hit.penalty * idf * (w * (bm25K1 + 1)) / (w + bm25K1*norm)
		}
	}
	return scores, matched
}

// appendUnique appends s to list when it is not already present, keeping the
// accumulated field values free of duplicates.
func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// distinct returns terms with duplicates removed, preserving first-seen order,
// so a repeated query token neither double-scores nor deflates coverage.
func distinct(terms []string) []string {
	seen := make(map[string]bool, len(terms))
	out := terms[:0]
	for _, t := range terms {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// reasons describes, for each matched term, which field of the person it hit,
// and returns the strongest evidence among those hits. A fuzzily corrected
// term classifies by its resolved stem and says so.
func (ix *Index) reasons(
	pid model.ID, terms map[string]bool, resolved map[string]termHit, asked map[string]string,
) ([]string, float64) {
	pt := ix.texts[pid]
	p := ix.Graph.People[pid]
	out := make([]string, 0, len(terms))
	var evidence float64
	for term := range terms {
		hit := resolved[term]
		field, strength := "mention", evidenceMention
		var found string
		// take records the word a stem lookup found, so a switch case can
		// classify the hit and capture what it matched in the same test.
		take := func(word string, ok bool) bool {
			if ok {
				found = word
			}
			return ok
		}
		switch {
		case pt != nil && take(stemMatch(hit.key, pt.Topics...)):
			field, strength = "topic", evidenceTopic
		case pt != nil && take(stemMatch(hit.key, pt.Titles...)):
			field, strength = "title", evidenceTitle
		case pt != nil && take(stemMatch(hit.key, pt.Teams...)):
			field, strength = "team", evidenceTeam
		}
		evidence = max(evidence, strength)
		// A synonym arrives with a deliberate discount, which must not read as a
		// typo correction: the "for" clause already says why the word differs
		// from the question.
		if from := asked[term]; from != "" && from != term {
			out = append(out, fmt.Sprintf("%s (%s) for %q", term, field, from))
			continue
		}
		// Knowing a subject best is not the same as still being in it. On a
		// real repository the leading expert of two subjects in five had
		// stopped touching them, and was less than half as likely to still
		// hold the subject six months on, so sending somebody to ask them
		// without saying so is how an answer goes stale.
		if field == "topic" && quietOn(p, found) {
			field += ", not lately"
		}
		// A correction has to name what it corrected to. Told only that
		// "zigby" was fuzzy, a reader cannot tell whether whodar read it as
		// zigbee or as zigzag, which is the difference between a lucky save
		// and a wrong guess presented with confidence.
		if hit.fuzzy() && found != "" && found != term {
			out = append(out, fmt.Sprintf("%s (%s, read for %q)", found, field, term))
			continue
		}
		if hit.fuzzy() {
			field += ", fuzzy"
		}
		out = append(out, fmt.Sprintf("%s (%s)", term, field))
	}
	sort.Strings(out)
	return out, evidence
}

// quietOn reports whether somebody knows a subject but has stopped working on
// it. An empty recent record means the source never said what was recent, so
// nothing is claimed either way.
func quietOn(p *model.Person, topic string) bool {
	if p == nil || len(p.Recent) == 0 || topic == "" {
		return false
	}
	tid := topicID(topic)
	return p.Topics[tid] > 0 && p.Recent[tid] == 0
}

// match records the word a stem lookup found// channelReasons describes, for each matched term, which field of the channel
// it hit, and returns the strongest evidence among those hits. A fuzzily
// corrected term classifies by its resolved stem and says so.
func (ix *Index) channelReasons(
	cid model.ID, terms map[string]bool, resolved map[string]termHit, asked map[string]string,
) ([]string, float64) {
	ct := ix.channelTexts[cid]
	out := make([]string, 0, len(terms))
	var evidence float64
	for term := range terms {
		hit := resolved[term]
		field, strength := "mention", evidenceMention
		var found string
		// take records the word a stem lookup found, so a switch case can
		// classify the hit and capture what it matched in the same test.
		take := func(word string, ok bool) bool {
			if ok {
				found = word
			}
			return ok
		}
		switch {
		case ct != nil && take(stemMatch(hit.key, ct.Topics...)):
			field, strength = "topic", evidenceTopic
		case ct != nil && take(stemMatch(hit.key, ct.Topic)):
			field, strength = "topic", evidenceTopic
		case ct != nil && take(stemMatch(hit.key, ct.Name)):
			field, strength = "name", evidenceTopic
		}
		evidence = max(evidence, strength)
		if from := asked[term]; from != "" && from != term {
			out = append(out, fmt.Sprintf("%s (%s) for %q", term, field, from))
			continue
		}
		// A correction has to name what it corrected to. Told only that
		// "zigby" was fuzzy, a reader cannot tell whether whodar read it as
		// zigbee or as zigzag, which is the difference between a lucky save
		// and a wrong guess presented with confidence.
		if hit.fuzzy() && found != "" && found != term {
			out = append(out, fmt.Sprintf("%s (%s, read for %q)", found, field, term))
			continue
		}
		if hit.fuzzy() {
			field += ", fuzzy"
		}
		out = append(out, fmt.Sprintf("%s (%s)", term, field))
	}
	sort.Strings(out)
	return out, evidence
}

// snapshot is the serializable form of an index written to and read from disk.
type snapshot struct {
	// Graph is the entity graph.
	Graph *model.Graph `json:"graph"`
	// Postings is the per-person inverted index packed as a compact binary blob
	// (JSON stores a byte slice as base64), which is far smaller and faster to
	// read than a map of maps and is the bulk of an index once the source
	// records move to the sidecar.
	Postings []byte `json:"postings"`
	// Texts holds normalized per-person field text.
	Texts map[model.ID]*personText `json:"texts"`
	// ChannelPostings is the per-channel inverted index, packed the same way.
	ChannelPostings []byte `json:"channel_postings"`
	// ChannelTexts holds normalized per-channel field text.
	ChannelTexts map[model.ID]*channelText `json:"channel_texts"`
	// PersonVecs holds per-person embedding vectors, quantized to int8 (JSON
	// stores each as a small number array), a quarter the size of float32.
	PersonVecs map[model.ID][]int8 `json:"person_vecs,omitempty"`
	// ChannelVecs holds per-channel embedding vectors, quantized the same way.
	ChannelVecs map[model.ID][]int8 `json:"channel_vecs,omitempty"`
	// TopicVecs holds per-topic embedding vectors, quantized the same way.
	TopicVecs map[model.ID][]int8 `json:"topic_vecs,omitempty"`
	// Aliases maps each known alias identifier to its canonical form.
	Aliases map[model.ID]model.ID `json:"aliases,omitempty"`
	// Joins records the inferred identity merges with their confidence and
	// evidence, so a re-index keeps them and a reader can audit why two
	// identities became one.
	Joins []Join `json:"joins,omitempty"`
	// SourceCounts is how many records each source contributed. The records
	// themselves live in a sidecar file so a query never loads them; only their
	// counts stay here for status and the shrink guard.
	SourceCounts map[string]int `json:"source_counts,omitempty"`
	// BuiltAt is when the index was last written, so a reader can tell how stale
	// it is without re-running an index.
	BuiltAt time.Time `json:"built_at,omitempty"`
}

// sourcesSnapshot is the sidecar that holds the raw records per source, read
// only when a merge needs to rebuild. Keeping it out of the main index is what
// lets a query load a fraction of the bytes, since the records are the bulk of
// an index and no query reads them.
type sourcesSnapshot struct {
	// Sources holds the records each source contributed, so a later merge can
	// replace one source without re-reading the others.
	Sources map[string][]connector.Record `json:"sources,omitempty"`
}

// Option configures Load and Save. With no option the index is read and written
// as plain JSON; WithCodec injects an at-rest codec so the file is encrypted.
type Option func(*ioConfig)

// ioConfig holds the resolved options for one Load or Save.
type ioConfig struct {
	// codec transforms the bytes at rest; Plain by default.
	codec vault.Codec
}

// newIOConfig applies opts over the plain-JSON default.
func newIOConfig(opts []Option) ioConfig {
	cfg := ioConfig{codec: vault.Plain{}}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithCodec sets the at-rest codec for a Load or Save. A nil codec is ignored,
// leaving the plain-JSON default.
func WithCodec(c vault.Codec) Option {
	return func(cfg *ioConfig) {
		if c != nil {
			cfg.codec = c
		}
	}
}

// Save writes the index to path readable only by the owner (mode 0600), creating
// parent directories as needed. It is compact JSON, or its encrypted form when
// WithCodec is set, and each write goes through a temporary file and a rename so
// a crash cannot truncate an existing file. The raw source records, which are
// the bulk of an index and which no query reads, go to a sidecar file next to
// path so a query loads only the small main index.
func (ix *Index) Save(path string, opts ...Option) error {
	cfg := newIOConfig(opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("index: mkdir: %w", err)
	}
	snap := snapshot{
		Graph:           ix.Graph,
		Postings:        invindex.EncodePostings(ix.postings),
		Texts:           ix.texts,
		ChannelPostings: invindex.EncodePostings(ix.channelPostings),
		ChannelTexts:    ix.channelTexts,
		PersonVecs:      quantizeVecs(ix.personVecs),
		ChannelVecs:     quantizeVecs(ix.channelVecs),
		TopicVecs:       quantizeVecs(ix.topicVecs),
		Aliases:         ix.identityResolver().Pairs(),
		Joins:           ix.joins,
		SourceCounts:    ix.sourceCounts,
		BuiltAt:         ix.now(),
	}
	if err := writeEncoded(path, snap, cfg.codec); err != nil {
		return err
	}
	// Write the sources sidecar only when the sources are in hand. An index
	// loaded to answer a query carries none, and overwriting the sidecar then
	// would erase the records a later merge needs; leaving it keeps them.
	if ix.sources != nil {
		side := sourcesSnapshot{Sources: redactedSources(ix.sources)}
		if err := writeEncoded(sourcesPath(path), side, cfg.codec); err != nil {
			return err
		}
	}
	return nil
}

// writeEncoded marshals v to JSON, encodes it with the codec, and writes it
// atomically at mode 0600.
func writeEncoded(path string, v any, codec vault.Codec) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("index: encode %s: %w", filepath.Base(path), err)
	}
	enc, err := codec.Encode(raw)
	if err != nil {
		return fmt.Errorf("index: encrypt %s: %w", filepath.Base(path), err)
	}
	if err := util.WriteFileAtomic(path, enc, 0o600); err != nil {
		return fmt.Errorf("index: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// sourcesPath is the sidecar path for an index at path: the same name with a
// .sources segment inserted, so the two files travel together.
func sourcesPath(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".sources" + ext
}

// Load reads an index previously written by Save, decrypting it when WithCodec
// supplies the key. It returns vault.ErrEncrypted when the file is encrypted but
// no codec is given, so a caller can prompt for a passphrase.
func Load(path string, opts ...Option) (*Index, error) {
	cfg := newIOConfig(opts)
	stored, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("index: open: %w", err)
	}
	raw, err := cfg.codec.Decode(stored)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("index: decode: %w", err)
	}
	postings, err := invindex.DecodePostings[model.ID](snap.Postings)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	channelPostings, err := invindex.DecodePostings[model.ID](snap.ChannelPostings)
	if err != nil {
		return nil, fmt.Errorf("index: channel %w", err)
	}
	ix := &Index{
		Graph:           snap.Graph,
		postings:        postings,
		texts:           snap.Texts,
		channelPostings: channelPostings,
		channelTexts:    snap.ChannelTexts,
		personVecs:      dequantizeVecs(snap.PersonVecs),
		channelVecs:     dequantizeVecs(snap.ChannelVecs),
		topicVecs:       dequantizeVecs(snap.TopicVecs),
		// sources stays nil: a loaded index answers queries, which never read
		// the records. A merge calls LoadSources to bring them in from the
		// sidecar before rebuilding.
		sourceCounts: snap.SourceCounts,
		builtAt:      snap.BuiltAt,
		resolver:     identity.NewResolver(),
		joins:        snap.Joins,
		halfLife:     DefaultHalfLife,
		now:          time.Now,
	}
	ix.resolver.Restore(snap.Aliases)
	if ix.Graph == nil {
		ix.Graph = model.NewGraph()
	}
	if ix.Graph.People == nil {
		ix.Graph.People = make(map[model.ID]*model.Person)
	}
	if ix.Graph.Teams == nil {
		ix.Graph.Teams = make(map[model.ID]*model.Team)
	}
	if ix.Graph.Orgs == nil {
		ix.Graph.Orgs = make(map[model.ID]*model.Org)
	}
	if ix.Graph.Topics == nil {
		ix.Graph.Topics = make(map[model.ID]*model.Topic)
	}
	if ix.Graph.Channels == nil {
		ix.Graph.Channels = make(map[model.ID]*model.Channel)
	}
	if ix.postings == nil {
		ix.postings = make(map[string]map[model.ID]float64)
	}
	if ix.texts == nil {
		ix.texts = make(map[model.ID]*personText)
	}
	if ix.channelPostings == nil {
		ix.channelPostings = make(map[string]map[model.ID]float64)
	}
	if ix.channelTexts == nil {
		ix.channelTexts = make(map[model.ID]*channelText)
	}
	if ix.personVecs == nil {
		ix.personVecs = make(map[model.ID][]float32)
	}
	if ix.topicVecs == nil {
		ix.topicVecs = make(map[model.ID][]float32)
	}
	if ix.channelVecs == nil {
		ix.channelVecs = make(map[model.ID][]float32)
	}
	ix.refreshStats()
	return ix, nil
}

// LoadSources reads the sources sidecar for the index at path into the index, so
// a merge can rebuild from every source and not just the one it is adding.
// Merging into a loaded index must call this first: without the records a
// rebuild would drop every source read before. A missing sidecar is an error
// rather than an empty set, since a silent shrink is exactly what this guards.
func (ix *Index) LoadSources(path string, opts ...Option) error {
	cfg := newIOConfig(opts)
	stored, err := os.ReadFile(sourcesPath(path))
	if err != nil {
		return fmt.Errorf("index: open sources: %w", err)
	}
	raw, err := cfg.codec.Decode(stored)
	if err != nil {
		return fmt.Errorf("index: sources: %w", err)
	}
	var side sourcesSnapshot
	if err := json.Unmarshal(raw, &side); err != nil {
		return fmt.Errorf("index: decode sources: %w", err)
	}
	ix.sources = side.Sources
	if ix.sources == nil {
		ix.sources = make(map[string][]connector.Record)
	}
	if ix.sourceCounts == nil {
		ix.sourceCounts = make(map[string]int)
	}
	for name, recs := range ix.sources {
		ix.sourceCounts[name] = len(recs)
	}
	return nil
}

// personID resolves a stable identifier for a record, preferring an explicit
// id, then email, then a slug of the name.
// identityKey normalizes an alternate identifier for the resolver: an email is
// folded like any other email, a role mailbox is dropped, and anything else is
// lowercased and trimmed.
func identityKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "@") {
		if util.IsRoleEmail(s) {
			return ""
		}
		return util.NormalizeEmail(s)
	}
	return strings.ToLower(s)
}

func personID(rec connector.Record) model.ID {
	switch {
	case rec.PersonID != "":
		return model.ID(strings.ToLower(strings.TrimSpace(rec.PersonID)))
	case rec.Email != "":
		return model.ID(util.NormalizeEmail(rec.Email))
	case rec.Name != "":
		return model.ID(slug(rec.Name))
	default:
		return ""
	}
}

// topicID returns the canonical identifier for a topic name, folding synonyms
// and abbreviations onto one topic so the same concept is not split across forms.
func topicID(name string) model.ID {
	return model.ID(canonicalTopic(name))
}

// betterName reports whether name should replace current. A handle-like
// placeholder ("@login", "jira:accountid") never replaces a real name.
func betterName(current, name string) bool {
	if name == "" {
		return false
	}
	if current == "" {
		return true
	}
	return !strings.HasPrefix(name, "@") && !strings.Contains(name, ":")
}

// fillIdentity copies non-empty identity fields from rec onto p.
func fillIdentity(p *model.Person, rec connector.Record) {
	if betterName(p.Name, rec.Name) {
		p.Name = rec.Name
	}
	if rec.Email != "" {
		p.Email = rec.Email
	}
	if rec.Title != "" {
		p.Title = rec.Title
	}
	if rec.Manager != "" {
		p.ManagerID = model.ID(strings.ToLower(rec.Manager))
	}
}

// statesOwnership reports whether a source assigns subjects to people by
// declaration rather than by evidence of work. A CODEOWNERS file and an org
// chart's topics column both say who is responsible for an area; neither says
// anybody has touched it.
func statesOwnership(source string) bool {
	return source == "codeowners" || source == "org-csv"
}

// buildTopicLinks records which subjects a source saw worked on together. The
// subject itself is not established by this: being changed alongside something
// is not the same as somebody declaring it, so a subject that appears only here
// stays unsalient until a person holds it.
func (ix *Index) buildTopicLinks(rec connector.Record) {
	tid := topicID(rec.Name)
	if tid == "" || len(rec.Links) == 0 {
		return
	}
	g := ix.Graph
	topic := g.Topics[tid]
	if topic == nil {
		topic = &model.Topic{ID: tid, Name: strings.ToLower(rec.Name)}
		g.Topics[tid] = topic
	}
	if topic.Near == nil {
		topic.Near = make(map[model.ID]model.Tie)
	}
	for _, link := range rec.Links {
		to := topicID(link.To)
		if to == "" || to == tid || link.Weight <= 0 {
			continue
		}
		// Sources disagree about how strongly two subjects are tied; keep the
		// strongest claim rather than letting a weak one dilute it.
		if link.Weight > topic.Near[to].Weight {
			// A source names the person by whatever address the work carried,
			// which is not always how the graph keys them: a provider's
			// no-reply address encodes a login the connector keys people by.
			// Normalized here, the tie points at somebody the graph can name.
			sole := model.ID("")
			if link.Sole != "" {
				sole = model.ID(util.NormalizeEmail(link.Sole))
			}
			topic.Near[to] = model.Tie{
				Weight:    link.Weight,
				Witnesses: link.Witnesses,
				Sole:      sole,
			}
		}
	}
}

// linkOrg attaches the person to their team and organization, creating those
// entities in the graph when first seen.
func linkOrg(g *model.Graph, p *model.Person, rec connector.Record) {
	var orgID model.ID
	if rec.Org != "" {
		orgID = model.ID(slug(rec.Org))
		if g.Orgs[orgID] == nil {
			g.Orgs[orgID] = &model.Org{ID: orgID, Name: rec.Org}
		}
		p.OrgID = orgID
	}
	if rec.Team != "" {
		teamID := model.ID(slug(rec.Team))
		if g.Teams[teamID] == nil {
			g.Teams[teamID] = &model.Team{ID: teamID, Name: rec.Team}
		}
		if orgID != "" {
			g.Teams[teamID].OrgID = orgID
		}
		p.TeamID = teamID
	}
}
