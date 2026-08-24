package connector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestWalkIsIndependentOfWorkerCount checks dividing the walk across workers
// changes only how long it takes. Splitting the work would be worthless if the
// answer depended on how many pieces it was split into, and a difference here
// would mean somebody's expertise appears or disappears with the core count of
// whatever machine indexed them.
func TestWalkIsIndependentOfWorkerCount(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "--quiet", "-b", "main")

	// Several authors touching overlapping areas, so the merge across workers
	// has something to actually merge.
	authors := []struct{ name, email string }{
		{"Ada", "ada@corp.com"}, {"Bo", "bo@corp.com"}, {"Cy", "cy@corp.com"},
	}
	areas := []string{"billing", "ledger", "kafka", "vault", "search"}
	for i := range 60 {
		a := authors[i%len(authors)]
		area := areas[i%len(areas)]
		sub := filepath.Join(dir, area)
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		file := filepath.Join(sub, fmt.Sprintf("f%d.go", i%4))
		if err := os.WriteFile(file, []byte(fmt.Sprintf("package x // %d\n", i)), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		gitRun(t, dir, "add", ".")
		cmd := exec.Command("git", "commit", "--quiet", "-m", "work on "+area)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME="+a.name, "GIT_AUTHOR_EMAIL="+a.email,
			"GIT_COMMITTER_NAME="+a.name, "GIT_COMMITTER_EMAIL="+a.email)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit %d: %v: %s", i, err, out)
		}
	}

	read := func(workers int) []Record {
		recs, err := NewGitHistory(GitOptions{
			Paths: []string{dir}, SinceDays: 3650, Workers: workers,
		}).Fetch(context.Background())
		if err != nil {
			t.Fatalf("fetch with %d workers: %v", workers, err)
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Email < recs[j].Email })
		for i := range recs {
			sort.Strings(recs[i].Topics)
			sort.Strings(recs[i].WeakTopics)
		}
		return recs
	}

	want := read(1)
	if len(want) != len(authors) {
		t.Fatalf("read %d authors, want %d", len(want), len(authors))
	}
	for _, workers := range []int{2, 3, 8, 16} {
		t.Run(fmt.Sprintf("%d workers", workers), func(t *testing.T) {
			if diff := cmp.Diff(want, read(workers), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("walking with %d workers differs from walking with one (-one +%d):\n%s",
					workers, workers, diff)
			}
		})
	}
}
