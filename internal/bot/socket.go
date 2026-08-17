package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kordloom/whodar/internal/slack"
)

// wsConn is the minimal WebSocket the socket runner needs.
type wsConn interface {
	// Read returns the next text message.
	Read(ctx context.Context) ([]byte, error)
	// Write sends a text message.
	Write(ctx context.Context, data []byte) error
	// Close closes the connection.
	Close() error
}

// Dialer opens a WebSocket to url.
type Dialer func(ctx context.Context, url string) (wsConn, error)

// socketEnvelope is a Socket Mode frame.
type socketEnvelope struct {
	// Type is the frame type: hello, disconnect, events_api, or
	// slash_commands.
	Type string `json:"type"`
	// EnvelopeID identifies the frame for acknowledgment.
	EnvelopeID string `json:"envelope_id"`
	// Payload carries the event or slash command.
	Payload struct {
		// Event is the Slack event, set on events_api frames.
		Event slackEvent `json:"event"`
		// Slash-command fields, set on slash_commands frames.
		Command     string `json:"command"`
		Text        string `json:"text"`
		UserID      string `json:"user_id"`
		ResponseURL string `json:"response_url"`
	} `json:"payload"`
}

// slackEvent is the subset of a Slack event the bot reads.
type slackEvent struct {
	// Type is the event type, such as app_mention or message.
	Type string `json:"type"`
	// Text is the message text.
	Text string `json:"text"`
	// Channel is the channel or DM id.
	Channel string `json:"channel"`
	// User is the author's user id.
	User string `json:"user"`
	// TS is the message timestamp.
	TS string `json:"ts"`
	// ThreadTS is the thread timestamp, if any.
	ThreadTS string `json:"thread_ts"`
	// BotID is set when a bot authored the message.
	BotID string `json:"bot_id"`
	// ChannelType is "im" for direct messages.
	ChannelType string `json:"channel_type"`
}

// Reconnect backoff bounds: failures back off exponentially from
// initialBackoff to maxBackoff, and a session that stayed healthy for
// steadyPeriod resets the backoff.
const (
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
	steadyPeriod   = 30 * time.Second
)

// maxConcurrentAnswers caps how many answers run at once, so a burst of
// mentions cannot spawn unbounded resolver work.
const maxConcurrentAnswers = 8

// SocketRunner runs a Slack Socket Mode session: it opens a WebSocket with the
// app-level token, reads event frames, acknowledges them, and dispatches
// questions to the Engine. It reconnects with backoff until the context is
// canceled.
type SocketRunner struct {
	// app is the app-level token client used to open connections.
	app *slack.Client
	// engine answers questions.
	engine *Engine
	// replier posts answers back to Slack.
	replier Replier
	// respond posts slash-command answers; nil ignores slash frames.
	respond Responder
	// botUserID is the bot's own user id, used to ignore its own messages.
	botUserID string
	// dial opens the WebSocket; overridable for tests.
	dial Dialer
	// log receives connection notices.
	log io.Writer
	// answerSlots bounds concurrent answers to maxConcurrentAnswers. Each
	// answer goroutine acquires a slot itself so the read loop never blocks.
	answerSlots chan struct{}
}

// SocketOption configures a SocketRunner.
type SocketOption func(*SocketRunner)

// WithDialer overrides the WebSocket dialer, for tests.
func WithDialer(d Dialer) SocketOption {
	return func(s *SocketRunner) {
		if d != nil {
			s.dial = d
		}
	}
}

// WithLog sets where connection notices are written.
func WithLog(w io.Writer) SocketOption {
	return func(s *SocketRunner) {
		if w != nil {
			s.log = w
		}
	}
}

// WithResponder enables slash-command answers through r.
func WithResponder(r Responder) SocketOption {
	return func(s *SocketRunner) {
		if r != nil {
			s.respond = r
		}
	}
}

// NewSocketRunner builds a SocketRunner. It panics on nil dependencies.
func NewSocketRunner(app *slack.Client, engine *Engine, replier Replier, botUserID string, opts ...SocketOption) *SocketRunner {
	if app == nil || engine == nil || replier == nil {
		panic("bot: NewSocketRunner requires app, engine, and replier")
	}
	s := &SocketRunner{
		app: app, engine: engine, replier: replier, botUserID: botUserID,
		dial: dialWebSocket, log: io.Discard,
		answerSlots: make(chan struct{}, maxConcurrentAnswers),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run opens connections and processes events until ctx is canceled. Failures
// anywhere, including the very first connection, reconnect with exponential
// backoff instead of exiting, so a laptop waking from sleep or a flapping
// network self-heals.
func (s *SocketRunner) Run(ctx context.Context) error {
	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		start := time.Now()
		err := s.connectAndServe(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) >= steadyPeriod {
			backoff = initialBackoff
		}
		fmt.Fprintf(s.log, "whodar bot: reconnecting in %s after: %v\n", backoff, err)
		if !sleepCtx(ctx, backoff) {
			return nil
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// connectAndServe opens one Socket Mode session and serves it to completion.
func (s *SocketRunner) connectAndServe(ctx context.Context) error {
	url, err := s.app.ConnectionsOpen(ctx)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	conn, err := s.dial(ctx, url)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	return s.session(ctx, conn)
}

// sleepCtx waits for d, returning false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// session reads and dispatches frames until the connection ends. Answers run on
// their own goroutines so a slow one, up to handleTimeout, cannot stall the read
// loop from acking and reading the next envelope. Outstanding answers are
// drained before the session returns.
func (s *SocketRunner) session(ctx context.Context, conn wsConn) error {
	defer func() { _ = conn.Close() }()
	var answers sync.WaitGroup
	defer answers.Wait()
	for {
		data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var env socketEnvelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Type {
		case "hello":
			continue
		case "disconnect":
			return errors.New("server requested disconnect")
		default:
			if env.EnvelopeID != "" {
				s.ack(ctx, conn, env.EnvelopeID)
			}
			switch {
			case env.Type == "events_api":
				ev := env.Payload.Event
				s.dispatch(ctx, &answers, func() {
					routeEvent(ctx, s.engine, s.replier, s.botUserID, ev, s.log)
				})
			case env.Type == "slash_commands" && s.respond != nil:
				cmd := slashCommand{
					Text:        env.Payload.Text,
					UserID:      env.Payload.UserID,
					ResponseURL: env.Payload.ResponseURL,
				}
				s.dispatch(ctx, &answers, func() {
					routeSlash(ctx, s.engine, s.respond, cmd, s.log)
				})
			}
		}
	}
}

// dispatch runs fn on its own goroutine after acquiring an answer slot, so the
// read loop never blocks: a slot fills only inside the goroutine. It abandons
// the work if ctx ends before a slot frees. The WaitGroup lets the session
// drain in-flight answers before it returns.
func (s *SocketRunner) dispatch(ctx context.Context, wg *sync.WaitGroup, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case s.answerSlots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-s.answerSlots }()
		fn()
	}()
}

// ack acknowledges a frame so Slack does not redeliver it.
func (s *SocketRunner) ack(ctx context.Context, conn wsConn, id string) {
	body, err := json.Marshal(map[string]string{"envelope_id": id})
	if err != nil {
		return
	}
	_ = conn.Write(ctx, body)
}

// routeEvent answers a mention or direct message, ignoring the bot's own and
// other bots' messages. It is shared by the socket and events transports,
// recovers panics, and bounds the work so a hung resolver cannot pin the bot.
func routeEvent(ctx context.Context, e *Engine, r Replier, botUserID string, ev slackEvent, log io.Writer) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(log, "whodar bot: handler panic: %v\n", rec)
		}
	}()
	if ev.BotID != "" || (botUserID != "" && ev.User == botUserID) {
		return
	}
	mention := ev.Type == "app_mention"
	directMessage := ev.Type == "message" && ev.ChannelType == "im"
	if !mention && !directMessage {
		return
	}
	thread := ev.ThreadTS
	if thread == "" && mention {
		thread = ev.TS
	}
	ctx, cancel := context.WithTimeout(ctx, handleTimeout)
	defer cancel()
	event := Event{Text: ev.Text, Channel: ev.Channel, ThreadTS: thread, User: ev.User}
	if err := e.Handle(ctx, event, r); err != nil {
		fmt.Fprintf(log, "whodar bot: handle: %v\n", err)
	}
}

// dialWebSocket is the production dialer backed by the WebSocket library.
func dialWebSocket(ctx context.Context, url string) (wsConn, error) {
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	return &coderConn{c: c}, nil
}

// coderConn adapts the WebSocket library connection to wsConn.
type coderConn struct {
	// c is the underlying connection.
	c *websocket.Conn
}

// Read returns the next text message.
func (cc *coderConn) Read(ctx context.Context) ([]byte, error) {
	_, data, err := cc.c.Read(ctx)
	return data, err
}

// Write sends a text message.
func (cc *coderConn) Write(ctx context.Context, data []byte) error {
	return cc.c.Write(ctx, websocket.MessageText, data)
}

// Close closes the connection normally.
func (cc *coderConn) Close() error {
	return cc.c.Close(websocket.StatusNormalClosure, "")
}
