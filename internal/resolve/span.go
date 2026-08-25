package resolve

import (
	"sort"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
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

// SoleSpans finds the connections between subjects that rest on one person,
// strongest first. A limit of zero or less returns all of them.
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
			if o := ix.Graph.Topics[other]; o == nil || !o.Salient() {
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
		if out[i].Topic != out[j].Topic {
			return out[i].Topic < out[j].Topic
		}
		return out[i].With < out[j].With
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
