package connector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// newPullRepo builds a repository whose changes land through merge commits
// naming their pull request, the shape GitHub writes, alongside plenty of
// ordinary authored commits.
func newPullRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, nil, "init", "-q", "-b", "main")
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("README.md", "start\n")
	runGit(t, dir, nil, "add", "README.md")
	runGit(t, dir, authorEnv("Base", "base@x.com", "2026-01-01T10:00:00Z"), "commit", "-q", "-m", "init")

	// Two pull requests, each landing on its own branch through a no-ff merge.
	for i, area := range []string{"scheduler", "storage"} {
		branch := fmt.Sprintf("pr%d", i+1)
		runGit(t, dir, nil, "checkout", "-q", "-b", branch)
		write(area+"/impl.go", "package "+area+"\n")
		runGit(t, dir, nil, "add", area+"/impl.go")
		runGit(t, dir, authorEnv("Contributor", "contrib@x.com",
			fmt.Sprintf("2026-01-%02dT10:00:00Z", i+2)), "commit", "-q", "-m", "work on "+area)
		runGit(t, dir, nil, "checkout", "-q", "main")
		runGit(t, dir, authorEnv("Merger", "merge@x.com",
			fmt.Sprintf("2026-01-%02dT11:00:00Z", i+2)),
			"merge", "-q", "--no-ff", "-m",
			fmt.Sprintf("Merge pull request #%d from fork/%s", 100+i, branch), branch)
	}
	return dir
}

// TestPullDirsFromMerges verifies the local pull-request-to-places link: a
// merge names its pull request, and the directories it landed are recorded
// so a reviewer can be credited with the places rather than the prose.
func TestPullDirsFromMerges(t *testing.T) {
	t.Parallel()
	dir := newPullRepo(t)
	g := NewGitHistory(GitOptions{Paths: []string{dir}, SinceDays: 36500})
	if _, err := g.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	pulls := g.PullDirs()
	if len(pulls) != 2 {
		t.Fatalf("pull dirs = %v, want both merges recorded", pulls)
	}
	if !slices.Contains(pulls[100], "scheduler") {
		t.Errorf("pull 100 dirs = %v, want scheduler", pulls[100])
	}
	if !slices.Contains(pulls[101], "storage") {
		t.Errorf("pull 101 dirs = %v, want storage", pulls[101])
	}
	// A merge is not authorship: the merger must not be credited for it.
	recs, err := g.Fetch(context.Background())
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	for _, r := range recs {
		if r.Kind == KindPerson && r.Email == "merge@x.com" {
			t.Errorf("the merger was credited as an author: %+v", r)
		}
	}
}

// TestMergesDoNotConsumeTheCommitCap verifies the cap counts authorship. A
// repository that lands everything through merges once starved itself of the
// commits the cap exists to read.
func TestMergesDoNotConsumeTheCommitCap(t *testing.T) {
	t.Parallel()
	dir := newPullRepo(t)
	var log strings.Builder
	recs, err := NewGitHistory(GitOptions{
		Paths: []string{dir}, SinceDays: 36500, MaxCommits: 3, Log: &log,
	}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.Kind == KindPerson && r.Email == "contrib@x.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("the contributor is missing under a cap of 3; merges ate it. records=%+v", recs)
	}
}
