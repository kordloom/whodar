package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/kordloom/whodar/internal/util"
)

// refreshSkipFlags are index flags that must not be replayed by refresh: they
// control the run itself, not what to fetch. The source is passed explicitly.
//
//nolint:gochecknoglobals // Read-only lookup table.
var refreshSkipFlags = map[string]bool{
	"source": true, "merge": true, "full": true, "allow-shrink": true,
	"data-dir": true, "pretty": true, "changes-file": true,
}

// captureIndexFlags returns the explicitly-set index flags worth replaying on a
// refresh, expanding a repeatable flag into one entry per value.
func captureIndexFlags(cmd *cobra.Command) []string {
	var args []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if refreshSkipFlags[f.Name] {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				args = append(args, "--"+f.Name, v)
			}
			return
		}
		args = append(args, "--"+f.Name+"="+f.Value.String())
	})
	return args
}

// saveInvocation records the flags a source was indexed with, so refresh can
// replay them. It is best-effort: a failure to save never fails the index. A
// source reading stdin cannot be replayed, so it is not recorded.
func saveInvocation(opts *options, cmd *cobra.Command, source string) error {
	if readsStdin(cmd, source) {
		return nil
	}
	inv, err := loadInvocations(opts.refreshPath())
	if err != nil {
		return err
	}
	inv[source] = captureIndexFlags(cmd)
	return saveInvocations(opts.refreshPath(), inv)
}

// readsStdin reports whether this index run reads its input from stdin, which a
// scheduled refresh could not supply.
func readsStdin(cmd *cobra.Command, source string) bool {
	if source != "json" {
		return false
	}
	f := cmd.Flags().Lookup("file")
	return f == nil || f.Value.String() == "" || f.Value.String() == "-"
}

// loadInvocations reads the refresh config. A missing file yields an empty map.
func loadInvocations(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("refresh: read %s: %w", path, err)
	}
	inv := map[string][]string{}
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, fmt.Errorf("refresh: parse %s: %w", path, err)
	}
	return inv, nil
}

// saveInvocations writes the refresh config atomically.
func saveInvocations(path string, inv map[string][]string) error {
	raw, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("refresh: encode: %w", err)
	}
	if err := util.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("refresh: write %s: %w", path, err)
	}
	return nil
}

// newRefreshCmd builds the refresh command, which re-indexes every source with
// the flags it was last indexed with, merging each into the existing index.
func newRefreshCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Re-index every source with its saved flags, merging into the index",
		Long: `Re-run every source that has been indexed at least once, reusing the flags it was
indexed with and merging the result. It is what a scheduled refresh runs, so the
graph stays current without retyping each source's scope.

  whodar refresh`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inv, err := loadInvocations(opts.refreshPath())
			if err != nil {
				return err
			}
			if len(inv) == 0 {
				return fmt.Errorf("%w: nothing to refresh; run `whodar index --source ...` at least once first", ErrBadArgs)
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("refresh: locate binary: %w", err)
			}
			sources := make([]string, 0, len(inv))
			for s := range inv {
				sources = append(sources, s)
			}
			sort.Strings(sources)

			failed := 0
			for _, src := range sources {
				fmt.Fprintf(cmd.ErrOrStderr(), "refreshing %s...\n", src)
				args := append([]string{"index", "--source", src, "--merge", "--data-dir", opts.dataDir}, inv[src]...)
				sub := exec.CommandContext(cmd.Context(), exe, args...)
				sub.Stdout = cmd.OutOrStdout()
				sub.Stderr = cmd.ErrOrStderr()
				if err := sub.Run(); err != nil {
					failed++
					fmt.Fprintf(cmd.ErrOrStderr(), "refresh %s failed: %v\n", src, err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("refresh: %d of %d source(s) failed", failed, len(sources))
			}
			return nil
		},
	}
	return cmd
}
