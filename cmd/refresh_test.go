package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCaptureIndexFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "index"}
	f := cmd.Flags()
	var org, source string
	var repos []string
	var merge bool
	f.StringVar(&org, "github-org", "", "")
	f.StringArrayVar(&repos, "repo", nil, "")
	f.StringVar(&source, "source", "", "")
	f.BoolVar(&merge, "merge", false, "")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.Set("github-org", "acme"))
	must(f.Set("repo", "a/b"))
	must(f.Set("repo", "c/d"))
	must(f.Set("source", "github"))
	must(f.Set("merge", "true"))

	got := strings.Join(captureIndexFlags(cmd), " ")
	if !strings.Contains(got, "--github-org=acme") {
		t.Errorf("missing github-org: %q", got)
	}
	if !strings.Contains(got, "--repo a/b") || !strings.Contains(got, "--repo c/d") {
		t.Errorf("missing repeated repo flags: %q", got)
	}
	if strings.Contains(got, "merge") || strings.Contains(got, "source") {
		t.Errorf("replayed a skipped flag: %q", got)
	}
}

func TestInvocationsRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "refresh.json")
	in := map[string][]string{"github": {"--github-org", "acme"}, "slack": {"--include-private=true"}}
	if err := saveInvocations(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := loadInvocations(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out) != 2 || strings.Join(out["github"], " ") != "--github-org acme" {
		t.Errorf("round trip mismatch: %+v", out)
	}
	empty, err := loadInvocations(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || len(empty) != 0 {
		t.Errorf("missing file = %+v, %v; want empty, nil", empty, err)
	}
}
