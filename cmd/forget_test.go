package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/resolve"
)

// TestForgetPurgesPerson indexes the Slack export fixture, purges one person,
// and then proves they are gone from every file whodar keeps and every answer
// it gives, while everyone else survives.
func TestForgetPurgesPerson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, stderr, err := runCmd(t, "index", "--data-dir", dir,
		"--source", "slack-export", "--file", exportZip,
		"--episodes", "--since-days", "36500"); err != nil {
		t.Fatalf("index: %v\n%s", err, stderr)
	}

	// Before: bob is findable.
	out, _, err := runCmd(t, "ask", "--data-dir", dir, "who knows about consumer lag dashboards")
	if err != nil {
		t.Fatalf("ask before: %v", err)
	}
	if !strings.Contains(string(out), "bob@corp.com") {
		t.Fatalf("bob not findable before the purge:\n%s", out)
	}

	if _, stderr, err := runCmd(t, "forget", "--data-dir", dir, "--yes", "bob@corp.com"); err != nil {
		t.Fatalf("forget: %v\n%s", err, stderr)
	}

	// Gone from every stored byte, name and email both.
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, trace := range []string{"bob@corp.com", "Bob Okafor"} {
			if strings.Contains(string(data), trace) {
				t.Errorf("%s still contains %q after forget", path, trace)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Gone from answers, while alice still stands.
	out, _, err = runCmd(t, "ask", "--data-dir", dir, "who knows about the kafka upgrade")
	if err != nil {
		t.Fatalf("ask after: %v", err)
	}
	var ans resolve.JSONAnswer
	if err := json.Unmarshal(out, &ans); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for _, p := range ans.People {
		if p.Email == "bob@corp.com" {
			t.Error("bob still answers after being forgotten")
		}
	}
	found := false
	for _, p := range ans.People {
		if p.Email == "alice@corp.com" {
			found = true
		}
	}
	if !found {
		t.Error("alice vanished; the purge took more than the named person")
	}
}

// TestForgetUnknownPerson verifies purging a stranger is an error, not a
// silent no-op.
func TestForgetUnknownPerson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv,
		[]byte("name,email,title,team,topics\nJo,jo@x.com,Eng,Core,kafka\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", csv); err != nil {
		t.Fatalf("index: %v", err)
	}
	if _, _, err := runCmd(t, "forget", "--data-dir", dir, "--yes", "nobody@x.com"); err == nil {
		t.Fatal("forgetting an unknown person: want error, got nil")
	}
}
