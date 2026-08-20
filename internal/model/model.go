// Package model defines the normalized expertise graph: the people, teams,
// orgs, and topics that whodar indexes and ranks.
package model

// ID is a stable identifier for a graph entity.
type ID string

// Person is an individual in the organization.
type Person struct {
	// ID uniquely identifies the person.
	ID ID
	// Name is the person's display name.
	Name string
	// Email is the person's work email address.
	Email string
	// Title is the person's job title.
	Title string
	// TeamID links the person to their team.
	TeamID ID
	// OrgID links the person to their organization.
	OrgID ID
	// ManagerID links the person to their manager, if known.
	ManagerID ID
	// Identities lists alternate identifiers merged into this person, such as
	// a GitHub login joined to an email.
	Identities []ID
	// Topics maps a topic ID to this person's affinity weight for it.
	Topics map[ID]float64
}

// Team is a named group of people within an organization.
type Team struct {
	// ID uniquely identifies the team.
	ID ID
	// Name is the team's display name.
	Name string
	// OrgID links the team to its organization.
	OrgID ID
	// Desc is an optional description of the team's remit.
	Desc string
}

// Org is a top-level organization or department.
type Org struct {
	// ID uniquely identifies the organization.
	ID ID
	// Name is the organization's display name.
	Name string
}

// Topic is a subject of expertise, keyed by a normalized lowercase name.
type Topic struct {
	// ID uniquely identifies the topic.
	ID ID
	// Name is the normalized topic text.
	Name string
}

// Channel is a place to ask, such as a Slack channel.
type Channel struct {
	// ID uniquely identifies the channel.
	ID ID
	// Name is the channel name without any leading symbol.
	Name string
	// Topic is the channel's stated topic, shown to users.
	Topic string
	// URL opens the channel in its source tool, empty when unknown.
	URL string
	// Members lists the people active in the channel, by person ID.
	Members []ID
	// Topics maps a topic ID to the channel's affinity weight for it.
	Topics map[ID]float64
}

// Graph holds the full set of entities whodar has indexed.
type Graph struct {
	// People maps person ID to person.
	People map[ID]*Person `json:"people"`
	// Teams maps team ID to team.
	Teams map[ID]*Team `json:"teams"`
	// Orgs maps org ID to organization.
	Orgs map[ID]*Org `json:"orgs"`
	// Topics maps topic ID to topic.
	Topics map[ID]*Topic `json:"topics"`
	// Channels maps channel ID to channel.
	Channels map[ID]*Channel `json:"channels"`
}

// NewGraph returns an empty graph with initialized maps.
func NewGraph() *Graph {
	return &Graph{
		People:   make(map[ID]*Person),
		Teams:    make(map[ID]*Team),
		Orgs:     make(map[ID]*Org),
		Topics:   make(map[ID]*Topic),
		Channels: make(map[ID]*Channel),
	}
}

// Match is a single ranked answer: a person, their team, the relevance score,
// and the human-readable reasons the person matched.
type Match struct {
	// Person is the matched individual.
	Person *Person
	// Team is the matched person's team, if known.
	Team *Team
	// Score is the relevance score; higher is more relevant.
	Score float64
	// Confidence estimates how trustworthy the match is, from zero to one:
	// query-term coverage scaled by the strength of the matched fields.
	Confidence float64
	// Reasons explains why the person matched, for transparency.
	Reasons []string
}

// ChannelMatch is a ranked channel answer: the channel, its score, the reasons
// it matched, and the people most worth contacting there.
type ChannelMatch struct {
	// Channel is the matched channel.
	Channel *Channel
	// Score is the relevance score; higher is more relevant.
	Score float64
	// Confidence estimates how trustworthy the match is, from zero to one:
	// query-term coverage scaled by the strength of the matched fields.
	Confidence float64
	// Reasons explains why the channel matched.
	Reasons []string
	// TopMembers are the most relevant active members, most relevant first.
	TopMembers []*Person
}
