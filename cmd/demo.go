package cmd

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/feedback"
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
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Explore whodar on a simulated company",
		Long: `Build an index from a simulated company and serve the web UI over it. The
simulation covers all eight sources, so identity joins, recency, confidence,
and feedback all behave as they would on real data. Nothing is fetched from
the network and no credentials are needed. Sample data only; it is discarded
when the demo stops.

To try cloud AI on the sample data, export a provider key (such as
WHODAR_ANTHROPIC_KEY) and add --policy redacted.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.MkdirTemp("", "whodar-demo-*")
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			defer func() { _ = os.RemoveAll(dir) }()

			fmt.Fprintln(cmd.ErrOrStderr(), "whodar demo: indexing a simulated company (sample data only)")
			ix, err := simorg.BuildIndex(dir)
			if err != nil {
				return err
			}
			store, err := feedback.Load(dir+"/feedback.json", nil)
			if err != nil {
				return err
			}
			if cfg.episodes, err = simorg.BuildEpisodes(ix); err != nil {
				return err
			}
			// The demo has no real user, so recall starts as the person the
			// sample conversations were had with.
			cfg.recallMe = demoPerson

			link := "http://" + cfg.addr + "/?q=" + url.QueryEscape(demoQuery)
			fmt.Fprintf(cmd.ErrOrStderr(),
				"whodar demo: %d people, %d channels across 8 sources\nwhodar demo: try %s\n",
				len(ix.Graph.People), len(ix.Graph.Channels), link)
			go openBrowser(link)

			return serveWeb(cmd, opts, ix, store, cfg)
		},
	}
	addWebFlags(cmd, &cfg, "127.0.0.1:8765")
	return cmd
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
