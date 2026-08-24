package cmd

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/simorg"
)

// demoPerson is who the demo's recall view starts as: the engineer the sample
// conversations involve.
const demoPerson = "angela@corp.com"

// demoQuery is the question the demo opens with, so the first thing a new
// user sees is an answered page.
const demoQuery = "who do I talk to about billing retries"

// newDemoCmd builds the demo command, which explores whodar on a simulated
// company with no credentials or setup.
func newDemoCmd(opts *options) *cobra.Command {
	var cfg webConfig
	var big bool
	var saveIndex string
	var embed bool
	open := true
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Explore whodar on a simulated company",
		Long: `Build an index from a simulated company and serve the web UI over it. The
simulation covers all eight sources, so identity joins, recency, confidence,
and feedback all behave as they would on real data. Nothing is fetched from
the network and no credentials are needed. Sample data only; it is discarded
when the demo stops.

To try cloud AI on the sample data, export a provider key (such as
WHODAR_ANTHROPIC_KEY) and add --policy redacted. Only do this for a demo you keep
to yourself: the demo serves with no token, so a publicly reachable one with a key
set becomes an open relay to your paid account.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.MkdirTemp("", "whodar-demo-*")
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			defer func() { _ = os.RemoveAll(dir) }()

			buildIndex, buildEpisodes := simorg.BuildIndex, simorg.BuildEpisodes
			recallMe, query, sources := demoPerson, demoQuery, 8
			if big {
				buildIndex, buildEpisodes = simorg.BuildBigIndex, simorg.BuildBigEpisodes
				recallMe, query = simorg.BigDemoPerson()
				sources = 8
			}

			fmt.Fprintln(cmd.ErrOrStderr(), "whodar demo: indexing a simulated company (sample data only)")
			ix, err := buildIndex(dir)
			if err != nil {
				return err
			}
			store, err := feedback.Load(dir+"/feedback.json", nil)
			if err != nil {
				return err
			}
			if cfg.episodes, err = buildEpisodes(ix); err != nil {
				return err
			}
			if saveIndex != "" {
				return saveDemo(cmd, ix, cfg.episodes, saveIndex, embed, cfg)
			}
			// The demo is public sample data, so it serves open with no token
			// and, having no real user, starts recall as the person the sample
			// conversations were had with.
			cfg.public = true
			cfg.recallMe = recallMe

			link := "http://" + cfg.addr + "/?q=" + url.QueryEscape(query)
			fmt.Fprintf(cmd.ErrOrStderr(),
				"whodar demo: %d people, %d channels across %d sources\nwhodar demo: try %s\n",
				len(ix.Graph.People), len(ix.Graph.Channels), sources, link)
			if open {
				go openBrowser(link)
			}

			return serveWeb(cmd, opts, ix, store, cfg)
		},
	}
	addWebFlags(cmd, &cfg, "127.0.0.1:8765")
	cmd.Flags().BoolVar(&big, "big", false,
		"Serve a large simulated company of 200+ people instead of the small sample")
	cmd.Flags().BoolVar(&open, "open", true, "Open the demo in a browser on start")
	cmd.Flags().StringVar(&saveIndex, "save-index", "",
		"Write the simulated company to this directory as a real index, then exit")
	cmd.Flags().BoolVar(&embed, "embed", false,
		"Generate embeddings while saving, so the saved index answers in semantic mode. Needs Ollama.")
	return cmd
}

// saveDemo writes the simulated company to dir as a real index, so the same
// sample data the web demo serves can be driven from the command line: ask,
// risk, ownership, related, and attest all read an index from a data directory.
// Embedding is opt-in because it needs a local model and takes a while.
func saveDemo(
	cmd *cobra.Command, ix *index.Index, eps *episode.Store, dir string, embed bool, cfg webConfig,
) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	if embed {
		fmt.Fprintln(cmd.ErrOrStderr(), "whodar demo: embedding, this takes a minute")
		if err := ix.Embed(cmd.Context(), newDocOllama(cfg.embedModel, cfg.ollamaURL)); err != nil {
			return fmt.Errorf("demo: embed: %w", err)
		}
	}
	if err := ix.Save(filepath.Join(dir, "index.json")); err != nil {
		return fmt.Errorf("demo: save index: %w", err)
	}
	if eps != nil {
		if err := eps.Save(filepath.Join(dir, "episodes.json")); err != nil {
			return fmt.Errorf("demo: save episodes: %w", err)
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"whodar demo: wrote the sample company to %s\nwhodar demo: try whodar --data-dir %s risk\n",
		dir, dir)
	return nil
}

// openBrowser makes a best-effort attempt to open link in the default
// browser once the server has had a moment to come up.
func openBrowser(link string) {
	time.Sleep(300 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	_ = cmd.Run()
}
