package ownersbench

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/github"
	"github.com/kordloom/whodar/internal/index"
)

// TestReviewSignalProbe is a one-off experiment runner, gated behind env
// vars: it merges a git-history index with a year of live GitHub review and
// pull data for one repository and saves the result, so the owners benchmark
// can measure what review signal is worth. It is not a test of anything.
func TestReviewSignalProbe(t *testing.T) {
	repoPath := os.Getenv("WHODAR_PROBE_REPO")
	repoFull := os.Getenv("WHODAR_PROBE_GITHUB")
	outDir := os.Getenv("WHODAR_PROBE_OUT")
	token := os.Getenv("WHODAR_GITHUB_TOKEN")
	if repoPath == "" || repoFull == "" || outDir == "" || token == "" {
		t.Skip("probe env not set")
	}

	git := connector.NewGitHistory(connector.GitOptions{Paths: []string{repoPath}, SinceDays: 730, Log: os.Stderr})
	gitRecs, err := git.Fetch(context.Background())
	if err != nil {
		t.Fatalf("git: %v", err)
	}

	gh := connector.NewGitHubWithClient(github.New(token), connector.GitHubOptions{
		Repos: []string{repoFull},
		Since: time.Now().AddDate(-1, 0, 0),
		Log:   os.Stderr,
	})
	ghRecs, err := gh.Fetch(context.Background())
	if err != nil {
		t.Fatalf("github: %v", err)
	}
	t.Logf("git records: %d, github records: %d", len(gitRecs), len(ghRecs))

	ix := index.New()
	ix.Add(gitRecs)
	ix.Add(ghRecs)
	ix.AutoJoin()
	ix.Canonicalize()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := ix.Save(outDir + "/index.json"); err != nil {
		t.Fatalf("save: %v", err)
	}
	t.Logf("saved merged index: %d people", len(ix.Graph.People))
}
