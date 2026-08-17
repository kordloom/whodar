package util

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

// TestWriteFileAtomic covers fresh writes, overwrites that tighten
// permissions, and a missing parent directory.
func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Data        string
		Perm        fs.FileMode
		PreExisting string // existing content at path; empty means absent
		MissingDir  bool   // target a path whose parent does not exist
		WantErr     bool
	}{{ // Test 0: Fresh file gets the content and permissions.
		Data: `{"a":1}`, Perm: 0o600,
	}, { // Test 1: Overwrite replaces content and tightens permissions.
		Data: "new", Perm: 0o600, PreExisting: "old",
	}, { // Test 2: A missing parent directory is an error, nothing written.
		Data: "x", Perm: 0o600, MissingDir: true, WantErr: true,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "out.json")
			if test.MissingDir {
				path = filepath.Join(dir, "absent", "out.json")
			}
			if test.PreExisting != "" {
				if err := os.WriteFile(path, []byte(test.PreExisting), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := WriteFileAtomic(path, []byte(test.Data), test.Perm)
			if (err != nil) != test.WantErr {
				t.Fatalf("err = %v, want error %t", err, test.WantErr)
			}
			if test.WantErr {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Errorf("stat after failed write = %v, want not exist", statErr)
				}
				return
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(test.Data, string(got)); diff != "" {
				t.Errorf("content mismatch (-want +got):\n%s", diff)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != test.Perm {
				t.Errorf("perm = %o, want %o", info.Mode().Perm(), test.Perm)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Errorf("directory holds %d entries, want only the target: %v", len(entries), entries)
			}
		})
	}
}

// TestTruncate verifies the cut respects the byte cap and never splits a rune,
// since indexed source text arrives in every language.
func TestTruncate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		In   string
		Max  int
		Want string
	}{{ // Test 0: Shorter than the cap.
		In: "billing", Max: 20, Want: "billing",
	}, { // Test 1: Exactly the cap.
		In: "billing", Max: 7, Want: "billing",
	}, { // Test 2: Cut on an ASCII boundary.
		In: "billing retries", Max: 7, Want: "billing",
	}, { // Test 3: A cut that would split a multi-byte rune drops it whole.
		In: "café", Max: 4, Want: "caf",
	}, { // Test 4: Emoji are not split.
		In: "ok 🎉", Max: 5, Want: "ok ",
	}, { // Test 5: A zero cap yields nothing.
		In: "billing", Max: 0, Want: "",
	}, { // Test 6: A negative cap yields nothing.
		In: "billing", Max: -1, Want: "",
	}, { // Test 7: Empty input.
		In: "", Max: 10, Want: "",
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			got := Truncate(test.In, test.Max)
			if got != test.Want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", test.In, test.Max, got, test.Want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Truncate(%q, %d) = %q, not valid UTF-8", test.In, test.Max, got)
			}
		})
	}
}
