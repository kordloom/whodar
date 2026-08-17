package index

import (
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/model"
)

// CanonicalizeEpisodes resolves each conversation's participants to the people
// the graph knows. A change reviewed under a GitHub login, a ticket closed
// under a Jira account, and a Slack thread all name the same person once this
// has run, which is what lets recall find a person's work wherever it
// happened. Participants that collapse onto one person are deduplicated, and
// the order they were seen in is kept.
func (ix *Index) CanonicalizeEpisodes(eps []episode.Episode) {
	for i := range eps {
		participants := eps[i].Participants
		out := participants[:0]
		seen := make(map[model.ID]bool, len(participants))
		for _, p := range participants {
			id := ix.Canonical(p)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		eps[i].Participants = out
	}
}
