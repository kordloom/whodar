package index

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/model"
)

// Changes describes which people, teams, and channels entered or left the graph
// between a previous index and a new one.
type Changes struct {
	// PeopleJoined are people present now but not before.
	PeopleJoined []string `json:"people_joined,omitempty"`
	// PeopleLeft are people present before but not now.
	PeopleLeft []string `json:"people_left,omitempty"`
	// TeamsAdded are teams present now but not before.
	TeamsAdded []string `json:"teams_added,omitempty"`
	// TeamsRemoved are teams present before but not now.
	TeamsRemoved []string `json:"teams_removed,omitempty"`
	// ChannelsAdded are channels present now but not before.
	ChannelsAdded []string `json:"channels_added,omitempty"`
	// ChannelsRemoved are channels present before but not now.
	ChannelsRemoved []string `json:"channels_removed,omitempty"`
}

// Empty reports whether nothing changed.
func (c Changes) Empty() bool {
	return len(c.PeopleJoined)+len(c.PeopleLeft)+
		len(c.TeamsAdded)+len(c.TeamsRemoved)+
		len(c.ChannelsAdded)+len(c.ChannelsRemoved) == 0
}

// Summary is a one-line count of the changes.
func (c Changes) Summary() string {
	var parts []string
	count := func(n int, sign, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s%d %s", sign, n, label))
		}
	}
	count(len(c.PeopleJoined), "+", "people")
	count(len(c.PeopleLeft), "-", "people")
	count(len(c.ChannelsAdded), "+", "channels")
	count(len(c.ChannelsRemoved), "-", "channels")
	count(len(c.TeamsAdded), "+", "teams")
	count(len(c.TeamsRemoved), "-", "teams")
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// Diff reports what changed from the prev graph to the cur graph.
func Diff(prev, cur *model.Graph) Changes {
	if prev == nil {
		prev = model.NewGraph()
	}
	if cur == nil {
		cur = model.NewGraph()
	}
	return Changes{
		PeopleJoined:    missingPeople(cur.People, prev.People),
		PeopleLeft:      missingPeople(prev.People, cur.People),
		TeamsAdded:      missingTeams(cur.Teams, prev.Teams),
		TeamsRemoved:    missingTeams(prev.Teams, cur.Teams),
		ChannelsAdded:   missingChannels(cur.Channels, prev.Channels),
		ChannelsRemoved: missingChannels(prev.Channels, cur.Channels),
	}
}

// missingPeople returns labels of people in a whose id is absent from b.
func missingPeople(a, b map[model.ID]*model.Person) []string {
	var out []string
	for id, p := range a {
		if _, ok := b[id]; !ok {
			out = append(out, personLabel(id, p))
		}
	}
	sort.Strings(out)
	return out
}

// personLabel is a human label for a person, preferring name then email.
func personLabel(id model.ID, p *model.Person) string {
	switch {
	case p != nil && p.Name != "":
		return p.Name
	case p != nil && p.Email != "":
		return p.Email
	default:
		return string(id)
	}
}

// missingTeams returns names of teams in a whose id is absent from b.
func missingTeams(a, b map[model.ID]*model.Team) []string {
	var out []string
	for id, t := range a {
		if _, ok := b[id]; !ok {
			name := string(id)
			if t != nil && t.Name != "" {
				name = t.Name
			}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// missingChannels returns names of channels in a whose id is absent from b.
func missingChannels(a, b map[model.ID]*model.Channel) []string {
	var out []string
	for id, ch := range a {
		if _, ok := b[id]; !ok {
			name := string(id)
			if ch != nil && ch.Name != "" {
				name = ch.Name
			}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
