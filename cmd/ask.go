package cmd

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/fact"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/llm"
	"github.com/kordloom/whodar/internal/policy"
	"github.com/kordloom/whodar/internal/resolve"
)

// Cloud provider credentials come only from the environment, never flags.
const (
	// anthropicKeyEnv holds the Claude API key.
	anthropicKeyEnv = "WHODAR_ANTHROPIC_KEY"
	// openaiKeyEnv holds the OpenAI-compatible API key.
	openaiKeyEnv = "WHODAR_OPENAI_KEY"
	// geminiKeyEnv holds the Gemini API key.
	geminiKeyEnv = "WHODAR_GEMINI_KEY"
)

// Gemini speaks the OpenAI-compatible protocol at Google's endpoint, so the
// openai client serves it with a base URL and a Gemini model name.
const (
	// geminiHost is the egress destination checked against the policy.
	geminiHost = "generativelanguage.googleapis.com"
	// geminiBaseURL is Google's OpenAI-compatible API root.
	geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	// defaultGeminiModel is used when --model is not set.
	defaultGeminiModel = "gemini-2.5-flash"
)

// newAskCmd builds the ask command, which answers a question from the index.
func newAskCmd(opts *options) *cobra.Command {
	var (
		limit      int
		mode       string
		model      string
		embedModel string
		ollamaURL  string
		provider   string
		openaiURL  string
		fbStrength string
	)
	cmd := &cobra.Command{
		Use:   "ask [question]",
		Short: "Ask who to talk to about something",
		Long: `Answer a question from the index: the people to talk to and the channels to
ask in, each with reasons and a confidence from zero to one.

Modes:
  keyword   no model, deterministic, always works (default)
  semantic  blend meaning with your words; needs an index built with --embed
  llm       a local Ollama model re-ranks and writes a recommendation

Examples:
  whodar ask "who do I talk to about billing retries"
  whodar ask --mode llm "where do I ask about kafka"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			applyFeedback(ix, opts, cmd.ErrOrStderr())
			if err := applyFeedbackStrength(ix, fbStrength); err != nil {
				return err
			}
			res, err := pickResolver(ix, opts, mode, model, embedModel, ollamaURL, provider, openaiURL)
			if err != nil {
				return err
			}
			query := strings.Join(args, " ")
			ans, err := res.Resolve(cmd.Context(), query, limit)
			if err != nil {
				return err
			}
			warnEmptyAsk(cmd, ix, query, ans)
			view := ans.View(query)
			if err := opts.render(cmd.OutOrStdout(), view, func(w io.Writer, s style) {
				renderAsk(w, query, view, s)
			}); err != nil {
				return err
			}
			showRelatedFacts(cmd, opts, query)
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 5, "Maximum number of results per section.")
	f.StringVar(&mode, "mode", "keyword", "Resolver: keyword, semantic, or llm.")
	f.StringVar(&model, "model", "", "Ollama chat model for --mode llm (default llama3.1).")
	f.StringVar(&embedModel, "embed-model", "", "Ollama embed model for semantic/llm (default nomic-embed-text).")
	f.StringVar(&ollamaURL, "ollama-url", "http://localhost:11434", "Ollama base URL.")
	f.StringVar(&provider, "provider", "ollama",
		"LLM provider: ollama, anthropic, openai, or gemini. Cloud providers need --policy redacted or open.")
	f.StringVar(&openaiURL, "openai-url", "",
		"OpenAI-compatible base URL including the version path, e.g. http://localhost:1234/v1.")
	f.StringVar(&fbStrength, "feedback", "normal",
		"How hard votes move ranking: off, low, normal, or high.")
	return cmd
}

// warnEmptyAsk explains an empty answer on stderr so it does not read as a
// silent success. An empty index, a query with no term the index knows, and a
// genuine miss are different problems with different fixes, and the JSON on
// stdout cannot tell them apart.
func warnEmptyAsk(cmd *cobra.Command, ix *index.Index, query string, ans resolve.Answer) {
	if len(ans.People) > 0 || len(ans.Channels) > 0 {
		return
	}
	w := cmd.ErrOrStderr()
	if ix.Graph == nil || len(ix.Graph.People) == 0 {
		fmt.Fprintln(w, "No one is indexed yet: run `whodar index` against a source first.")
		return
	}
	// Ask ranks by who knows a subject. A question naming a person is answered
	// directly, but a team, a channel, or a half-remembered name still returns
	// nothing while the entity sits in the index, so offer the closest matches
	// by name before the generic hint and point a misdirected question
	// somewhere useful.
	if hits := suggestFromSearch(ix, query, 5); len(hits) > 0 {
		fmt.Fprintln(w, "No expertise match for that. Closest by name:")
		for _, h := range hits {
			label := h.Name
			if h.Kind == "channel" {
				label = "#" + h.Name
			}
			if ctx := firstNonEmpty(h.Title, h.Team); ctx != "" {
				label += " (" + ctx + ")"
			}
			fmt.Fprintln(w, "  "+label)
		}
		fmt.Fprintln(w, "Use `whodar search` to explore, or ask with the terms your team would use.")
		return
	}
	fmt.Fprintln(w,
		"No match for that question. Matching is on the words people and channels are described by, "+
			"so try the terms your team would use, or `whodar directory` to see what is indexed.")
}

// firstNonEmpty returns the first non-empty string, or empty when all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// suggestFromSearch finds the closest entities for an ask that had no expertise
// match. It searches each meaningful word of the question, not the whole phrase,
// since a natural-language question is never a substring of one short field, then
// merges the hits by score. It falls back to the whole query when the question
// has no searchable words.
func suggestFromSearch(ix *index.Index, query string, limit int) []resolve.SearchResult {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return resolve.Search(ix, query, limit)
	}
	seen := make(map[string]bool)
	var all []resolve.SearchResult
	for _, t := range terms {
		for _, r := range resolve.Search(ix, t, limit) {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			all = append(all, r)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Name < all[j].Name
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// showRelatedFacts prints any recorded facts whose subject, object, or detail
// mentions a query term, so a fact somebody typed appears alongside the crawled
// answer, labeled with its source and date. Facts are supplementary, so a
// failure to load them never fails the answer.
func showRelatedFacts(cmd *cobra.Command, opts *options, query string) {
	store, err := fact.Load(opts.factsPath())
	if err != nil {
		return
	}
	terms := queryTerms(query)
	if len(terms) == 0 {
		return
	}
	var hits []fact.Fact
	for _, f := range store.List(fact.Filter{}) {
		if factMentions(f, terms) {
			hits = append(hits, f)
		}
	}
	if len(hits) == 0 {
		return
	}
	w := cmd.ErrOrStderr()
	fmt.Fprintln(w, "Recorded facts:")
	writeFactLines(w, hits)
}

// queryTerms lowercases the query and returns its words of three or more
// letters or digits, the terms a fact is matched against.
func queryTerms(q string) []string {
	var out []string
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	for _, w := range fields {
		if len(w) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// factMentions reports whether any term appears in the fact's subject, object,
// or detail.
func factMentions(f fact.Fact, terms []string) bool {
	hay := strings.ToLower(f.Subject + " " + f.Object + " " + f.Detail)
	for _, t := range terms {
		if strings.Contains(hay, t) {
			return true
		}
	}
	return false
}

// pickResolver builds the resolver for the chosen mode. Semantic mode and the
// default ollama provider target a local server; anything non-local is gated
// by the egress policy. Cloud providers additionally run redacted under the
// redacted policy, so no names or emails leave the machine.
func pickResolver(ix *index.Index, opts *options, mode, model, embedModel, ollamaURL string, provider, openaiURL string) (resolve.Resolver, error) {
	switch mode {
	case "", "keyword":
		return resolve.NewKeyword(ix), nil
	case "semantic":
		if provider != "" && provider != "ollama" {
			return nil, fmt.Errorf("%w: semantic mode needs local embeddings; use --provider ollama", ErrBadArgs)
		}
		if !ix.HasEmbeddings() {
			return nil, fmt.Errorf(
				"%w: this index has no embeddings, so semantic mode has nothing to match against. "+
					"Re-index with --embed, or ask with --mode keyword", ErrBadArgs)
		}
		if err := guardLLMHost(opts.pol, ollamaURL); err != nil {
			return nil, err
		}
		return resolve.NewSemantic(ix, newOllama(model, embedModel, ollamaURL)), nil
	case "llm":
		switch provider {
		case "", "ollama":
			if err := guardLLMHost(opts.pol, ollamaURL); err != nil {
				return nil, err
			}
			client := newOllama(model, embedModel, ollamaURL)
			return resolve.NewLLM(ix, client, client), nil
		case "anthropic", "openai", "gemini":
			chat, err := cloudChatter(opts.pol, provider, model, openaiURL)
			if err != nil {
				return nil, err
			}
			if opts.pol.Mode() == policy.Redacted {
				return resolve.NewRedactedLLM(ix, chat, nil), nil
			}
			return resolve.NewLLM(ix, chat, nil), nil
		default:
			return nil, fmt.Errorf(
				"%w: provider %q (want ollama, anthropic, openai, or gemini)", ErrBadArgs, provider)
		}
	default:
		return nil, fmt.Errorf("%w: mode %q (want keyword, semantic, or llm)", ErrBadArgs, mode)
	}
}

// cloudChatter builds a chat client for the anthropic, openai, or gemini
// provider, gated by the egress policy. Strict denies anything non-local.
// Redacted permits only the known provider hosts, with redaction applied by
// the resolver. A local --openai-url, such as LM Studio, counts as local and
// needs no opt-in; a remote one needs open. Keys come only from the
// environment.
func cloudChatter(pol policy.Policy, provider, model, openaiURL string) (resolve.Chatter, error) {
	switch provider {
	case "anthropic":
		if err := pol.AllowEgress("api.anthropic.com"); err != nil {
			return nil, cloudDenied(provider, err)
		}
		key := os.Getenv(anthropicKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("%w: set %s", ErrBadArgs, anthropicKeyEnv)
		}
		return llm.NewAnthropic(key,
			llm.WithAnthropicModel(model), llm.WithAnthropicTransport(policy.Transport(nil, pol))), nil
	case "gemini":
		if err := pol.AllowEgress(geminiHost); err != nil {
			return nil, cloudDenied(provider, err)
		}
		key := os.Getenv(geminiKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("%w: set %s", ErrBadArgs, geminiKeyEnv)
		}
		if model == "" {
			model = defaultGeminiModel
		}
		return llm.NewOpenAI(key, llm.WithOpenAIModel(model),
			llm.WithOpenAIBaseURL(geminiBaseURL), llm.WithOpenAITransport(policy.Transport(nil, pol))), nil
	}

	key := os.Getenv(openaiKeyEnv)
	clientOpts := []llm.OpenAIOption{llm.WithOpenAITransport(policy.Transport(nil, pol))}
	if model != "" {
		clientOpts = append(clientOpts, llm.WithOpenAIModel(model))
	}
	if openaiURL != "" {
		if err := guardLLMHost(pol, openaiURL); err != nil {
			return nil, err
		}
		clientOpts = append(clientOpts, llm.WithOpenAIBaseURL(openaiURL))
	} else {
		if err := pol.AllowEgress("api.openai.com"); err != nil {
			return nil, cloudDenied(provider, err)
		}
		if key == "" {
			return nil, fmt.Errorf("%w: set %s (or point --openai-url at a local server)", ErrBadArgs, openaiKeyEnv)
		}
	}
	return llm.NewOpenAI(key, clientOpts...), nil
}

// cloudDenied explains a policy denial with the way to opt in.
func cloudDenied(provider string, err error) error {
	return fmt.Errorf(
		"cloud provider %s: %w (use --policy redacted to send anonymized candidates, or --policy open)",
		provider, err)
}

// newOllama builds an Ollama client for the chat and embed models.
func newOllama(model, embedModel, ollamaURL string) *llm.Ollama {
	return llm.New(model, llm.WithBaseURL(ollamaURL), llm.WithEmbedModel(embedModel))
}

// newDocOllama builds the client indexing embeds with. Stored items and the
// questions asked of them carry different task prefixes on models trained
// asymmetrically, so the index-time client is marked as the document side.
func newDocOllama(embedModel, ollamaURL string) *llm.Ollama {
	return llm.New("", llm.WithBaseURL(ollamaURL), llm.WithEmbedModel(embedModel),
		llm.WithEmbedTask(llm.EmbedDocuments))
}

// guardLLMHost permits a loopback model host unconditionally and requires the
// open policy for any other host. The semantic and Ollama paths send full
// profile text with no redaction, so a non-loopback host would leak names,
// emails, and titles. Redacted's known-provider allowance is sound only on the
// cloud path, which anonymizes candidates, so it must not apply here: only open,
// where the operator accepts unrestricted egress, admits a remote host.
func guardLLMHost(pol policy.Policy, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid model url %q: %v", ErrBadArgs, raw, err)
	}
	host := u.Hostname()
	if u.Opaque != "" || host == "" {
		return fmt.Errorf("%w: model url %q has no host", ErrBadArgs, raw)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if pol.Mode() != policy.Open {
		return fmt.Errorf(
			"%w: model host %s needs --policy open; it receives unredacted profiles",
			policy.ErrEgressDenied, host)
	}
	return nil
}
