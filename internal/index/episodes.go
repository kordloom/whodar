package index

import (
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/model"
)

// CanonicalizeStore re-resolves every stored conversation's participants
// against the graph as it stands now. Identities join over time, so a
// conversation recorded before a person's handle was linked to their email
// would otherwise stay unreachable to them.
func (ix *Index) CanonicalizeStore(store *episode.Store) {
	for _, ep := range store.All() {
		before := append([]model.ID(nil), ep.Participants...)
		one := []episode.Episode{*ep}
		ix.CanonicalizeEpisodes(one)
		if !sameIDs(before, one[0].Participants) {
			store.Relink(ep.ID, one[0].Participants)
		}
	}
}

// sameIDs reports whether two participant lists match.
func sameIDs(a, b []model.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

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
