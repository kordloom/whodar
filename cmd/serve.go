package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/policy"
	"github.com/kordloom/whodar/internal/recall"
	"github.com/kordloom/whodar/internal/resolve"
	"github.com/kordloom/whodar/internal/web"
)

// shutdownTimeout bounds how long serve waits for in-flight requests to finish.
const shutdownTimeout = 5 * time.Second

// serveTokenEnv holds the bearer token that gates every request when the web
// UI binds beyond localhost. Serving off-loopback without it is refused.
const serveTokenEnv = "WHODAR_SERVE_TOKEN"

// webConfig carries the resolver settings shared by serve and demo.
type webConfig struct {
	// addr is the listen address.
	addr string
	// mode is the default resolver mode.
	mode string
	// model is the chat model for llm mode.
	model string
	// embedModel is the Ollama embed model.
	embedModel string
	// ollamaURL is the Ollama base URL.
	ollamaURL string
	// provider is the llm-mode provider: ollama, anthropic, openai, or gemini.
	provider string
	// openaiURL is an OpenAI-compatible base URL for the openai provider.
	openaiURL string
	// episodes holds the conversations recall answers from; nil disables it.
	episodes *episode.Store
	// recallMe is the identity the recall view starts with.
	recallMe string
	// fbStrength is how hard votes move ranking.
	fbStrength string
}

// newServeCmd builds the serve command, which runs the web UI on localhost and
// shuts down cleanly on interrupt.
func newServeCmd(opts *options) *cobra.Command {
	var cfg webConfig
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the whodar web UI on localhost",
		Long: `Serve the local web UI over the same engine as ask. Binds to localhost by
default, so nothing leaves the machine. Queries are shareable links
(/?q=who+owns+billing runs on load) and every result has feedback buttons.

The AI mode's provider row shows live readiness. Local Ollama works under the
default strict policy. Cloud providers (Claude, ChatGPT, Gemini) need their
key exported (WHODAR_ANTHROPIC_KEY, WHODAR_OPENAI_KEY, WHODAR_GEMINI_KEY) and
--policy redacted or open, since strict keeps everything on this machine.

Binding beyond localhost requires WHODAR_SERVE_TOKEN; every request must then
carry the token as a bearer header or a token query parameter, which sets a
session cookie. Put TLS in front of it for anything beyond a trusted network.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			if cfg.episodes, err = opts.loadEpisodes(); err != nil {
				return err
			}
			if cfg.recallMe = os.Getenv(meEnv); cfg.recallMe == "" {
				cfg.recallMe = gitEmail()
			}
			store := applyFeedback(ix, opts, cmd.ErrOrStderr())
			return serveWeb(cmd, opts, ix, store, cfg)
		},
	}
	addWebFlags(cmd, &cfg, "127.0.0.1:8765")
	return cmd
}

// addWebFlags registers the shared web-serving flags on cmd.
func addWebFlags(cmd *cobra.Command, cfg *webConfig, defaultAddr string) {
	f := cmd.Flags()
	f.StringVar(&cfg.addr, "addr", defaultAddr, "Address to listen on.")
	f.StringVar(&cfg.mode, "mode", "keyword", "Default resolver: keyword, semantic, or llm.")
	f.StringVar(&cfg.model, "model", "", "Ollama chat model for llm mode.")
	f.StringVar(&cfg.embedModel, "embed-model", "", "Ollama embed model for semantic/llm mode.")
	f.StringVar(&cfg.ollamaURL, "ollama-url", "http://localhost:11434", "Ollama base URL.")
	f.StringVar(&cfg.provider, "provider", "ollama",
		"LLM provider: ollama, anthropic, openai, or gemini. Cloud providers need --policy redacted or open.")
	f.StringVar(&cfg.openaiURL, "openai-url", "",
		"OpenAI-compatible base URL, e.g. a local LM Studio or vLLM server.")
	f.StringVar(&cfg.fbStrength, "feedback", "normal",
		"How hard votes move ranking: off, low, normal, or high.")
}

// recallFn returns the web recall handler, or nil when recall is unavailable.
// An answer is scoped to the person the request names, and the web app has no
// way to prove who is asking: the serve token gates access to the server, not
// to one person's history. So recall is served only on loopback, where the
// only caller is the person running whodar. Off-loopback it is disabled
// outright rather than trusting a name the caller supplies.
func recallFn(opts *options, ix *index.Index, cfg webConfig) web.RecallFunc {
	store := cfg.episodes
	if store == nil || store.Len() == 0 || !loopbackAddr(cfg.addr) {
		return nil
	}
	res := recall.New(store, ix)
	return func(ctx context.Context, person, query string, limit int) (recall.Answer, error) {
		who := res.Who(person)
		if who == "" {
			return recall.Answer{}, fmt.Errorf("%w: name who is asking", web.ErrBadRequest)
		}
		return res.Resolve(ctx, recall.Query{Text: query, Person: who, Limit: limit}), nil
	}
}

// serveWeb runs the web UI over ix until interrupted. A nil store disables
// the feedback API. Binding beyond localhost requires the serve token, which
// then gates every request.
func serveWeb(cmd *cobra.Command, opts *options, ix *index.Index, store *feedback.Store, cfg webConfig) error {
	token := os.Getenv(serveTokenEnv)
	if !loopbackAddr(cfg.addr) && token == "" {
		return fmt.Errorf("%w: %s binds beyond localhost; set %s so every request needs a token",
			ErrBadArgs, cfg.addr, serveTokenEnv)
	}
	if err := applyFeedbackStrength(ix, cfg.fbStrength); err != nil {
		return err
	}
	ask := func(ctx context.Context, query, reqMode, reqProvider string, limit int) (resolve.Answer, error) {
		if reqMode == "" {
			reqMode = cfg.mode
		}
		provider := cfg.provider
		if reqProvider != "" {
			provider = reqProvider
		}
		res, err := pickResolver(ix, opts, reqMode, cfg.model, cfg.embedModel, cfg.ollamaURL, provider, cfg.openaiURL)
		if err != nil {
			if errors.Is(err, ErrBadArgs) {
				err = fmt.Errorf("%w: %w", web.ErrBadRequest, err)
			}
			return resolve.Answer{}, err
		}
		return res.Resolve(ctx, query, limit)
	}
	var vote web.FeedbackFunc
	if store != nil {
		vote = func(e feedback.Entry) error {
			if err := store.Add(e); err != nil {
				return err
			}
			ix.SetFeedback(store.All())
			return nil
		}
	}
	person := func(id string) (resolve.JSONProfile, bool) {
		profile, ok := ix.Profile(model.ID(id))
		if !ok {
			return resolve.JSONProfile{}, false
		}
		return resolve.ProfileView(profile), true
	}
	dir := resolve.BuildDirectory(ix)
	modes := func(ctx context.Context) web.ModesReport {
		return modeReadiness(ctx, ix, opts.pol, cfg)
	}

	handler, err := web.Handler(web.Config{
		Ask: ask, Feedback: vote, Person: person, Version: version, AuthToken: token,
		Directory: &dir, Modes: modes, Recall: recallFn(opts, ix, cfg), RecallMe: cfg.recallMe,
		Log: cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	ctx := cmd.Context()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(cmd.ErrOrStderr(), "whodar serving on http://%s (Ctrl-C to stop)\n", cfg.addr)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		fmt.Fprintln(cmd.ErrOrStderr(), "whodar: shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

// loopbackAddr reports whether addr binds only the loopback interface. An
// empty host, such as ":8765", binds every interface and is not loopback.
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return isLoopbackHost(host)
}

// isLoopbackHost reports whether host names the loopback interface: the literal
// "localhost" or any loopback IP such as 127.0.0.1, 127.0.0.2, or ::1.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// modeReadiness reports what each answer mode and AI provider needs right
// now, so the UI can guide the user before they ask instead of failing after.
func modeReadiness(
	ctx context.Context, ix *index.Index, pol policy.Policy, cfg webConfig,
) web.ModesReport {
	modes := map[string]web.ModeInfo{
		"keyword": {Ready: true, Hint: "Matches your exact words, with typo tolerance. Always available."},
	}
	if ix.HasEmbeddings() {
		modes["semantic"] = web.ModeInfo{
			Ready: true,
			Hint: "Matches by meaning, so \"failed payments\" can find \"billing retries\". " +
				"Uses this index's local embeddings.",
		}
	} else {
		modes["semantic"] = web.ModeInfo{
			Ready: false,
			Hint: "Matches by meaning, so \"failed payments\" can find \"billing retries\". " +
				"This index has none yet: rebuild it with whodar index --embed (uses local Ollama).",
		}
	}

	providers := map[string]web.ModeInfo{
		"ollama":    ollamaReadiness(ctx, cfg),
		"anthropic": cloudReadiness(pol, "Claude", anthropicKeyEnv),
		"openai":    cloudReadiness(pol, "ChatGPT", openaiKeyEnv),
		"gemini":    cloudReadiness(pol, "Gemini", geminiKeyEnv),
	}
	provider := cfg.provider
	if provider == "" {
		provider = "ollama"
	}
	modes["llm"] = providers[provider]

	return web.ModesReport{Modes: modes, Providers: providers, Provider: provider}
}

// ollamaReadiness live-probes the local Ollama server.
func ollamaReadiness(ctx context.Context, cfg webConfig) web.ModeInfo {
	model := cfg.model
	if model == "" {
		model = "llama3.1"
	}
	if ollamaUp(ctx, cfg.ollamaURL) {
		return web.ModeInfo{
			Ready: true,
			Hint: "A model on this machine (" + model + ") picks the best matches and writes " +
				"a recommendation. Nothing leaves your computer.",
		}
	}
	return web.ModeInfo{
		Ready: false,
		Hint: "Runs a private model on this machine, but Ollama is not running. Install it " +
			"from ollama.com, run: ollama pull " + model + ", then pick it again.",
	}
}

// cloudReadiness checks a cloud provider's key and the egress policy.
func cloudReadiness(pol policy.Policy, name, keyEnv string) web.ModeInfo {
	if pol.Mode() == policy.Strict {
		return web.ModeInfo{
			Ready: false,
			Hint: name + " is off under the strict policy. Restart serve with --policy " +
				"redacted (sends only anonymized candidates) or open.",
		}
	}
	if os.Getenv(keyEnv) == "" {
		return web.ModeInfo{
			Ready: false,
			Hint:  "To use " + name + ", set " + keyEnv + " and restart serve.",
		}
	}
	return web.ModeInfo{
		Ready: true,
		Hint: name + " picks the best matches and writes a recommendation, under the " +
			pol.Mode().String() + " policy.",
	}
}

// ollamaUp reports whether a local Ollama answers at base within a short
// timeout. Only loopback hosts are probed; a remote host is left to the
// egress guard at ask time and assumed reachable here.
func ollamaUp(ctx context.Context, base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	if !loopbackAddr(u.Host) {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/tags", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
