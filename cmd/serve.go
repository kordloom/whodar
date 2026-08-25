package cmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/attest"
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/policy"
	"github.com/kordloom/whodar/internal/recall"
	"github.com/kordloom/whodar/internal/report"
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
	// public serves open to any caller with no token, even off loopback. It is
	// meant only for the demo, whose index and recall are sample data with
	// nothing private to protect.
	public bool
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
			if cfg.episodes, err = opts.loadEpisodes(cmd); err != nil {
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
// An answer is scoped to the person the request names, and the web app cannot
// prove who is asking: a serve token gates the server, not one person's
// history, and one token cannot tell two people apart. So recall is served
// only where the caller can only be the person running whodar: a loopback bind
// with no token. A token means the server is shared, whether directly or
// behind a proxy that forwards to loopback, and recall stays off. The demo is
// the exception: its history is sample data, so it serves recall openly.
func recallFn(ix *index.Index, cfg webConfig, token string) web.RecallFunc {
	store := cfg.episodes
	if store == nil || store.Len() == 0 || token != "" || (!loopbackAddr(cfg.addr) && !cfg.public) {
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
// then gates every request, unless cfg.public marks the index as sample data.
func serveWeb(cmd *cobra.Command, opts *options, ix *index.Index, store *feedback.Store, cfg webConfig) error {
	token := os.Getenv(serveTokenEnv)
	if !loopbackAddr(cfg.addr) && token == "" && !cfg.public {
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
		return resolve.ProfileView(ix, profile), true
	}
	dir := resolve.BuildDirectory(ix)
	modes := func(ctx context.Context) web.ModesReport {
		return modeReadiness(ctx, ix, opts.pol, cfg)
	}

	handler, err := web.Handler(web.Config{
		Ask: ask, Feedback: vote, Person: person, Version: version, AuthToken: token,
		Directory: &dir, Modes: modes, Recall: recallFn(ix, cfg, token), RecallMe: cfg.recallMe,
		Search: func(q string, limit int) []resolve.SearchResult { return resolve.Search(ix, q, limit) },
		Exposure: func() web.Exposure {
			return web.Exposure{
				Risk:    resolve.Risk(ix, 0),
				Drift:   resolve.Ownership(ix).Drift,
				Regions: resolve.Regions(ix, regionsShown),
			}
		},
		Brief: func() report.Brief {
			all := resolve.Risk(ix, 0)
			exposed := report.Exposures(all)
			return report.Brief{
				Generated: time.Now(),
				People:    len(ix.Graph.People),
				Scored:    len(all),
				Sources:   ix.SourceNames(),
				Risks:     all,
				Regions:   resolve.Regions(ix, regionsShown),
				Spans:     resolve.SoleSpans(ix, spansShown),
				Totals:    report.Count(all, exposed),
				Exposed:   exposed,
			}
		},
		Departure: func(person string) resolve.DepartureImpact {
			return resolve.Departure(ix, person)
		},
		Attest: attestFn(ix, opts, cmd.ErrOrStderr()),
		Related: func(topic string, limit int) []resolve.TopicRelation {
			return resolve.Related(ix, topic, limit)
		},
		CLI:   cliFn(ix, ask, attestFn(ix, opts, cmd.ErrOrStderr())),
		Log:   cmd.ErrOrStderr(),
		Ready: func() bool { return ix != nil && len(ix.Graph.People) > 0 },
	})
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	// Bind before announcing. Serving is not worth printing until the port is
	// actually held, and a bind failure underneath a success line reads as a
	// server that started and then died.
	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("%w: cannot bind %s: %w: pass --addr to use a free port", ErrServe, cfg.addr, err)
	}

	ctx := cmd.Context()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
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

// cliAskQuery is the question the command line view answers, chosen so the same
// question can sit beside a recorded run that had a model, showing what the
// model adds rather than claiming it finds someone the words alone would miss.
const cliAskQuery = "kafka consumer lag keeps climbing, who can help"

// cliFn renders what the command line prints for this same index, through the
// very renderers the terminal uses, with color turned off. The web views answer
// these questions in their own way; this exists so a reader can see the tool as
// an engineer would run it, against the data in front of them, rather than take
// a screenshot's word for it.
func cliFn(ix *index.Index, ask web.AskFunc, seal web.AttestFunc) web.CLIFunc {
	var plain style
	return func(name string) (string, bool) {
		var b strings.Builder
		switch name {
		case "ask":
			answer, err := ask(context.Background(), cliAskQuery, "keyword", "", 5)
			if err != nil {
				return "", false
			}
			renderAsk(&b, cliAskQuery, answer.View(cliAskQuery), plain)
		case "risk":
			renderRisk(&b, resolve.Risk(ix, 12), plain)
		case "ownership":
			renderOwnership(&b, resolve.Ownership(ix), plain)
		case "related":
			topic := "billing"
			if top := resolve.Risk(ix, 1); len(top) > 0 {
				topic = top[0].Topic
			}
			renderRelated(&b, topic, resolve.Related(ix, topic, 8), plain)
		case "attest":
			if seal == nil {
				return "", false
			}
			bundle, err := seal()
			if err != nil {
				return "", false
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, bundle, "", "  "); err != nil {
				b.Write(bundle)
				break
			}
			b.Write(pretty.Bytes())
		default:
			return "", false
		}
		return b.String(), true
	}
}

// attestFn returns the web handler that seals the current knowledge-risk finding
// into a signed LoomSeal bundle. The signing key is the persistent one under the
// data directory where that is writable. A hardened or read-only deployment,
// such as the public demo, cannot keep one, so it signs with a key held only in
// memory: the bundle carries its own public key, so it still verifies on its own
// terms, it just does not claim the same identity across restarts.
func attestFn(ix *index.Index, opts *options, logw io.Writer) web.AttestFunc {
	priv, err := opts.attestKey()
	if err != nil {
		_, key, genErr := ed25519.GenerateKey(nil)
		if genErr != nil {
			fmt.Fprintf(logw, "whodar: attest disabled: %v\n", genErr)
			return nil
		}
		fmt.Fprintf(logw, "whodar: attest signing with an in-memory key (%v)\n", err)
		priv = key
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil
	}
	return func() ([]byte, error) {
		payload, evidence := attestPayload(resolve.Risk(ix, 0))
		return attest.Seal(priv, "whodar", version, attest.InstallID(pub),
			"whodar.knowledge-risk/1", map[string]any{"id": "organization", "type": "fleet"},
			payload, evidence, time.Now())
	}
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

	report := web.ModesReport{Modes: modes, Providers: providers, Provider: provider}
	if cfg.public {
		report = demoModeHints(report)
	}
	return report
}

// demoModeHints rewrites the hints a public demo shows for the modes it does not
// run. The local hints tell the reader to install Ollama, rebuild the index, or
// restart the server, which is advice a visitor cannot act on and which makes a
// deliberate choice read as a broken feature. What is true is better: ranking is
// deterministic and needs no model, a model is an upgrade you host yourself, and
// a public server must never hold a provider key.
func demoModeHints(r web.ModesReport) web.ModesReport {
	const (
		semanticHint = "Matches by meaning, so \"failed payments\" can find \"billing retries\". " +
			"It runs on an embedding model you host yourself, so this public demo stays keyword only. " +
			"Every answer you see here is deterministic, with no model involved."
		modelHint = "Whodar ranks deterministically and explains every answer, so a model is an " +
			"upgrade rather than a requirement. Run whodar yourself to point it at a local model, " +
			"or at Claude, ChatGPT, or Gemini under a policy you set."
		cloudHint = " This public demo carries no provider key on purpose: a shared server holding " +
			"one would spend someone else's account."
	)
	if m, ok := r.Modes["semantic"]; ok && !m.Ready {
		m.Hint = semanticHint
		r.Modes["semantic"] = m
	}
	for name, p := range r.Providers {
		if p.Ready {
			continue
		}
		p.Hint = modelHint
		if name != "ollama" {
			p.Hint += cloudHint
		}
		r.Providers[name] = p
	}
	if m, ok := r.Modes["llm"]; ok && !m.Ready {
		r.Modes["llm"] = r.Providers[r.Provider]
	}
	return r
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
