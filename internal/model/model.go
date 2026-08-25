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
	// Owns lists topic IDs this person is a declared owner of, from a source of
	// record such as CODEOWNERS, as opposed to topics they merely work in.
	Owns []ID
	// Stated is the part of each topic weight that a source of record assigned
	// rather than the person earning it: being listed in CODEOWNERS or given a
	// topics column in an org chart. Subtracting it from Topics leaves the work
	// they actually did, which is the difference between owning an area and
	// working in it. Without it, indexing a CODEOWNERS file makes every owner
	// look active in everything they own.
	Stated map[ID]float64
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
	// Curated marks a topic that some source stated explicitly, such as a GitHub
	// topic, a Jira label, a CODEOWNERS path, or an org-chart topics column. A
	// topic only ever mined out of prose is not curated, which is how an
	// incidental word is told apart from a subject somebody declared.
	Curated bool
	// Sources names the connectors that produced this topic. Breadth across
	// sources is the strongest evidence a topic is real rather than a stray word.
	Sources []string
	// Ubiquitous marks a topic nearly everybody holds. Those are the scaffolding
	// of a codebase rather than subjects within it: the name of the repository,
	// the language it is written in, the directory every file sits under. They
	// are stated as plainly as any real subject and are worthless for telling
	// people apart, which is the only thing a subject is for here.
	Ubiquitous bool
}

// Salient reports whether a topic is established enough to present as a subject
// in its own right. A curated topic counts; a topic only mined from prose has to
// show up in more than one source to earn it. Either way a topic everybody holds
// does not count, because it distinguishes nobody.
func (t *Topic) Salient() bool {
	if t == nil || t.Ubiquitous {
		return false
	}
	return t.Curated || len(t.Sources) > 1
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
