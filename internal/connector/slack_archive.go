package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/slack"
)

// Archive bounds. A retained conversation is meant to be read by a person, so
// it keeps the discussion and stops well short of copying a channel.
const (
	// defaultMaxArchiveMessages caps retained messages per conversation.
	defaultMaxArchiveMessages = 50
	// maxArchiveBytes caps retained text per conversation.
	maxArchiveBytes = 12000
)

// fillArchive retains the content of each threaded conversation, which is what
// turns a pointer into an answer. It is the only path that reads replies, so
// it runs only when an archive is licensed, permitted, and asked for. A
// conversation that cannot be read is left as a pointer rather than failing
// the run.
func (s *Slack) fillArchive(ctx context.Context, eps []episode.Episode, byID map[string]slack.User) {
	limit := s.opts.MaxArchiveMessages
	if limit <= 0 {
		limit = defaultMaxArchiveMessages
	}
	for i := range eps {
		ep := &eps[i]
		if ep.Kind != episode.KindThread || ep.PlaceID == "" {
			continue
		}
		threadTS := threadTSOf(ep.ID)
		if threadTS == "" {
			continue
		}
		msgs, err := s.client.Replies(ctx, ep.PlaceID, threadTS, limit)
		if err != nil {
			if errors.Is(err, slack.ErrRateLimited) {
				fmt.Fprintf(s.opts.Log,
					"slack: rate limited reading #%s; keeping the rest as links only\n", ep.Place)
				return
			}
			fmt.Fprintf(s.opts.Log, "slack: could not read a thread in #%s: %v\n", ep.Place, err)
			continue
		}
		ep.Archive = notesFrom(msgs, byID)
	}
}

// notesFrom turns fetched messages into retained notes, skipping system and
// bot messages and stopping at the size cap.
func notesFrom(msgs []slack.Message, byID map[string]slack.User) []episode.Note {
	var notes []episode.Note
	bytes := 0
	for _, m := range msgs {
		if m.Subtype != "" || m.User == "" || m.BotID != "" || m.Text == "" {
			continue
		}
		if bytes+len(m.Text) > maxArchiveBytes {
			break
		}
		bytes += len(m.Text)
		notes = append(notes, episode.Note{
			Author: model.ID(slackUserRef(m.User, byID)),
			At:     slackTime(m.TS),
			Text:   m.Text,
		})
	}
	return notes
}

// threadTSOf recovers the thread timestamp from an episode id of the form
// "slack:<channel>:<thread ts>". A windowed conversation has no thread and
// returns the empty string.
func threadTSOf(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == ':' {
			ts := id[i+1:]
			if ts == "" || ts[0] == 'w' {
				return ""
			}
			return ts
		}
	}
	return ""
}
