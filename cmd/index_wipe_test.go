package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIndexRefusesToReplaceWithNothing verifies a replacing run that read
// nothing leaves the existing index alone. Every connector reports an
// unreachable or unauthorized source as a skip rather than a failure, so an
// expired token yields zero records with no error, and replacing on that would
// erase a working graph while reporting success.
func TestIndexRefusesToReplaceWithNothing(t *testing.T) {
	dir := t.TempDir()
	csv := writeOrgCSV(t, dir)
	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", csv); err != nil {
		t.Fatalf("index: %v", err)
	}
	indexPath := filepath.Join(dir, "index.json")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	// A source that yields no records at all, which is what every repo or
	// channel failing looks like from here.
	empty := filepath.Join(dir, "empty.csv")
	if err := os.WriteFile(empty, []byte("name,email,title,team,topics\n"), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", empty); !errors.Is(err, ErrNoRecords) {
		t.Fatalf("index with nothing to read = %v, want ErrNoRecords", err)
	}

	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the index was rewritten by a run that read nothing")
	}
	out, _, err := runCmd(t, "ask", "--data-dir", dir, "who owns retries")
	if err != nil || !strings.Contains(string(out), "jane@x.com") {
		t.Fatalf("index no longer answers after an empty run: %v\n%s", err, out)
	}
}
