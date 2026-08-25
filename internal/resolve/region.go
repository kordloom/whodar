package resolve

import (
	"math"
	"sort"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// Region is a connected body of work resting on one person: subjects tied to
// each other because they are changed together, where the same person leads
// every one of them.
//
// It is a different finding from a subject with a bus factor of one, and a
// worse one. Ten unrelated subjects each held by one person are ten small
// risks. Ten subjects that change together and are all led by the same person
// are one large risk, because whoever picks the work up has to learn the whole
// region at once, and nothing in a per-subject report shows that.
type Region struct {
	// Lead is the display name of the person who leads every subject in it.
	Lead string `json:"lead"`
	// LeadID is that person's canonical identifier.
	LeadID string `json:"leadId"`
	// Topics are the connected subjects, alphabetically.
	Topics []string `json:"topics"`
}

// Size is how many subjects the region spans.
func (r Region) Size() int { return len(r.Topics) }

// minRegion is the fewest joined subjects worth reporting as a region. Two is
// a pair, which per-subject risk already shows well enough.
const minRegion = 3

// Regions finds the connected bodies of work that rest on a single person,
// largest first. A limit of zero or less returns all of them.
func Regions(ix *index.Index, limit int) []Region {
	lead := make(map[model.ID]model.ID)
	for tid, topic := range ix.Graph.Topics {
		if !topic.Salient() {
			continue
		}
		if who := leadOf(ix, tid); who != "" {
			lead[tid] = who
		}
	}

	// Adjacency between subjects, kept to those a person actually leads so the
	// walk below only crosses ties inside one person's work.
	near := make(map[model.ID][]model.ID, len(lead))
	for tid := range lead {
		for other := range ix.Graph.Topics[tid].Near {
			if _, ok := lead[other]; ok {
				near[tid] = append(near[tid], other)
			}
		}
	}

	ids := make([]model.ID, 0, len(lead))
	for tid := range lead {
		ids = append(ids, tid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var out []Region
	seen := make(map[model.ID]bool, len(ids))
	for _, start := range ids {
		if seen[start] {
			continue
		}
		who := lead[start]
		// Walk outwards while the same person still leads, so a region stops at
		// the edge of what one person holds rather than running through the
		// whole graph.
		group := []model.ID{start}
		seen[start] = true
		for i := 0; i < len(group); i++ {
			for _, next := range near[group[i]] {
				if seen[next] || lead[next] != who {
					continue
				}
				seen[next] = true
				group = append(group, next)
			}
		}
		if len(group) < minRegion {
			continue
		}
		names := make([]string, 0, len(group))
		for _, tid := range group {
			names = append(names, string(tid))
		}
		sort.Strings(names)
		out = append(out, Region{Lead: personName(ix, who), LeadID: string(who), Topics: names})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Topics) != len(out[j].Topics) {
			return len(out[i].Topics) > len(out[j].Topics)
		}
		return out[i].Topics[0] < out[j].Topics[0]
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// leadOf names the person a subject rests on. It is the same judgement
// ownership drift makes, and deliberately the same arithmetic: raw weight hands
// every subject in a code base to whoever touches everything, which turned a
// maintainer's own region over to a passer-by in four separate places before
// this was written down once. Weight is discounted by the square root of
// everything that person does, and only work counts, never a subject a source
// of record assigned them.
func leadOf(ix *index.Index, topic model.ID) model.ID {
	var best model.ID
	bestScore := 0.0
	for id, p := range ix.Graph.People {
		here := p.Topics[topic] - p.Stated[topic]
		if here <= 0 {
			continue
		}
		var total float64
		for tid, w := range p.Topics {
			if work := w - p.Stated[tid]; work > 0 {
				total += work
			}
		}
		if total <= 0 {
			continue
		}
		score := here / math.Sqrt(total)
		if score > bestScore || (score == bestScore && id < best) {
			best, bestScore = id, score
		}
	}
	if best == "" {
		return ""
	}
	return ix.CanonicalID(best)
}
