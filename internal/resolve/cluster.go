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

// mergeCut is the share of a fragment topic's experts who must also hold the
// compound topic before the fragment folds into it.
const mergeCut = 0.6

// topicGroups maps every salient topic to the topic that represents its group,
// folding a fragment into the compound it belongs to: "billing" and "retries"
// into "billing-retries" when the same people hold all three. Tokenizing text
// into topics splits a compound subject into its words, and reporting each of
// them as a separate subject counts one body of knowledge several times.
//
// A fold needs two things to agree, because either alone is wrong. Sharing
// experts is not enough: one person's unrelated topics overlap completely, and
// merging on that would fuse every subject a lone expert happens to hold.
// Sharing words is not enough either: "kubernetes" is genuinely broader than
// "kubernetes-deploys" and keeps its own experts. Only a topic that is both a
// word of the compound and held by the compound's people is the same subject
// said shorter. A topic that maps to itself is its own group.
func topicGroups(ix *index.Index) map[string]string {
	holders := topicHolders(ix)
	names := make([]string, 0, len(holders))
	for t := range holders {
		names = append(names, t)
	}
	sort.Strings(names)

	// Only a topic containing one of a fragment's words can absorb it, so index
	// the compounds by word and compare against those instead of every pair. A
	// real organization has thousands of topics, where a full pairwise scan would
	// cost millions of comparisons to find the same handful of folds.
	words := make(map[string][]string, len(names))
	byWord := make(map[string][]string)
	for _, t := range names {
		w := strings.Split(t, "-")
		words[t] = w
		if len(w) > 1 {
			for _, one := range w {
				byWord[one] = append(byWord[one], t)
			}
		}
	}

	parent := make(map[string]string, len(names))
	for _, a := range names {
		aWords := words[a]
		if len(holders[a]) == 0 {
			continue
		}
		var best string
		bestOverlap, bestWords := 0.0, 0
		seen := make(map[string]bool)
		for _, b := range byWord[aWords[0]] {
			if seen[b] {
				continue
			}
			seen[b] = true
			bWords := words[b]
			if a == b || len(bWords) <= len(aWords) || !wordSubset(aWords, bWords) {
				continue
			}
			shared := 0
			for id := range holders[a] {
				if holders[b][id] {
					shared++
				}
			}
			overlap := float64(shared) / float64(len(holders[a]))
			if overlap < mergeCut {
				continue
			}
			// Strongest overlap wins, then the nearest compound, so a fragment
			// folds into the phrase it belongs to rather than a longer one that
			// merely contains it.
			if best == "" || overlap > bestOverlap ||
				(overlap == bestOverlap && len(bWords) < bestWords) {
				best, bestOverlap, bestWords = b, overlap, len(bWords)
			}
		}
		if best != "" {
			parent[a] = best
		}
	}

	// Token-set inclusion is a strict partial order, so the fold graph has no
	// cycles and every chain ends. Walk each one to its root.
	out := make(map[string]string, len(names))
	for _, a := range names {
		root := a
		for range len(names) {
			next, ok := parent[root]
			if !ok {
				break
			}
			root = next
		}
		out[a] = root
	}
	return out
}

// wordSubset reports whether every word of a appears in b.
func wordSubset(a, b []string) bool {
	in := make(map[string]bool, len(b))
	for _, w := range b {
		in[w] = true
	}
	for _, w := range a {
		if !in[w] {
			return false
		}
	}
	return true
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
