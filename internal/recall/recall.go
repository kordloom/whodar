// Package recall answers the question whodar's index cannot: not who knows
// about something, but when you worked through it before and who was with you.
// An answer is a pointer, never a transcript: the people, the place, the date,
// and a link back to the conversation in the tool it happened in.
package recall

import (
	"sort"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// Resolver answers recall questions from an episode store, naming people from
// the graph the main index holds.
type Resolver struct {
	// store holds the episodes.
	store *episode.Store
	// ix names people and resolves the identifiers sources use.
	ix *index.Index
	// horizon is how far back links are trusted. Past it an answer says the
	// link may no longer resolve, because the workspace may have aged the
	// message out. Zero means no claim is made.
	horizon time.Duration
}

// New returns a Resolver over store, naming people from ix. It panics on a nil
// store or index, which is a wiring mistake rather than a runtime condition.
func New(store *episode.Store, ix *index.Index) *Resolver {
	if store == nil || ix == nil {
		panic("recall: New requires a store and an index")
	}
	return &Resolver{store: store, ix: ix}
}

// SetHorizon sets how far back links are trusted before an answer warns that
// the source may have deleted the message.
func (r *Resolver) SetHorizon(d time.Duration) { r.horizon = d }

// Who resolves an identifier a person is known by, such as an email or a Slack
// user ID, to their canonical identity. It returns an empty ID when the hint
// is empty.
func (r *Resolver) Who(hint string) model.ID {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return ""
	}
	return r.ix.Canonical(model.ID(hint))
}

// Known reports whether any indexed conversation includes a person, which
// separates "you were not in any of these" from "nothing has been indexed
// yet". An empty answer means different things in those two cases.
func (r *Resolver) Known(person model.ID) bool { return r.store.HasPerson(person) }

// Len reports how many conversations are held.
func (r *Resolver) Len() int { return r.store.Len() }

// Query asks what a person worked through before.
type Query struct {
	// Text is the question.
	Text string
	// Person scopes the answer to conversations this person took part in.
	// Personal recall always sets it.
	Person model.ID
	// Limit caps episodes returned; zero means five.
	Limit int
}

// Answer is a recall result, shaped for JSON so the CLI, the web app, and the
// bot all render the same thing.
type Answer struct {
	// Query echoes the question asked.
	Query string `json:"query"`
	// Person is who the answer is about.
	Person string `json:"person,omitempty"`
	// Episodes are the matching conversations, best first.
	Episodes []Episode `json:"episodes"`
	// Scope states what was searched, so an empty answer is not mistaken for
	// an absence of history.
	Scope Scope `json:"scope"`
}

// Episode is one remembered conversation.
type Episode struct {
	// People are the others who took part, which is the answer to "who helped
	// me".
	People []Person `json:"people"`
	// Place is where it happened, such as a channel name.
	Place string `json:"place,omitempty"`
	// Source names the tool it happened in.
	Source string `json:"source"`
	// Kind is the conversation shape: a thread, a stretch of channel talk, or
	// a resolved ticket.
	Kind string `json:"kind"`
	// When is when it last saw activity.
	When time.Time `json:"when"`
	// Messages counts the messages behind it.
	Messages int `json:"messages,omitempty"`
	// Permalink opens the conversation in its own tool, where the reader's own
	// access still applies.
	Permalink string `json:"permalink,omitempty"`
	// LinkMayHaveExpired warns that the conversation is older than the
	// workspace is known to keep, so the link may no longer resolve.
	LinkMayHaveExpired bool `json:"link_may_have_expired,omitempty"`
	// Matched lists the query terms found in the conversation.
	Matched []string `json:"matched,omitempty"`
	// Confidence is how much of the question this conversation covered, from
	// zero to one.
	Confidence float64 `json:"confidence"`
}

// Person names someone who took part.
type Person struct {
	// Name is their display name.
	Name string `json:"name,omitempty"`
	// Email is their work email.
	Email string `json:"email,omitempty"`
	// Title is their job title.
	Title string `json:"title,omitempty"`
	// ID is the canonical identifier, present when no name is known.
	ID string `json:"id,omitempty"`
}

// Scope states what a recall answer covered, so a miss can be read correctly.
type Scope struct {
	// Sources are the tools episodes came from.
	Sources []string `json:"sources"`
	// Episodes is how many conversations were searched.
	Episodes int `json:"episodes"`
	// Oldest is the earliest conversation held.
	Oldest time.Time `json:"oldest,omitempty"`
	// Note explains in words what is and is not covered.
	Note string `json:"note"`
}

// Resolve answers a recall query.
func (r *Resolver) Resolve(q Query) Answer {
	hits := r.store.Search(episode.Query{Text: q.Text, Person: q.Person, Limit: q.Limit})
	ans := Answer{
		Query:    q.Text,
		Person:   string(q.Person),
		Episodes: make([]Episode, 0, len(hits)),
		Scope:    r.scope(),
	}
	for _, hit := range hits {
		ans.Episodes = append(ans.Episodes, r.view(hit, q.Person))
	}
	return ans
}

// view renders one search hit for display.
func (r *Resolver) view(hit episode.Result, asker model.ID) Episode {
	ep := hit.Episode
	others := ep.Others(asker)
	people := make([]Person, 0, len(others))
	for _, id := range others {
		people = append(people, r.person(id))
	}
	out := Episode{
		People:     people,
		Place:      ep.Place,
		Source:     ep.Source,
		Kind:       string(ep.Kind),
		When:       ep.Occurred,
		Messages:   ep.Messages,
		Permalink:  ep.Permalink,
		Matched:    hit.Matched,
		Confidence: roundTwo(hit.Confidence),
	}
	if r.horizon > 0 && !ep.Occurred.IsZero() && time.Since(ep.Occurred) > r.horizon {
		out.LinkMayHaveExpired = true
	}
	return out
}

// person names an identifier from the graph, falling back to the identifier
// itself when the graph has never seen it.
func (r *Resolver) person(id model.ID) Person {
	if p := r.ix.Graph.People[r.ix.Canonical(id)]; p != nil {
		return Person{Name: p.Name, Email: p.Email, Title: p.Title}
	}
	return Person{ID: string(id)}
}

// scope describes the history that was searched.
func (r *Resolver) scope() Scope {
	all := r.store.All()
	sources := make(map[string]bool)
	var oldest time.Time
	for _, ep := range all {
		sources[ep.Source] = true
		if ep.Occurred.IsZero() {
			continue
		}
		if oldest.IsZero() || ep.Occurred.Before(oldest) {
			oldest = ep.Occurred
		}
	}
	names := make([]string, 0, len(sources))
	for s := range sources {
		names = append(names, s)
	}
	sort.Strings(names)
	return Scope{
		Sources:  names,
		Episodes: len(all),
		Oldest:   oldest,
		Note: "Covers conversations whodar has indexed: channels the bot can read, " +
			"within the history window. Direct messages are not indexed.",
	}
}

// roundTwo trims a confidence to two decimals for stable output.
func roundTwo(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
