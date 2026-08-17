package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/bot"
	"github.com/kordloom/whodar/internal/resolve"
	"github.com/kordloom/whodar/internal/slack"
)

// Bot environment variables. The bot token is shared with the index command.
const (
	// slackAppTokenEnv holds the app-level token (xapp-) for socket mode.
	slackAppTokenEnv = "WHODAR_SLACK_APP_TOKEN"
	// slackSigningSecretEnv holds the signing secret for the events transport.
	slackSigningSecretEnv = "WHODAR_SLACK_SIGNING_SECRET"
)

// slackReplier posts bot answers through the Slack Web API.
type slackReplier struct {
	// client posts messages.
	client *slack.Client
}

// Reply posts text to channel, threading under threadTS when set.
func (s slackReplier) Reply(ctx context.Context, channel, threadTS, text string) error {
	return s.client.PostMessage(ctx, channel, threadTS, text)
}

// newBotCmd builds the bot command, which answers questions from Slack over
// Socket Mode or the Events API.
func newBotCmd(opts *options) *cobra.Command {
	var (
		transport  string
		mode       string
		model      string
		embedModel string
		provider   string
		openaiURL  string
		fbStrength string
		ollamaURL  string
		addr       string
		limit      int
	)
	cmd := &cobra.Command{
		Use:   "bot",
		Short: "Run the Slack bot",
		Long: `Run the Slack bot. Mention it in a channel or send it a direct message; a
trailing --llm or --keyword in a message overrides the mode for that answer.

Transports and their credentials:
  socket  no public URL needed     WHODAR_SLACK_TOKEN + WHODAR_SLACK_APP_TOKEN
  events  serves HTTP for Slack    WHODAR_SLACK_TOKEN + WHODAR_SLACK_SIGNING_SECRET`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			botToken := os.Getenv(slackTokenEnv)
			if botToken == "" {
				return fmt.Errorf("%w: set %s", ErrBadArgs, slackTokenEnv)
			}
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			applyFeedback(ix, opts, cmd.ErrOrStderr())
			if err := applyFeedbackStrength(ix, fbStrength); err != nil {
				return err
			}

			botClient := slack.New(botToken)
			botUserID, err := botClient.AuthTest(cmd.Context())
			if err != nil {
				return fmt.Errorf("slack auth: %w", err)
			}

			ask := func(ctx context.Context, query, reqMode string, n int) (resolve.Answer, error) {
				if reqMode == "" {
					reqMode = mode
				}
				res, err := pickResolver(ix, opts, reqMode, model, embedModel, ollamaURL, provider, openaiURL)
				if err != nil {
					return resolve.Answer{}, err
				}
				return res.Resolve(ctx, query, n)
			}
			engine := bot.New(ask, mode, botUserID, limit)
			replier := slackReplier{client: botClient}

			switch transport {
			case "", "socket":
				return runSocketBot(cmd, engine, replier, botUserID)
			case "events":
				return runEventsBot(cmd, engine, replier, botUserID, addr)
			default:
				return fmt.Errorf("%w: transport %q (want socket or events)", ErrBadArgs, transport)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&transport, "transport", "socket", "Slack transport: socket or events.")
	f.StringVar(&mode, "mode", "keyword", "Default answer mode: keyword, semantic, or llm.")
	f.StringVar(&model, "model", "", "Ollama chat model for llm mode.")
	f.StringVar(&embedModel, "embed-model", "", "Ollama embed model for semantic/llm mode.")
	f.StringVar(&ollamaURL, "ollama-url", "http://localhost:11434", "Ollama base URL.")
	f.StringVar(&provider, "provider", "ollama",
		"LLM provider: ollama, anthropic, openai, or gemini. Cloud providers need --policy redacted or open.")
	f.StringVar(&openaiURL, "openai-url", "",
		"OpenAI-compatible base URL, e.g. a local LM Studio or vLLM server.")
	f.StringVar(&fbStrength, "feedback", "normal",
		"How hard votes move ranking: off, low, normal, or high.")
	f.StringVar(&addr, "addr", "127.0.0.1:8766", "Address for the events transport HTTP server.")
	f.IntVar(&limit, "limit", 5, "Maximum results per section.")
	return cmd
}

// runSocketBot runs the bot over Socket Mode until interrupted.
func runSocketBot(cmd *cobra.Command, engine *bot.Engine, replier bot.Replier, botUserID string) error {
	appToken := os.Getenv(slackAppTokenEnv)
	if appToken == "" {
		return fmt.Errorf("%w: set %s for socket transport", ErrBadArgs, slackAppTokenEnv)
	}
	runner := bot.NewSocketRunner(slack.New(appToken), engine, replier, botUserID,
		bot.WithLog(cmd.ErrOrStderr()), bot.WithResponder(bot.ResponderFunc(slack.Respond)))
	fmt.Fprintln(cmd.ErrOrStderr(), "whodar bot: connected over socket mode (Ctrl-C to stop)")
	return runner.Run(cmd.Context())
}

// runEventsBot serves the Slack Events API until interrupted.
func runEventsBot(cmd *cobra.Command, engine *bot.Engine, replier bot.Replier, botUserID, addr string) error {
	secret := os.Getenv(slackSigningSecretEnv)
	if secret == "" {
		return fmt.Errorf("%w: set %s for events transport", ErrBadArgs, slackSigningSecretEnv)
	}
	handler := bot.NewEventsHandler(engine, replier, botUserID, secret, bot.WithEventsLog(cmd.ErrOrStderr()))
	slash := bot.NewSlashHandler(engine, bot.ResponderFunc(slack.Respond), secret,
		bot.WithSlashLog(cmd.ErrOrStderr()))
	mux := http.NewServeMux()
	mux.Handle("/slack/events", handler)
	mux.Handle("/slack/commands", slash)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx := cmd.Context()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(cmd.ErrOrStderr(),
		"whodar bot: events endpoint on http://%s/slack/events (Ctrl-C to stop)\n", addr)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		fmt.Fprintln(cmd.ErrOrStderr(), "whodar bot: shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
