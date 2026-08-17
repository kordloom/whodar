package resolve

import (
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// Directory is the browsable inventory of everything indexed, for the web
// UI's directory views. Build it once per process; the graph does not change
// while serving.
type Directory struct {
	// People lists everyone, sorted by display name.
	People []DirectoryPerson `json:"people"`
	// Channels lists every channel, sorted by name.
	Channels []DirectoryChannel `json:"channels"`
	// Teams lists every team with its size, sorted by name.
	Teams []DirectoryTeam `json:"teams"`
	// Topics lists every topic with how many people carry it, most first.
	Topics []DirectoryTopic `json:"topics"`
}

// DirectoryPerson is one row of the people directory.
type DirectoryPerson struct {
	// ID is the person's canonical identifier.
	ID string `json:"id"`
	// Name is the display name, falling back to the identifier.
	Name string `json:"name"`
	// Email is the person's work email.
	Email string `json:"email,omitempty"`
	// Title is the person's job title.
	Title string `json:"title,omitempty"`
	// Team is the person's team name.
	Team string `json:"team,omitempty"`
	// Org is the person's organization name.
	Org string `json:"org,omitempty"`
	// Topics are the person's strongest expertise areas, strongest first.
	Topics []string `json:"topics,omitempty"`
}

// DirectoryChannel is one row of the channel directory.
type DirectoryChannel struct {
	// Name is the channel name without a leading symbol.
	Name string `json:"name"`
	// Topic is the channel's stated topic.
	Topic string `json:"topic,omitempty"`
	// Members is how many people are active in the channel.
	Members int `json:"members"`
}

// DirectoryTeam is one row of the team directory.
type DirectoryTeam struct {
	// Name is the team name.
	Name string `json:"name"`
	// Org is the team's organization name.
	Org string `json:"org,omitempty"`
	// People is how many people the team has.
	People int `json:"people"`
}

// DirectoryTopic is one row of the topic directory.
type DirectoryTopic struct {
	// Name is the topic slug, which doubles as its display name.
	Name string `json:"name"`
	// People is how many people carry the topic.
	People int `json:"people"`
}

// BuildDirectory assembles the directory from the index graph.
func BuildDirectory(ix *index.Index) Directory {
	g := ix.Graph
	var d Directory

	teamSize := make(map[model.ID]int)
	topicSize := make(map[model.ID]int)
	for _, p := range g.People {
		row := DirectoryPerson{
			ID:     string(p.ID),
			Name:   p.Name,
			Email:  p.Email,
			Title:  p.Title,
			Topics: topTopics(p.Topics, 6),
		}
		if row.Name == "" {
			row.Name = row.ID
		}
		if t := g.Teams[p.TeamID]; t != nil {
			row.Team = t.Name
			teamSize[p.TeamID]++
		}
		if o := g.Orgs[p.OrgID]; o != nil {
			row.Org = o.Name
		}
		for tid := range p.Topics {
			topicSize[tid]++
		}
		d.People = append(d.People, row)
	}
	sort.Slice(d.People, func(i, j int) bool {
		if li, lj := strings.ToLower(d.People[i].Name), strings.ToLower(d.People[j].Name); li != lj {
			return li < lj
		}
		// Two people can share a display name, so the unique id keeps the order
		// stable between runs rather than following random map iteration.
		return d.People[i].ID < d.People[j].ID
	})

	for _, ch := range g.Channels {
		d.Channels = append(d.Channels, DirectoryChannel{
			Name: ch.Name, Topic: ch.Topic, Members: len(ch.Members),
		})
	}
	sort.Slice(d.Channels, func(i, j int) bool {
		if li, lj := strings.ToLower(d.Channels[i].Name), strings.ToLower(d.Channels[j].Name); li != lj {
			return li < lj
		}
		return d.Channels[i].Name < d.Channels[j].Name
	})

	for tid, t := range g.Teams {
		row := DirectoryTeam{Name: t.Name, People: teamSize[tid]}
		if o := g.Orgs[t.OrgID]; o != nil {
			row.Org = o.Name
		}
		d.Teams = append(d.Teams, row)
	}
	sort.Slice(d.Teams, func(i, j int) bool {
		if li, lj := strings.ToLower(d.Teams[i].Name), strings.ToLower(d.Teams[j].Name); li != lj {
			return li < lj
		}
		return d.Teams[i].Org < d.Teams[j].Org
	})

	for tid, count := range topicSize {
		d.Topics = append(d.Topics, DirectoryTopic{Name: string(tid), People: count})
	}
	sort.Slice(d.Topics, func(i, j int) bool {
		if d.Topics[i].People != d.Topics[j].People {
			return d.Topics[i].People > d.Topics[j].People
		}
		return d.Topics[i].Name < d.Topics[j].Name
	})
	return d
}
