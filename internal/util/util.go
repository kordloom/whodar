// Package util holds small helpers shared across whodar's packages.
package util

import (
	"errors"
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
	e := strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndexByte(e, '@')
	if at <= 0 {
		return e
	}
	local, domain := e[:at], e[at+1:]
	// Drop a plus-tag: alice+ci@x.com and alice@x.com are the same mailbox.
	// Dots are left alone because many corporate domains make first.last
	// significant, so folding them would merge distinct people.
	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	return local + "@" + domain
}

// roleLocals are email local-parts that name a function or shared team mailbox
// rather than one person, so an address at one of them must not be used to merge
// two distinct people.
//
//nolint:gochecknoglobals // Read-only lookup table.
var roleLocals = map[string]bool{
	"admin": true, "administrator": true, "support": true, "help": true,
	"helpdesk": true, "info": true, "sales": true, "contact": true,
	"noreply": true, "no-reply": true, "donotreply": true, "do-not-reply": true,
	"team": true, "teams": true, "dev": true, "devops": true, "ops": true,
	"oncall": true, "on-call": true, "alerts": true, "alert": true,
	"notifications": true, "notification": true, "hello": true,
	"billing": true, "accounts": true, "accounting": true, "hr": true,
	"security": true, "abuse": true, "postmaster": true, "webmaster": true,
	"marketing": true, "press": true, "jobs": true, "careers": true,
	"recruiting": true, "root": true, "service": true, "services": true,
	"system": true, "sysadmin": true, "bot": true,
}

// IsRoleEmail reports whether email addresses a function or shared team mailbox
// rather than a single person, so a caller can avoid merging two people who
// happen to share it. It checks the local-part after normalization.
func IsRoleEmail(email string) bool {
	e := NormalizeEmail(email)
	at := strings.IndexByte(e, '@')
	if at <= 0 {
		return false
	}
	return roleLocals[e[:at]]
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
		// The raw error names the randomly-suffixed temp file, which never
		// existed and means nothing to a user. Report the directory that could
		// not be written and the underlying cause instead.
		cause := err
		var pe *fs.PathError
		if errors.As(err, &pe) {
			cause = pe.Err
		}
		return fmt.Errorf("cannot write to directory %s: %v", filepath.Dir(path), cause)
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

// GitHubNoreplyLogin returns the GitHub login encoded in a GitHub noreply commit
// email, and whether email was one. GitHub issues commit emails of the form
// "ID+login@users.noreply.github.com", and older ones as
// "login@users.noreply.github.com", so a commit made under a private email can
// still be joined to that person's GitHub activity without exposing a real
// address.
func GitHubNoreplyLogin(email string) (string, bool) {
	const suffix = "@users.noreply.github.com"
	e := strings.ToLower(strings.TrimSpace(email))
	if !strings.HasSuffix(e, suffix) {
		return "", false
	}
	local := strings.TrimSuffix(e, suffix)
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[i+1:]
	}
	if local == "" {
		return "", false
	}
	return local, true
}

// Distinct returns the items whose key has not been seen before, keeping the
// order they arrived in and dropping any whose key is the zero value. Callers
// were hand-rolling this loop everywhere a source returns the same person or
// the same subject more than once, which is most of them: a comment thread
// names its author on every comment, and a path yields the same subject at
// every level.
//
// It is deliberately not used for the visited sets of a graph walk. Those look
// similar and mean something else, and reading one as the other is how a walk
// stops early.
func Distinct[T any, K comparable](items []T, key func(T) K) []T {
	var zero K
	seen := make(map[K]bool, len(items))
	out := make([]T, 0, len(items))
	for _, item := range items {
		k := key(item)
		if k == zero || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, item)
	}
	return out
}
