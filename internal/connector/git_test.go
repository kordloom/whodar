package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// newFixtureRepo creates a git repository with a small history: Alice touches
// terraform twice, Bob touches python once, and a bot commit that must be
// skipped.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	commit := func(rel, content, name, email string, when time.Time) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatalf("add: %v", err)
		}
		sig := &object.Signature{Name: name, Email: email, When: when}
		if _, err := wt.Commit("touch "+rel, &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	now := time.Now()
	commit("infra/main.tf", "a", "Alice Smith", "alice@corp.com", now.AddDate(0, 0, -30))
	commit("infra/vpc.tf", "b", "Alice Smith", "alice@corp.com", now.AddDate(0, 0, -10))
	commit("app/serve.py", "c", "Bob Jones", "bob@corp.com", now.AddDate(0, 0, -5))
	commit("go.sum", "d", "dependabot[bot]", "12345+dependabot[bot]@users.noreply.github.com",
		now.AddDate(0, 0, -1))
	return dir
}

func TestGitHistoryFetch(t *testing.T) {
	t.Parallel()
	dir := newFixtureRepo(t)
	recs, err := NewGitHistory(GitOptions{Paths: []string{dir}}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	byEmail := make(map[string]Record)
	for _, r := range recs {
		byEmail[r.Email] = r
	}
	if len(byEmail) != 2 {
		t.Fatalf("authors = %d (%v), want 2 with the bot skipped", len(byEmail), recs)
	}

	// Path tokens establish a subject and land in Topics; commit subject words
	// are prose and arrive as weak topics. Both carry affinity.
	all := func(r Record) []string {
		return append(append([]string(nil), r.Topics...), r.WeakTopics...)
	}
	alice := byEmail["alice@corp.com"]
	if !slices.Contains(all(alice), "terraform") || !slices.Contains(all(alice), "infra") {
		t.Errorf("alice topics = %v, weak = %v, want terraform and infra",
			alice.Topics, alice.WeakTopics)
	}
	if alice.Name != "Alice Smith" {
		t.Errorf("alice name = %q", alice.Name)
	}
	wantLatest := time.Now().AddDate(0, 0, -10)
	if alice.Time.Before(wantLatest.Add(-time.Hour)) || alice.Time.After(wantLatest.Add(time.Hour)) {
		t.Errorf("alice time = %v, want near her latest commit %v", alice.Time, wantLatest)
	}

	bob := byEmail["bob@corp.com"]
	if !slices.Contains(all(bob), "python") || !slices.Contains(all(bob), "serve") {
		t.Errorf("bob topics = %v, weak = %v, want python and serve", bob.Topics, bob.WeakTopics)
	}
}

func TestGitHistoryMaxCommits(t *testing.T) {
	t.Parallel()
	dir := newFixtureRepo(t)
	recs, err := NewGitHistory(GitOptions{Paths: []string{dir}, MaxCommits: 1}).
		Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(recs) != 1 || recs[0].Email != "bob@corp.com" {
		t.Errorf("records = %+v, want only the newest human commit (bob)", recs)
	}
}

func TestGitHistoryErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewGitHistory(GitOptions{}).Fetch(context.Background()); !errors.Is(err, ErrNoRepoPaths) {
		t.Errorf("no paths error = %v, want ErrNoRepoPaths", err)
	}
	dir := t.TempDir()
	var log strings.Builder
	recs, err := NewGitHistory(GitOptions{Paths: []string{dir}, Log: &log}).Fetch(context.Background())
	if err != nil {
		t.Errorf("Fetch = %v, want a non-repository directory skipped without error", err)
	}
	if len(recs) != 0 {
		t.Errorf("records = %+v, want none from a non-repository directory", recs)
	}
	if !strings.Contains(log.String(), "skipping") {
		t.Errorf("log = %q, want a skip warning", log.String())
	}
}

// TestGitHistoryMailmap verifies a .mailmap merges one person's two commit
// emails into a single record under the canonical identity.
func TestGitHistoryMailmap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	commit := func(rel, name, email string, when time.Time) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatalf("add: %v", err)
		}
		sig := &object.Signature{Name: name, Email: email, When: when}
		if _, err := wt.Commit("c", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	now := time.Now()
	commit("infra/vpc.tf", "Alice P", "alice@personal.com", now.AddDate(0, 0, -20))
	commit("infra/main.tf", "Alice Smith", "alice@corp.com", now.AddDate(0, 0, -5))
	mm := "Alice Smith <alice@corp.com> <alice@personal.com>\n"
	if err := os.WriteFile(filepath.Join(dir, ".mailmap"), []byte(mm), 0o600); err != nil {
		t.Fatalf("write mailmap: %v", err)
	}

	recs, err := NewGitHistory(GitOptions{Paths: []string{dir}}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d (%+v), want 1 merged author", len(recs), recs)
	}
	if recs[0].Email != "alice@corp.com" || recs[0].Name != "Alice Smith" {
		t.Errorf("record = %+v, want canonical alice@corp.com / Alice Smith", recs[0])
	}
	merged := append(append([]string(nil), recs[0].Topics...), recs[0].WeakTopics...)
	if !slices.Contains(merged, "terraform") {
		t.Errorf("topics = %v, weak = %v, want terraform from both commits",
			recs[0].Topics, recs[0].WeakTopics)
	}
}

// TestGitHistorySkipsBadRepo verifies a bad path is logged and skipped while
// good repositories still contribute records.
func TestGitHistorySkipsBadRepo(t *testing.T) {
	t.Parallel()
	good := newFixtureRepo(t)
	bad := t.TempDir()
	var log strings.Builder
	recs, err := NewGitHistory(GitOptions{Paths: []string{bad, good}, Log: &log}).
		Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch = %v, want the bad path skipped without error", err)
	}
	if len(recs) == 0 {
		t.Error("want records from the good repository")
	}
	if !strings.Contains(log.String(), "skipping") {
		t.Errorf("log = %q, want a skip warning for the bad path", log.String())
	}
}

// TestGitJoinsGitHubByNoreplyLogin verifies a commit made under a GitHub
// noreply email keys the author by their GitHub login, so their commits join
// their pull requests and reviews instead of appearing as a second person.
func TestGitJoinsGitHubByNoreplyLogin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "billing.go"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := wt.Add("billing.go"); err != nil {
		t.Fatalf("add: %v", err)
	}
	sig := &object.Signature{
		Name: "Octo Dev", Email: "99+octodev@users.noreply.github.com", When: time.Now(),
	}
	if _, err := wt.Commit("billing", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	recs, err := NewGitHistory(GitOptions{Paths: []string{dir}}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	var octo *Record
	for i := range recs {
		if recs[i].Email == "99+octodev@users.noreply.github.com" {
			octo = &recs[i]
		}
	}
	if octo == nil {
		t.Fatal("no record for the noreply author")
	}
	if octo.PersonID != "github:octodev" {
		t.Errorf("git author keyed as %q, want github:octodev so it joins GitHub", octo.PersonID)
	}
}
