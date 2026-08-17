// Package feedback stores user votes on answers so confirmed answers rise and
// corrected ones sink in future rankings. Votes live in their own file, apart
// from the index, so they survive re-indexing.
package feedback

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kordloom/whodar/internal/util"
)

// Votes a user can cast on one result.
const (
	// Helpful marks a result the user confirmed.
	Helpful = 1
	// NotHelpful marks a result the user corrected.
	NotHelpful = -1
)

// Entry is one vote on one result for one question.
type Entry struct {
	// Query is the question as asked.
	Query string `json:"query"`
	// Person is the voted person's identifier, when the vote is on a person.
	Person string `json:"person,omitempty"`
	// Channel is the voted channel's identifier, when the vote is on a channel.
	Channel string `json:"channel,omitempty"`
	// Vote is Helpful or NotHelpful.
	Vote int `json:"vote"`
	// Comment is an optional note explaining the vote.
	Comment string `json:"comment,omitempty"`
	// Time is when the vote was cast.
	Time time.Time `json:"time"`
}

// Valid reports whether the entry names a query, exactly one target, and a
// known vote.
func (e Entry) Valid() bool {
	if e.Query == "" || (e.Vote != Helpful && e.Vote != NotHelpful) {
		return false
	}
	return (e.Person != "") != (e.Channel != "")
}

// Store holds votes and persists them as JSON. It is safe for concurrent use.
type Store struct {
	// mu guards entries.
	mu sync.Mutex
	// entries are the recorded votes, oldest first.
	entries []Entry
	// path is the file the store persists to.
	path string
}

// Load reads a store from path. A missing file yields an empty store.
func Load(path string) (*Store, error) {
	entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, entries: entries}, nil
}

// Add records a vote and persists it, merging with any votes another process
// wrote since this store last read the file.
func (s *Store) Add(e Entry) error {
	if !e.Valid() {
		return fmt.Errorf("feedback: %w", ErrBadEntry)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(cur []Entry) []Entry {
		return append(cur, e)
	})
}

// All returns a copy of the recorded votes.
func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.entries...)
}

// Filter selects the votes to list or clear. Zero fields match everything;
// set fields must all match.
type Filter struct {
	// Query matches entries for this exact question, case-insensitively.
	Query string
	// Person matches votes on this person identifier.
	Person string
	// Channel matches votes on this channel name.
	Channel string
}

// matches reports whether e satisfies every set field of f.
func (f Filter) matches(e Entry) bool {
	if f.Query != "" && !strings.EqualFold(f.Query, e.Query) {
		return false
	}
	if f.Person != "" && !strings.EqualFold(f.Person, e.Person) {
		return false
	}
	if f.Channel != "" && !strings.EqualFold(f.Channel, e.Channel) {
		return false
	}
	return true
}

// List returns the votes matching f, oldest first.
func (s *Store) List(f Filter) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.entries {
		if f.matches(e) {
			out = append(out, e)
		}
	}
	return out
}

// Clear removes the votes matching f, persists the store, and returns how
// many were removed. It filters the file's current contents so a concurrent
// process's writes are not clobbered.
func (s *Store) Clear(f Filter) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	err := s.mutate(func(cur []Entry) []Entry {
		kept := make([]Entry, 0, len(cur))
		for _, e := range cur {
			if f.matches(e) {
				removed++
				continue
			}
			kept = append(kept, e)
		}
		return kept
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// mutate applies fn to the file's current contents under a cross-process lock,
// persists the result atomically, and only then swaps the in-memory entries, so
// a failed write never leaves memory ahead of disk and a concurrent process's
// votes are not lost. Callers hold s.mu.
func (s *Store) mutate(fn func([]Entry) []Entry) error {
	unlock, err := lockFile(s.path + lockSuffix)
	if err != nil {
		return err
	}
	defer unlock()

	cur, err := readEntries(s.path)
	if err != nil {
		return err
	}
	next := fn(cur)
	if err := saveEntries(s.path, next); err != nil {
		return err
	}
	s.entries = next
	return nil
}

// readEntries reads the votes at path. A missing file yields no entries.
func readEntries(path string) ([]Entry, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("feedback: read %s: %w", path, err)
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("feedback: parse %s: %w", path, err)
	}
	return entries, nil
}

// saveEntries writes entries to path atomically.
func saveEntries(path string, entries []Entry) error {
	raw, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("feedback: encode: %w", err)
	}
	if err := util.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("feedback: write %s: %w", path, err)
	}
	return nil
}
