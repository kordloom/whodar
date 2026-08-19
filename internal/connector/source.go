// Package connector pulls raw records from work sources and normalizes them
// into records the indexer merges into the expertise graph.
package connector

import (
	"context"
	"time"

	"github.com/kordloom/whodar/internal/episode"
)

// Record is one normalized observation from a source. A KindPerson record
// describes a person: identity, org placement, topics, and mined text. A
// KindChannel record describes a place to ask, with the channel name, topic,
// text, and active members.
type Record struct {
	// Kind classifies the record; the zero value is KindPerson.
	Kind Kind
	// PersonID is a stable per-source identifier; empty derives one from email.
	PersonID string
	// Name is the person's display name.
	Name string
	// Email is the person's work email.
	Email string
	// Title is the person's job title.
	Title string
	// Team is the person's team name.
	Team string
	// Org is the person's organization name.
	Org string
	// Manager is the manager's email or identifier, if known.
	Manager string
	// Topics are explicit expertise tags for the person or channel.
	Topics []string
	// Members lists person references active in a KindChannel record.
	Members []string
	// Text is free-form text attributed to the person or channel, mined for
	// topics. It is readable message content and is never written to disk: the
	// index persists only the stemmed Terms derived from it, so a stored index
	// holds a search index rather than the messages themselves.
	Text string
	// Terms are the stemmed search terms derived from Text. The index fills and
	// stores them in place of Text so a saved index can be rebuilt on a later
	// merge without keeping any readable message text on disk.
	Terms []string
	// Source names the origin connector, e.g. "org-csv".
	Source string
	// Weight scales this record's affinity contribution; zero means one.
	Weight float64
	// Time is when the described activity happened. The zero value marks the
	// record as a current fact that never decays.
	Time time.Time
}

// Kind classifies a record. For KindChannel records the person identity fields
// are unused: Name is the channel name, Title is the channel topic, Text is the
// purpose and sampled message text mined for affinity, and Members lists the
// person references active in the channel.
type Kind int

const (
	// KindPerson describes a person. It is the zero value.
	KindPerson Kind = iota
	// KindChannel describes a channel, a place to ask.
	KindChannel
)

// Source fetches and normalizes records from one origin.
type Source interface {
	// Fetch returns the records this source currently provides.
	Fetch(ctx context.Context) ([]Record, error)
}

// EpisodeSource is implemented by sources that also observe bounded
// conversations, such as a chat thread or a resolved ticket. Episodes are
// collected during Fetch and read afterwards, so a source that does not
// implement it costs nothing and the Source interface stays one method.
type EpisodeSource interface {
	// Episodes returns the conversations seen by the most recent Fetch.
	Episodes() []episode.Episode
}

// Watermarker is implemented by sources that support an incremental fetch.
// After a Fetch it reports how far the source was read, so the caller can
// persist a watermark and ask only for newer items next time. A source that
// does not implement it is always read in full.
type Watermarker interface {
	// Watermark returns the newest activity time read and whether the read
	// reached everything available. The caller passes the cursor back as the
	// next since bound; complete being false means more remains to read.
	Watermark() (cursor time.Time, complete bool)
}

// SourceFunc adapts a function to the Source interface.
type SourceFunc func(ctx context.Context) ([]Record, error)

// Fetch calls f.
func (f SourceFunc) Fetch(ctx context.Context) ([]Record, error) { return f(ctx) }
