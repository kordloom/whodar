package resolve

import (
	"sort"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/util"
)

// Span is a connection between two subjects that only one person has ever made.
//
// It is a different risk from a subject resting on one expert, and nothing that
// counts experts per subject can see it. Both areas may be well covered on
// their own while the work that crosses between them has only ever been done by
// one person, so what leaves with them is not a subject but the knowledge that
// the two belong together. The person picking either one up afterwards has no
// reason to look at the other.
type Span struct {
	// Topic and With are the two subjects, alphabetically.
	Topic string `json:"topic"`
	// With is the other subject.
	With string `json:"with"`
	// Person is the display name of the only person who has worked across both.
	Person string `json:"person"`
	// PersonID is that person's canonical identifier.
	PersonID string `json:"personId"`
	// Together is how much of the time the two subjects change as one thing.
	Together float64 `json:"together"`
	// Experts is how many people hold the two subjects between them, which is
	// what makes the finding surprising: the subjects are not short of experts.
	Experts int `json:"experts"`
}

// maxSpanRank is how far down a subject's ties a connection may sit and still
// be worth reporting. It must be among the strongest for BOTH subjects.
//
// This is a rank test rather than a floor on the weight, and deliberately so: a
// floor set by eye cuts the real ties along with the noise, because how much of
// the time two subjects move as one is a tiny number whenever both are also
// worked on alone. Rank is free of that. Measured on a real issue tracker it
// cut sixteen reported connections to seven and every one it dropped was noise:
// a weak tie between two broad subjects, where one person happening to be the
// only one who crossed says nothing about either.
const maxSpanRank = 3

// SoleSpans finds the connections between subjects that rest on one person,
// strongest first. A limit of zero or less returns all of them.
//
// Every surface that names a person has to be checked against the people who
// touch everything, who otherwise take every answer at once. This one is
// naturally resistant and was measured to be: on a real project the widest
// contributor, with fifteen thousand subjects to his name, is the sole witness
// to nothing at all. Being the only person who crossed between two areas is not
// a ranking, and somebody who touches everything has company everywhere, so the
// sweepers rule themselves out. Keep that property when changing this.
func SoleSpans(ix *index.Index, limit int) []Span {
	holders := topicHolders(ix)
	seen := make(map[[2]string]bool)
	var out []Span
	for tid, topic := range ix.Graph.Topics {
		if !topic.Salient() {
			continue
		}
		for other, tie := range topic.Near {
			if tie.Witnesses != 1 || tie.Sole == "" {
				continue
			}
			o := ix.Graph.Topics[other]
			if o == nil || !o.Salient() {
				continue
			}
			if tieRank(topic, other) > maxSpanRank || tieRank(o, tid) > maxSpanRank {
				continue
			}
			a, b := string(tid), string(other)
			if a > b {
				a, b = b, a
			}
			key := [2]string{a, b}
			if seen[key] {
				continue
			}
			seen[key] = true

			who := ix.CanonicalID(tie.Sole)
			// Both subjects having experts is the point: this is not a finding
			// about a subject nobody knows.
			people := make(map[model.ID]bool, len(holders[a])+len(holders[b]))
			for id := range holders[a] {
				people[id] = true
			}
			for id := range holders[b] {
				people[id] = true
			}
			out = append(out, Span{
				Topic: a, With: b, Person: personName(ix, who), PersonID: string(who),
				Together: tie.Weight, Experts: len(people),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Together != out[j].Together {
			return out[i].Together > out[j].Together
		}
		// A more specific pairing comes first, so collapsing below keeps the
		// one that names the subjects most exactly.
		ni, nj := len(out[i].Topic)+len(out[i].With), len(out[j].Topic)+len(out[j].With)
		if ni != nj {
			return ni > nj
		}
		if out[i].Topic != out[j].Topic {
			return out[i].Topic < out[j].Topic
		}
		return out[i].With < out[j].With
	})
	out = collapseSpans(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// collapseSpans drops a finding that restates one already reported.
//
// A subject name that reads as several words registers as those words as well
// as the whole, so one connection between two areas arrives once per fragment:
// api with image-uploads, api with image, and api with uploads are a single
// thing said three times. Keeping only the most specific of a family says it
// once, which is the difference between a report somebody reads and a list they
// skim past.
func collapseSpans(spans []Span) []Span {
	out := make([]Span, 0, len(spans))
	for _, s := range spans {
		restated := false
		for _, kept := range out {
			if sameSpan(kept, s) {
				restated = true
				break
			}
		}
		if !restated {
			out = append(out, s)
		}
	}
	return out
}

// sameSpan reports whether two findings name the same pair of areas. The pair
// is unordered, and either side may arrive as a fragment of its own name, so
// both orientations have to be tried.
func sameSpan(a, b Span) bool {
	if util.SameFamily(a.Topic, b.Topic) && util.SameFamily(a.With, b.With) {
		return true
	}
	return util.SameFamily(a.Topic, b.With) && util.SameFamily(a.With, b.Topic)
}

// tieRank is where other sits among a topic's ties, strongest first, counting
// from one. A subject not tied to it at all ranks last of all.
func tieRank(t *model.Topic, other model.ID) int {
	rank := 1
	w := t.Near[other].Weight
	if w <= 0 {
		return len(t.Near) + 1
	}
	for id, tie := range t.Near {
		// Ties of equal weight are ordered by name, so the rank of a given
		// subject does not depend on map order.
		if tie.Weight > w || (tie.Weight == w && id < other) {
			rank++
		}
	}
	return rank
}
