package episode

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/text"
	"github.com/kordloom/whodar/internal/vector"
)

// DefaultHalfLife is the age at which an episode's score halves. Recall
// reaches back months, so it decays more slowly than a question about who
// works on something now.
const DefaultHalfLife = 365 * 24 * time.Hour

// saturation bounds how much repetition one term can contribute to a single
// episode, so a word said thirty times in a long thread does not outrank the
// thread that is actually about it.
const saturation = 3.0

// Store holds episodes and the inverted index over their text. It lives in its
// own file beside the main index, so a question about who knows something
// never pays to load conversation history.
type Store struct {
	// episodes maps an episode ID to the episode.
	episodes map[string]*Episode
	// postings maps a term to per-episode accumulated weight.
	postings map[string]map[string]float64
	// byParticipant maps a person to the episodes they took part in. It is
	// derived on load rather than stored.
	byParticipant map[model.ID][]string
	// vecs holds per-episode embedding vectors when present.
	vecs map[string][]float32
	// halfLife is the age at which a score halves; zero uses the default.
	halfLife time.Duration
	// now returns the current time; tests pin it for deterministic decay.
	now func() time.Time
}

// New returns an empty store.
func New() *Store {
	return &Store{
		episodes:      make(map[string]*Episode),
		postings:      make(map[string]map[string]float64),
		byParticipant: make(map[model.ID][]string),
		vecs:          make(map[string][]float32),
		now:           time.Now,
	}
}

// Len reports how many episodes the store holds.
func (s *Store) Len() int { return len(s.episodes) }

// Episode returns the episode with the given ID.
func (s *Store) Episode(id string) (*Episode, bool) {
	ep, ok := s.episodes[id]
	return ep, ok
}

// All returns every episode, ordered newest first. Callers must not mutate the
// returned episodes.
func (s *Store) All() []*Episode {
	out := make([]*Episode, 0, len(s.episodes))
	for _, ep := range s.episodes {
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurred.Equal(out[j].Occurred) {
			return out[i].ID < out[j].ID
		}
		return out[i].Occurred.After(out[j].Occurred)
	})
	return out
}

// SetHalfLife sets the age at which a score halves. Zero restores the default;
// a negative duration turns decay off, so an old conversation ranks purely on
// how well it matches.
func (s *Store) SetHalfLife(d time.Duration) { s.halfLife = d }

// Add stores an episode and indexes its body. Adding an episode that is
// already present replaces it, so re-indexing a source is idempotent. The body
// is tokenized and dropped; only the resulting terms are kept.
func (s *Store) Add(ep Episode) {
	if ep.ID == "" {
		return
	}
	if old, ok := s.episodes[ep.ID]; ok {
		// Re-indexing must never quietly throw away kept content. A run
		// without the archive reports the same conversation with no words
		// attached, and taking that at face value would delete history the
		// source may no longer serve. Only `archive prune` deletes.
		if len(ep.Archive) == 0 && len(old.Archive) > 0 {
			ep.Archive = old.Archive
		}
		s.forget(old)
	}
	body := ep.Body
	stored := ep
	stored.Body = ""
	s.episodes[ep.ID] = &stored
	for _, p := range stored.Participants {
		s.byParticipant[p] = append(s.byParticipant[p], stored.ID)
	}
	for _, term := range text.Terms(strings.TrimSpace(body + " " + stored.Text())) {
		posting := s.postings[term]
		if posting == nil {
			posting = make(map[string]float64)
			s.postings[term] = posting
		}
		posting[stored.ID]++
	}
}

// SetVector attaches an embedding vector to an episode.
func (s *Store) SetVector(id string, vec []float32) {
	if _, ok := s.episodes[id]; ok && len(vec) > 0 {
		s.vecs[id] = vec
	}
}

// Vector returns the embedding vector for an episode.
func (s *Store) Vector(id string) ([]float32, bool) {
	v, ok := s.vecs[id]
	return v, ok
}

// forget removes an episode's postings and participant links, so replacing it
// cannot leave stale terms pointing at it.
func (s *Store) forget(ep *Episode) {
	for term, posting := range s.postings {
		delete(posting, ep.ID)
		if len(posting) == 0 {
			delete(s.postings, term)
		}
	}
	for _, p := range ep.Participants {
		ids := s.byParticipant[p]
		kept := ids[:0]
		for _, id := range ids {
			if id != ep.ID {
				kept = append(kept, id)
			}
		}
		if len(kept) == 0 {
			delete(s.byParticipant, p)
			continue
		}
		s.byParticipant[p] = kept
	}
	delete(s.vecs, ep.ID)
	delete(s.episodes, ep.ID)
}

// Query asks for episodes matching text, optionally narrowed to one person.
type Query struct {
	// Text is the question, matched against episode terms.
	Text string
	// Person narrows results to episodes that person took part in. Recall for
	// an individual always sets it; an archive search across the organization
	// leaves it empty.
	Person model.ID
	// Limit caps results; zero means five.
	Limit int
}

// Result is one ranked episode with the terms that matched it.
type Result struct {
	// Episode is the matched episode.
	Episode *Episode
	// Score ranks the result.
	Score float64
	// Confidence is the share of query terms this episode matched, from zero
	// to one.
	Confidence float64
	// Matched lists the query terms found in the episode.
	Matched []string
}

// Search ranks episodes for a query. Scoring is term overlap, saturated so
// repetition cannot dominate, scaled by how much of the question an episode
// covers and by how recently it happened.
func (s *Store) Search(q Query) []Result {
	terms := text.Terms(q.Text)
	if len(terms) == 0 {
		return nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}
	var scope map[string]bool
	if q.Person != "" {
		ids := s.byParticipant[q.Person]
		if len(ids) == 0 {
			return nil
		}
		scope = make(map[string]bool, len(ids))
		for _, id := range ids {
			scope[id] = true
		}
	}

	type acc struct {
		score   float64
		matched []string
	}
	hits := make(map[string]*acc)
	seen := make(map[string]bool, len(terms))
	unique := 0
	for _, term := range terms {
		if seen[term] {
			continue
		}
		seen[term] = true
		unique++
		for id, weight := range s.postings[term] {
			if scope != nil && !scope[id] {
				continue
			}
			a := hits[id]
			if a == nil {
				a = &acc{}
				hits[id] = a
			}
			a.score += weight / (weight + saturation)
			a.matched = append(a.matched, term)
		}
	}

	out := make([]Result, 0, len(hits))
	for id, a := range hits {
		ep := s.episodes[id]
		if ep == nil {
			continue
		}
		coverage := float64(len(a.matched)) / float64(unique)
		sort.Strings(a.matched)
		out = append(out, Result{
			Episode:    ep,
			Score:      a.score * coverage * s.decay(ep.Occurred),
			Confidence: coverage,
			Matched:    a.matched,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Episode.ID < out[j].Episode.ID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// SearchSemantic ranks episodes by how close their meaning is to a query
// vector, which finds a conversation whose words you no longer remember. It
// returns nothing when no episode was embedded.
func (s *Store) SearchSemantic(query []float32, q Query) []Result {
	if len(query) == 0 || len(s.vecs) == 0 {
		return nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}
	var scope map[string]bool
	if q.Person != "" {
		ids := s.byParticipant[q.Person]
		if len(ids) == 0 {
			return nil
		}
		scope = make(map[string]bool, len(ids))
		for _, id := range ids {
			scope[id] = true
		}
	}
	out := make([]Result, 0, len(s.vecs))
	for id, vec := range s.vecs {
		if scope != nil && !scope[id] {
			continue
		}
		ep := s.episodes[id]
		if ep == nil {
			continue
		}
		similarity := vector.Cosine(query, vec)
		if similarity <= 0 {
			continue
		}
		out = append(out, Result{
			Episode:    ep,
			Score:      similarity * s.decay(ep.Occurred),
			Confidence: similarity,
			Matched:    []string{"similar meaning"},
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Episode.ID < out[j].Episode.ID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// HasVectors reports whether any episode was embedded, which is what makes
// semantic recall possible.
func (s *Store) HasVectors() bool { return len(s.vecs) > 0 }

// decay returns the recency multiplier for an episode, halving every
// half-life. Undated episodes never decay.
func (s *Store) decay(at time.Time) float64 {
	half := s.halfLife
	if half == 0 {
		half = DefaultHalfLife
	}
	if half <= 0 || at.IsZero() {
		return 1
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	age := now().Sub(at)
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age.Seconds()/half.Seconds())
}
