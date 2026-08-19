package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/slack"
)

// Default Slack ingest bounds. Standard depth pulls roughly six months of
// history, capped per channel so one busy channel cannot dominate a run.
const (
	// defaultSinceDays is the history window in days.
	defaultSinceDays = 180
	// defaultMaxMessages caps messages read per channel.
	defaultMaxMessages = 5000
	// maxAuthorText caps stored text per author per channel, in bytes.
	maxAuthorText = 8000
	// sampleBudget caps stored channel sample text, in bytes.
	sampleBudget = 4000
)

// SlackOptions configures the Slack connector.
type SlackOptions struct {
	// IncludePrivate also ingests private channels the token can read.
	IncludePrivate bool
	// JoinPublic makes the bot join each public channel it is not already in
	// before reading it, so a whole workspace can be indexed without inviting
	// the bot by hand. It needs the channels:join scope and posts a join notice
	// in each channel it enters. Private channels still require an invite.
	JoinPublic bool
	// SinceDays bounds history age; zero uses the default.
	SinceDays int
	// MaxMessages caps messages per channel; zero uses the default.
	MaxMessages int
	// MaxChannels caps channels processed; zero means all.
	MaxChannels int
	// Episodes records the conversations behind the messages, so whodar can
	// point back at the discussion that solved something.
	Episodes bool
	// MaxEpisodesPerChannel caps episodes kept per channel; zero uses the
	// default.
	MaxEpisodesPerChannel int
	// Archive retains the content of each conversation, not just a link to
	// it. It is the only setting that reads thread replies.
	Archive bool
	// MaxArchiveMessages caps retained messages per conversation; zero uses
	// the default.
	MaxArchiveMessages int
	// Since, when later than the SinceDays window, reads only messages posted at
	// or after it, for an incremental re-index. Identity records (users) carry no
	// weight, so only the message-derived topics narrow to the delta.
	Since time.Time
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// withDefaults fills zero fields with defaults.
func (o SlackOptions) withDefaults() SlackOptions {
	if o.SinceDays <= 0 {
		o.SinceDays = defaultSinceDays
	}
	if o.MaxMessages <= 0 {
		o.MaxMessages = defaultMaxMessages
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	return o
}

// Slack is a Source that ingests workspace users, channels, and recent history.
type Slack struct {
	// client calls the Slack Web API.
	client *slack.Client
	// opts holds the resolved ingest bounds.
	opts SlackOptions
	// episodes holds the conversations seen by the last Fetch.
	episodes []episode.Episode
}

// NewSlack returns a Slack connector authenticating with token.
func NewSlack(token string, opts SlackOptions) *Slack {
	return &Slack{client: slack.New(token), opts: opts.withDefaults()}
}

// NewSlackWithClient returns a Slack connector using a preconfigured client.
// Tests use it to inject a client pointed at a mock server.
func NewSlackWithClient(client *slack.Client, opts SlackOptions) *Slack {
	if client == nil {
		panic("connector: NewSlackWithClient requires a non-nil client")
	}
	return &Slack{client: client, opts: opts.withDefaults()}
}

// Ping verifies the token with a cheap auth.test call, so a wizard can confirm
// credentials before committing to a full index.
func (s *Slack) Ping(ctx context.Context) error {
	_, err := s.client.AuthTest(ctx)
	return err
}

// Fetch reads users, channels, and bounded history, returning person and
// channel records. Person identity joins other sources by email.
func (s *Slack) Fetch(ctx context.Context) ([]Record, error) {
	users, err := s.client.Users(ctx)
	if err != nil {
		return nil, fmt.Errorf("slack users: %w", err)
	}
	byID := make(map[string]slack.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}

	var records []Record
	noEmail := 0
	for _, u := range users {
		if u.Profile.RealName == "" && u.Profile.Email == "" {
			continue
		}
		if u.Profile.Email == "" {
			noEmail++
		}
		records = append(records, personRecord(u))
	}
	if noEmail > 0 {
		// A user with no email keys on the opaque slack:UID and joins nothing,
		// so their Slack activity splits from the same person's git, GitHub, and
		// Jira work, and recall --me finds none of their Slack episodes. Surface
		// the count so the missing scope is visible rather than silently halving
		// the graph.
		fmt.Fprintf(s.opts.Log,
			"slack: %d of %d members have no email; the token lacks users:read.email, so their "+
				"Slack activity will not merge with the same people from other sources. Add the "+
				"users:read.email scope and reinstall to link them\n", noEmail, len(records))
	}

	types := "public_channel"
	if s.opts.IncludePrivate {
		types = "public_channel,private_channel"
	}
	channels, err := s.client.Channels(ctx, types)
	if err != nil {
		return nil, fmt.Errorf("slack channels: %w", err)
	}
	fmt.Fprintf(s.opts.Log, "slack: %d users, %d channels\n", len(users), len(channels))

	// The workspace URL turns a channel and a timestamp into a permalink with
	// no further calls. Losing it costs links, not episodes, so a failure here
	// is logged rather than fatal.
	workspaceURL := ""
	if s.opts.Episodes {
		s.episodes = nil
		auth, err := s.client.AuthTest(ctx)
		if err != nil {
			fmt.Fprintf(s.opts.Log, "slack: no workspace url, episodes will have no links: %v\n", err)
		} else {
			workspaceURL = auth.URL
		}
	}

	oldest := slackOldestSince(s.opts.SinceDays, s.opts.Since)
	skipped, read, notInChannel := 0, 0, 0
	joined := 0
	// maxConsecThrottle stops a run only when the whole workspace is hard rate
	// limited, not on a single slow channel.
	const maxConsecThrottle = 5
	consecThrottle := 0
	var unfinished []string
	for i, ch := range channels {
		if s.opts.MaxChannels > 0 && i >= s.opts.MaxChannels {
			fmt.Fprintf(s.opts.Log, "slack: stopping at %d channels (cap)\n", s.opts.MaxChannels)
			break
		}
		// Self-join a public channel the bot is not in yet, so the whole
		// workspace indexes without hand invites. A private channel cannot be
		// self-joined; only a member can add the bot there.
		if s.opts.JoinPublic && !ch.IsPrivate && !ch.IsMember {
			if err := s.client.JoinChannel(ctx, ch.ID); err != nil {
				fmt.Fprintf(s.opts.Log, "slack: could not join #%s: %v\n", ch.Name, err)
			} else {
				joined++
			}
		}
		msgs, err := s.client.History(ctx, ch.ID, oldest, s.opts.MaxMessages)
		if errors.Is(err, slack.ErrRateLimited) {
			// The history retry budget is large, so reaching here means this
			// channel stayed throttled for many minutes. Skip it and keep going
			// rather than abandoning every later channel, and record it so a
			// re-run can finish it. Only when many channels in a row throttle is
			// the whole workspace hard limited, and then stopping and keeping
			// what was read beats spinning.
			unfinished = append(unfinished, ch.Name)
			consecThrottle++
			fmt.Fprintf(s.opts.Log, "slack: still rate limited at #%s after retries; skipping it\n", ch.Name)
			if consecThrottle >= maxConsecThrottle {
				fmt.Fprintf(s.opts.Log,
					"slack: %d channels in a row are rate limited; stopping with %d read and %d left "+
						"to finish on a re-run\n", consecThrottle, read, len(unfinished))
				break
			}
			continue
		}
		consecThrottle = 0
		if errors.Is(err, slack.ErrAPI) {
			// Any per-channel API error, such as not_in_channel on a public
			// channel the bot was never invited to, is skipped so one
			// unreadable channel does not cost the whole run.
			skipped++
			if strings.Contains(err.Error(), "not_in_channel") {
				notInChannel++
			}
			fmt.Fprintf(s.opts.Log, "slack: skipping #%s: %v\n", ch.Name, err)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("slack history for %s: %w", ch.Name, err)
		}
		if len(msgs) >= s.opts.MaxMessages {
			fmt.Fprintf(s.opts.Log,
				"slack: #%s hit the %d message cap; older messages skipped\n", ch.Name, s.opts.MaxMessages)
		}

		chRec, authorText, authorLatest := channelRecord(ch, msgs, byID)
		records = append(records, chRec)
		for pid, text := range authorText {
			records = append(records, Record{
				Kind: KindPerson, Source: "slack", Weight: 1, PersonID: pid, Text: text,
				Time: authorLatest[pid],
			})
		}
		if s.opts.Episodes {
			eps := collectEpisodes(ch, msgs, episodeOpts{
				byID:         byID,
				workspaceURL: workspaceURL,
				max:          s.opts.MaxEpisodesPerChannel,
				archive:      s.opts.Archive,
				maxArchive:   s.opts.MaxArchiveMessages,
			})
			// Read each thread's replies and fold them into the searchable body
			// so recall matches the solution, not just the question. Verbatim
			// retention for quoting stays gated to a licensed archive inside.
			s.enrichThreads(ctx, eps, byID)
			s.episodes = append(s.episodes, eps...)
		}
		read++
		fmt.Fprintf(s.opts.Log, "slack: indexed #%s (%d messages)\n", ch.Name, len(msgs))
	}
	if joined > 0 {
		fmt.Fprintf(s.opts.Log, "slack: joined %d public channels to read them\n", joined)
	}
	if skipped > 0 {
		switch {
		case read == 0 && notInChannel == skipped:
			// The token works and lists channels, but the bot is in none of
			// them, so history is refused everywhere. This is the first-run
			// case: the app is installed but not yet invited anywhere.
			fmt.Fprintf(s.opts.Log,
				"slack: the bot is not a member of any channel, so no messages could be read. "+
					"In Slack, invite it with `/invite @whodar` in the channels you want indexed, "+
					"then run this again\n")
		case read == 0:
			// Not a membership problem, so the token cannot read history at
			// all: the scope is missing.
			fmt.Fprintf(s.opts.Log,
				"slack: no channel could be read. The bot token is likely missing the "+
					"channels:history scope (and groups:history for private channels). "+
					"Add it in your Slack app config and reinstall\n")
		default:
			fmt.Fprintf(s.opts.Log,
				"slack: skipped %d channels the bot is not in; invite it to the ones that matter\n", skipped)
		}
	}
	if len(unfinished) > 0 {
		fmt.Fprintf(s.opts.Log,
			"slack: %d channels were left unfinished by rate limiting; re-run to index them\n",
			len(unfinished))
	}
	return records, nil
}

// personRecord builds a person record from a Slack user. The record carries
// the Slack user ID as its identifier and the email alongside it, so the
// indexer joins the two. Without that join a Slack user ID resolves to nobody,
// and the bot cannot tell which person is asking.
func personRecord(u slack.User) Record {
	return Record{
		Kind:     KindPerson,
		Source:   "slack",
		Weight:   1,
		PersonID: "slack:" + u.ID,
		Name:     u.Profile.RealName,
		Email:    u.Profile.Email,
		Title:    u.Profile.Title,
	}
}

// channelRecord builds a channel record, the per-author message text mined
// for personal affinity, and each author's latest message time. System and
// bot messages are skipped.
func channelRecord(
	ch slack.Channel, msgs []slack.Message, byID map[string]slack.User,
) (Record, map[string]string, map[string]time.Time) {
	var members []string
	seen := make(map[string]bool)
	authorText := make(map[string]string)
	authorLatest := make(map[string]time.Time)
	var latest time.Time
	var sample strings.Builder

	for _, m := range msgs {
		if m.Subtype != "" || m.User == "" || m.BotID != "" {
			continue
		}
		pid := slackUserRef(m.User, byID)
		if pid == "" {
			continue
		}
		if !seen[pid] {
			seen[pid] = true
			members = append(members, pid)
		}
		if len(authorText[pid]) < maxAuthorText {
			authorText[pid] += " " + m.Text
		}
		if ts := slackTime(m.TS); !ts.IsZero() {
			if ts.After(authorLatest[pid]) {
				authorLatest[pid] = ts
			}
			if ts.After(latest) {
				latest = ts
			}
		}
		if sample.Len() < sampleBudget {
			sample.WriteString(" ")
			sample.WriteString(m.Text)
		}
	}

	rec := Record{
		Kind:    KindChannel,
		Source:  "slack",
		Weight:  1,
		Name:    ch.Name,
		Title:   ch.Topic.Value,
		Text:    strings.TrimSpace(ch.Purpose.Value + " " + sample.String()),
		Members: members,
		Time:    latest,
	}
	return rec, authorText, authorLatest
}

// slackTime parses a Slack epoch timestamp such as "1712345678.000100",
// returning the zero time when it does not parse.
func slackTime(ts string) time.Time {
	sec, err := strconv.ParseFloat(ts, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0).UTC()
}

// slackPersonID resolves a stable person ID from a user, preferring email.
func slackPersonID(u slack.User) string {
	if u.Profile.Email != "" {
		return strings.ToLower(u.Profile.Email)
	}
	return "slack:" + u.ID
}

// slackUserRef resolves a message author's user ID to a stable person ID.
func slackUserRef(userID string, byID map[string]slack.User) string {
	if userID == "" {
		return ""
	}
	if u, ok := byID[userID]; ok {
		return slackPersonID(u)
	}
	return "slack:" + userID
}

// slackOldestSince returns the Slack oldest timestamp for a read: the history
// window by default, or the watermark when it is more recent, so an incremental
// re-index reads only messages posted after the last run.
func slackOldestSince(days int, since time.Time) string {
	start := time.Now().AddDate(0, 0, -days)
	if since.After(start) {
		start = since
	}
	return slackTS(start)
}

// slackTS formats a time as a Slack message timestamp, Unix seconds with a
// microsecond suffix.
func slackTS(t time.Time) string {
	return fmt.Sprintf("%d.000000", t.Unix())
}
