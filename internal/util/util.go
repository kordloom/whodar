// Package util holds small helpers shared across whodar's packages.
package util

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// NormalizeEmail returns email trimmed of surrounding whitespace and
// lowercased. It is the canonical form of a person's merge key, so stray casing
// or spacing never splits one human across records from different sources.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Truncate returns s cut to at most max bytes without splitting a UTF-8 rune.
// Source text such as a pull request description or an issue body is indexed
// for the words it carries, so a pasted log dump is cut rather than allowed to
// drown the text that explains something.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		if max <= 0 {
			return ""
		}
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// WriteFileAtomic writes data to path through a same-directory temporary file
// and a rename, so a crash never leaves a partial or truncated file behind.
// perm applies to the final file even when path already exists looser.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("atomic write: temp: %w", err)
	}
	name := tmp.Name()
	if err := fillTemp(tmp, data, perm); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("atomic write: rename: %w", err)
	}
	return syncDir(filepath.Dir(path))
}

// syncDir fsyncs a directory so a rename into it survives a crash. A directory
// that cannot be opened for sync, as on some platforms, is not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("atomic write: sync dir: %w", err)
	}
	return nil
}

// fillTemp writes data and perm to the open temporary file and closes it.
func fillTemp(f *os.File, data []byte, perm fs.FileMode) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomic write: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomic write: sync: %w", err)
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomic write: chmod: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("atomic write: close: %w", err)
	}
	return nil
}
