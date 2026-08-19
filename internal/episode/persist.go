package episode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/kordloom/whodar/internal/invindex"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/util"
	"github.com/kordloom/whodar/internal/vault"
)

// snapshot is the serializable form of a store. The participant index is
// derived on load rather than stored, so it can never disagree with the
// episodes themselves.
type snapshot struct {
	// Episodes are the stored episodes.
	Episodes []*Episode `json:"episodes"`
	// Postings is the term-to-per-episode inverted index, packed as a compact
	// binary blob (JSON stores a byte slice as base64) rather than a JSON map of
	// maps, which is far smaller and faster to read back.
	Postings []byte `json:"postings"`
	// Vecs holds per-episode embedding vectors.
	Vecs map[string][]float32 `json:"vecs,omitempty"`
}

// Option configures Load and Save. With no option the store is read and
// written as plain JSON; WithCodec injects an at-rest codec so the file is
// encrypted with the same key as the index.
type Option func(*ioConfig)

// ioConfig holds the resolved options for one Load or Save.
type ioConfig struct {
	// codec transforms the bytes at rest; Plain by default.
	codec vault.Codec
}

// newIOConfig applies opts over the plain-JSON default.
func newIOConfig(opts []Option) ioConfig {
	cfg := ioConfig{codec: vault.Plain{}}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithCodec sets the at-rest codec for a Load or Save. A nil codec is ignored,
// leaving the plain-JSON default.
func WithCodec(c vault.Codec) Option {
	return func(cfg *ioConfig) {
		if c != nil {
			cfg.codec = c
		}
	}
}

// Save writes the store to path readable only by the owner (mode 0600),
// creating parent directories as needed, through a temporary file and a rename
// so a crash cannot truncate an existing file.
func (s *Store) Save(path string, opts ...Option) error {
	cfg := newIOConfig(opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("episode: mkdir: %w", err)
	}
	snap := snapshot{Episodes: s.All(), Postings: invindex.EncodePostings(s.postings), Vecs: s.vecs}
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("episode: encode: %w", err)
	}
	enc, err := cfg.codec.Encode(raw)
	if err != nil {
		return fmt.Errorf("episode: encrypt: %w", err)
	}
	if err := util.WriteFileAtomic(path, enc, 0o600); err != nil {
		return fmt.Errorf("episode: write: %w", err)
	}
	return nil
}

// Load reads a store previously written by Save, decrypting it when WithCodec
// supplies the key. It returns vault.ErrEncrypted when the file is encrypted
// but no codec is given, so a caller can prompt for a passphrase.
func Load(path string, opts ...Option) (*Store, error) {
	cfg := newIOConfig(opts)
	stored, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("episode: open: %w", err)
	}
	raw, err := cfg.codec.Decode(stored)
	if err != nil {
		return nil, fmt.Errorf("episode: %w", err)
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("episode: decode: %w", err)
	}
	s := New()
	postings, err := invindex.DecodePostings[string](snap.Postings)
	if err != nil {
		return nil, fmt.Errorf("episode: %w", err)
	}
	if len(postings) > 0 {
		s.postings = postings
	}
	if snap.Vecs != nil {
		s.vecs = snap.Vecs
	}
	for _, ep := range snap.Episodes {
		if ep == nil || ep.ID == "" {
			continue
		}
		s.episodes[ep.ID] = ep
		for _, p := range ep.Participants {
			s.byParticipant[p] = append(s.byParticipant[p], ep.ID)
		}
	}
	return s, nil
}

// LoadOrNew reads a store from path, returning an empty store when the file
// does not exist. Every other failure, including a missing decryption key, is
// returned so a real problem is never mistaken for an empty history.
func LoadOrNew(path string, opts ...Option) (*Store, error) {
	s, err := Load(path, opts...)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return New(), nil
		}
		return nil, err
	}
	return s, nil
}

// PurgeBefore drops episodes that last saw activity before cutoff, returning
// how many were removed. It is how a retention window is enforced.
func (s *Store) PurgeBefore(cutoff time.Time) int {
	var stale []*Episode
	for _, ep := range s.episodes {
		if !ep.Occurred.IsZero() && ep.Occurred.Before(cutoff) {
			stale = append(stale, ep)
		}
	}
	for _, ep := range stale {
		s.forget(ep)
	}
	return len(stale)
}

// PurgeArchive drops retained conversation content from every episode, leaving
// the pointers intact. It is what turning the archive off does to history
// already on disk.
func (s *Store) PurgeArchive() int {
	n := 0
	for _, ep := range s.episodes {
		if ep.Archived() {
			ep.Archive = nil
			n++
		}
	}
	return n
}

// Relink replaces an episode's participants and repoints the lookup that finds
// it by person. It is how an identity join reaches conversations already
// stored.
func (s *Store) Relink(id string, participants []model.ID) {
	ep, ok := s.episodes[id]
	if !ok {
		return
	}
	for _, p := range ep.Participants {
		ids := s.byParticipant[p]
		kept := make([]string, 0, len(ids))
		for _, existing := range ids {
			if existing != id {
				kept = append(kept, existing)
			}
		}
		if len(kept) == 0 {
			delete(s.byParticipant, p)
			continue
		}
		s.byParticipant[p] = kept
	}
	ep.Participants = participants
	for _, p := range participants {
		s.byParticipant[p] = append(s.byParticipant[p], id)
	}
}

// HasPerson reports whether any episode includes a person.
func (s *Store) HasPerson(id model.ID) bool { return len(s.byParticipant[id]) > 0 }

// Participants returns the people known to have taken part in any episode.
func (s *Store) Participants() []model.ID {
	out := make([]model.ID, 0, len(s.byParticipant))
	for p := range s.byParticipant {
		out = append(out, p)
	}
	return out
}
