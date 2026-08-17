// Package episode records the bounded conversations a person took part in, so
// whodar can answer which past discussion solved a problem and who was in it.
// An episode holds who, where, and when, plus a link back to the source. The
// conversation itself stays in the tool it happened in unless the archive is
// enabled.
package episode

import (
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/model"
)

// Kind classifies what shape of conversation an episode came from, so an
// answer can say whether it is pointing at a thread, a stretch of channel
// chatter, or a resolved ticket.
type Kind string

const (
	// KindThread is a threaded conversation with replies.
	KindThread Kind = "thread"
	// KindWindow is a run of channel messages close together in time, the
	// shape guidance takes when nobody starts a thread.
	KindWindow Kind = "window"
	// KindIncident is a resolved operational incident.
	KindIncident Kind = "incident"
	// KindIssue is a resolved ticket.
	KindIssue Kind = "issue"
	// KindChange is a merged code change and its review discussion.
	KindChange Kind = "change"
)

// Episode is one bounded conversation whodar can point back to.
type Episode struct {
	// ID uniquely identifies the episode across runs, so re-indexing updates
	// an episode rather than duplicating it.
	ID string `json:"id"`
	// Source names the origin connector, such as "slack".
	Source string `json:"source"`
	// Kind classifies the conversation shape.
	Kind Kind `json:"kind"`
	// Place is the human-readable location, such as a channel or project name.
	Place string `json:"place"`
	// PlaceID is the source's own identifier for that location.
	PlaceID string `json:"place_id,omitempty"`
	// Title is a short subject line when the source has one, such as an issue
	// or incident title. Chat episodes have none.
	Title string `json:"title,omitempty"`
	// Participants are the canonical people who took part, most involved
	// first.
	Participants []model.ID `json:"participants"`
	// Occurred is when the conversation last saw activity.
	Occurred time.Time `json:"occurred"`
	// Permalink points back to the conversation in its own tool.
	Permalink string `json:"permalink,omitempty"`
	// Messages counts the messages the episode was built from.
	Messages int `json:"messages,omitempty"`
	// Archive holds retained conversation content. It is empty unless the
	// archive is licensed and enabled, and it is what a Memory answer quotes.
	Archive []Note `json:"archive,omitempty"`
	// Body is the conversation text a connector hands over so the episode can
	// be indexed. The store tokenizes it and drops it, and it is never
	// serialized: an episode on disk holds terms, not messages.
	Body string `json:"-"`
}

// Note is one retained message inside an archived episode.
type Note struct {
	// Author is the canonical person who wrote it.
	Author model.ID `json:"author"`
	// At is when it was written.
	At time.Time `json:"at"`
	// Text is the message body as written.
	Text string `json:"text"`
}

// Archived reports whether the episode carries retained content.
func (e *Episode) Archived() bool { return len(e.Archive) > 0 }

// Involves reports whether person took part in the episode.
func (e *Episode) Involves(person model.ID) bool {
	for _, p := range e.Participants {
		if p == person {
			return true
		}
	}
	return false
}

// Others returns the participants apart from person, which is what a recall
// answer names: the people who helped, not the asker.
func (e *Episode) Others(person model.ID) []model.ID {
	out := make([]model.ID, 0, len(e.Participants))
	for _, p := range e.Participants {
		if p != person {
			out = append(out, p)
		}
	}
	return out
}

// Text returns the searchable text of the episode's own fields, which is
// indexed alongside its conversation text.
func (e *Episode) Text() string {
	return strings.TrimSpace(e.Title + " " + e.Place)
}
