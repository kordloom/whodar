package resolve

import (
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// relatedCut is the share of a topic's experts that must also hold another topic
// before the two count as related. It is deliberately high: a loose threshold
// relates everything to everything in a company where a few people are active
// across many subjects.
const relatedCut = 0.6

// TopicRelation is one topic that tends to be held by the same people as
// another, which is how a subject and its specialties are recognized without a
// taxonomy anyone had to write.
type TopicRelation struct {
	// Topic is the related topic.
	Topic string `json:"topic"`
	// Overlap is the share of the source topic's experts who also hold this one,
	// 0 to 1.
	Overlap float64 `json:"overlap"`
	// Narrower marks a topic that looks like a specialty of the source topic:
	// its experts are a subset, and there are fewer of them.
	Narrower bool `json:"narrower"`
}

// Related returns the topics whose experts substantially overlap with topic's,
// strongest first. Overlap is computed over the people who hold each topic, so
// the result reflects who actually does the work rather than what the words look
// like. A limit of zero or less returns all of them.
func Related(ix *index.Index, topic string, limit int) []TopicRelation {
	holders := topicHolders(ix)
	want := holders[strings.ToLower(strings.TrimSpace(topic))]
	if len(want) == 0 {
		return nil
	}
	var out []TopicRelation
	for other, people := range holders {
		if other == strings.ToLower(strings.TrimSpace(topic)) || len(people) == 0 {
			continue
		}
		shared := 0
		for id := range people {
			if want[id] {
				shared++
			}
		}
		if shared == 0 {
			continue
		}
		overlap := float64(shared) / float64(len(people))
		if overlap < relatedCut {
			continue
		}
		out = append(out, TopicRelation{
			Topic:    other,
			Overlap:  overlap,
			Narrower: len(people) < len(want),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Overlap != out[j].Overlap {
			return out[i].Overlap > out[j].Overlap
		}
		return out[i].Topic < out[j].Topic
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// topicHolders maps each salient topic to the set of people with real affinity
// for it, which is the evidence every relation is computed from.
func topicHolders(ix *index.Index) map[string]map[model.ID]bool {
	out := make(map[string]map[model.ID]bool)
	for id, p := range ix.Graph.People {
		for tid, w := range p.Topics {
			if w <= 0 || !ix.Graph.Topics[tid].Salient() {
				continue
			}
			key := string(tid)
			if out[key] == nil {
				out[key] = make(map[model.ID]bool)
			}
			out[key][id] = true
		}
	}
	return out
}
