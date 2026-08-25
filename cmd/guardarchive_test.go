package cmd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/policy"
)

// TestGuardArchiveRefusesEachWay covers the gate on keeping the words of a
// conversation. It is the only thing standing between a run and full
// conversation text on disk, and it had no test at all: a gate nobody checks is
// a gate that quietly stops holding.
func TestGuardArchiveRefusesEachWay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Name string
		Opts *options
		Want error
	}{{ // Test 0: The organization's policy forbids retaining content.
		Name: "policy forbids it",
		Opts: &options{dataDir: t.TempDir(), pol: policy.New(policy.Open, false).WithoutArchive()},
		Want: ErrBadArgs,
	}, { // Test 1: Allowed by policy, but this install is not licensed for it.
		Name: "not licensed",
		Opts: &options{dataDir: t.TempDir(), pol: policy.New(policy.Open, false)},
		Want: ErrLicense,
	}}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{}
			cmd.SetErr(&bytes.Buffer{})
			err := guardArchive(cmd, test.Opts)
			if !errors.Is(err, test.Want) {
				t.Errorf("guardArchive = %v, want %v", err, test.Want)
			}
		})
	}
}

// TestGuardArchiveSaysWhyItRefused checks the refusal tells somebody what to do
// about it. A gate that only says no sends them to read the source.
func TestGuardArchiveSaysWhyItRefused(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&errOut)
	err := guardArchive(cmd, &options{dataDir: t.TempDir(), pol: policy.New(policy.Open, false)})
	if err == nil {
		t.Fatal("an unlicensed install was allowed to retain conversation content")
	}
	for _, want := range []string{"Memory license", "hello@whodar.dev"} {
		if !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("refusal = %q, want it to mention %q", err, want)
		}
	}
}
