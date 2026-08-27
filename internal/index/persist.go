// Persisting the index: the snapshot on disk, optionally sealed, and the
// load path that rebuilds everything derived.

package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/identity"
	"github.com/kordloom/whodar/internal/invindex"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/util"
	"github.com/kordloom/whodar/internal/vault"
)

// snapshot is the serializable form of an index written to and read from disk.
type snapshot struct {
	// Graph is the entity graph.
	Graph *model.Graph `json:"graph"`
	// Postings is the per-person inverted index packed as a compact binary blob
	// (JSON stores a byte slice as base64), which is far smaller and faster to
	// read than a map of maps and is the bulk of an index once the source
	// records move to the sidecar.
	Postings []byte `json:"postings"`
	// Texts holds normalized per-person field text.
	Texts map[model.ID]*personText `json:"texts"`
	// ChannelPostings is the per-channel inverted index, packed the same way.
	ChannelPostings []byte `json:"channel_postings"`
	// ChannelTexts holds normalized per-channel field text.
	ChannelTexts map[model.ID]*channelText `json:"channel_texts"`
	// PersonVecs holds per-person embedding vectors, quantized to int8 (JSON
	// stores each as a small number array), a quarter the size of float32.
	PersonVecs map[model.ID][]int8 `json:"person_vecs,omitempty"`
	// ChannelVecs holds per-channel embedding vectors, quantized the same way.
	ChannelVecs map[model.ID][]int8 `json:"channel_vecs,omitempty"`
	// TopicVecs holds per-topic embedding vectors, quantized the same way.
	TopicVecs map[model.ID][]int8 `json:"topic_vecs,omitempty"`
	// Aliases maps each known alias identifier to its canonical form.
	Aliases map[model.ID]model.ID `json:"aliases,omitempty"`
	// Joins records the inferred identity merges with their strength and
	// evidence, so a re-index keeps them and a reader can audit why two
	// identities became one.
	Joins []Join `json:"joins,omitempty"`
	// SourceCounts is how many records each source contributed. The records
	// themselves live in a sidecar file so a query never loads them; only their
	// counts stay here for status and the shrink guard.
	SourceCounts map[string]int `json:"source_counts,omitempty"`
	// BuiltAt is when the index was last written, so a reader can tell how stale
	// it is without re-running an index.
	BuiltAt time.Time `json:"built_at,omitempty"`
}

// sourcesSnapshot is the sidecar that holds the raw records per source, read
// only when a merge needs to rebuild. Keeping it out of the main index is what
// lets a query load a fraction of the bytes, since the records are the bulk of
// an index and no query reads them.
type sourcesSnapshot struct {
	// Sources holds the records each source contributed, so a later merge can
	// replace one source without re-reading the others.
	Sources map[string][]connector.Record `json:"sources,omitempty"`
}

// Option configures Load and Save. With no option the index is read and written
// as plain JSON; WithCodec injects an at-rest codec so the file is encrypted.
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

// Save writes the index to path readable only by the owner (mode 0600), creating
// parent directories as needed. It is compact JSON, or its encrypted form when
// WithCodec is set, and each write goes through a temporary file and a rename so
// a crash cannot truncate an existing file. The raw source records, which are
// the bulk of an index and which no query reads, go to a sidecar file next to
// path so a query loads only the small main index.
func (ix *Index) Save(path string, opts ...Option) error {
	cfg := newIOConfig(opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("index: mkdir: %w", err)
	}
	snap := snapshot{
		Graph:           ix.Graph,
		Postings:        invindex.EncodePostings(ix.postings),
		Texts:           ix.texts,
		ChannelPostings: invindex.EncodePostings(ix.channelPostings),
		ChannelTexts:    ix.channelTexts,
		PersonVecs:      quantizeVecs(ix.personVecs),
		ChannelVecs:     quantizeVecs(ix.channelVecs),
		TopicVecs:       quantizeVecs(ix.topicVecs),
		Aliases:         ix.identityResolver().Pairs(),
		Joins:           ix.joins,
		SourceCounts:    ix.sourceCounts,
		BuiltAt:         ix.now(),
	}
	if err := writeEncoded(path, snap, cfg.codec); err != nil {
		return err
	}
	// Write the sources sidecar only when the sources are in hand. An index
	// loaded to answer a query carries none, and overwriting the sidecar then
	// would erase the records a later merge needs; leaving it keeps them.
	if ix.sources != nil {
		side := sourcesSnapshot{Sources: redactedSources(ix.sources)}
		if err := writeEncoded(sourcesPath(path), side, cfg.codec); err != nil {
			return err
		}
	}
	return nil
}

// writeEncoded marshals v to JSON, encodes it with the codec, and writes it
// atomically at mode 0600.
func writeEncoded(path string, v any, codec vault.Codec) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("index: encode %s: %w", filepath.Base(path), err)
	}
	enc, err := codec.Encode(raw)
	if err != nil {
		return fmt.Errorf("index: encrypt %s: %w", filepath.Base(path), err)
	}
	if err := util.WriteFileAtomic(path, enc, 0o600); err != nil {
		return fmt.Errorf("index: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// sourcesPath is the sidecar path for an index at path: the same name with a
// .sources segment inserted, so the two files travel together.
func sourcesPath(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".sources" + ext
}

// Load reads an index previously written by Save, decrypting it when WithCodec
// supplies the key. It returns vault.ErrEncrypted when the file is encrypted but
// no codec is given, so a caller can prompt for a passphrase.
func Load(path string, opts ...Option) (*Index, error) {
	cfg := newIOConfig(opts)
	stored, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("index: open: %w", err)
	}
	raw, err := cfg.codec.Decode(stored)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDamaged, filepath.Base(path), err)
	}
	postings, err := invindex.DecodePostings[model.ID](snap.Postings)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	channelPostings, err := invindex.DecodePostings[model.ID](snap.ChannelPostings)
	if err != nil {
		return nil, fmt.Errorf("index: channel %w", err)
	}
	ix := &Index{
		Graph:           snap.Graph,
		postings:        postings,
		texts:           snap.Texts,
		channelPostings: channelPostings,
		channelTexts:    snap.ChannelTexts,
		personVecs:      dequantizeVecs(snap.PersonVecs),
		channelVecs:     dequantizeVecs(snap.ChannelVecs),
		topicVecs:       dequantizeVecs(snap.TopicVecs),
		// sources stays nil: a loaded index answers queries, which never read
		// the records. A merge calls LoadSources to bring them in from the
		// sidecar before rebuilding.
		sourceCounts: snap.SourceCounts,
		builtAt:      snap.BuiltAt,
		resolver:     identity.NewResolver(),
		joins:        snap.Joins,
		halfLife:     DefaultHalfLife,
		now:          time.Now,
	}
	ix.resolver.Restore(snap.Aliases)
	if ix.Graph == nil {
		ix.Graph = model.NewGraph()
	}
	if ix.Graph.People == nil {
		ix.Graph.People = make(map[model.ID]*model.Person)
	}
	if ix.Graph.Teams == nil {
		ix.Graph.Teams = make(map[model.ID]*model.Team)
	}
	if ix.Graph.Orgs == nil {
		ix.Graph.Orgs = make(map[model.ID]*model.Org)
	}
	if ix.Graph.Topics == nil {
		ix.Graph.Topics = make(map[model.ID]*model.Topic)
	}
	if ix.Graph.Channels == nil {
		ix.Graph.Channels = make(map[model.ID]*model.Channel)
	}
	if ix.postings == nil {
		ix.postings = make(map[string]map[model.ID]float64)
	}
	if ix.texts == nil {
		ix.texts = make(map[model.ID]*personText)
	}
	if ix.channelPostings == nil {
		ix.channelPostings = make(map[string]map[model.ID]float64)
	}
	if ix.channelTexts == nil {
		ix.channelTexts = make(map[model.ID]*channelText)
	}
	if ix.personVecs == nil {
		ix.personVecs = make(map[model.ID][]float32)
	}
	if ix.topicVecs == nil {
		ix.topicVecs = make(map[model.ID][]float32)
	}
	if ix.channelVecs == nil {
		ix.channelVecs = make(map[model.ID][]float32)
	}
	ix.refreshStats()
	return ix, nil
}

// LoadSources reads the sources sidecar for the index at path into the index, so
// a merge can rebuild from every source and not just the one it is adding.
// Merging into a loaded index must call this first: without the records a
// rebuild would drop every source read before. A missing sidecar is an error
// rather than an empty set, since a silent shrink is exactly what this guards.
func (ix *Index) LoadSources(path string, opts ...Option) error {
	cfg := newIOConfig(opts)
	stored, err := os.ReadFile(sourcesPath(path))
	if err != nil {
		return fmt.Errorf("index: open sources: %w", err)
	}
	raw, err := cfg.codec.Decode(stored)
	if err != nil {
		return fmt.Errorf("index: sources: %w", err)
	}
	var side sourcesSnapshot
	if err := json.Unmarshal(raw, &side); err != nil {
		return fmt.Errorf("index: decode sources: %w", err)
	}
	ix.sources = side.Sources
	if ix.sources == nil {
		ix.sources = make(map[string][]connector.Record)
	}
	if ix.sourceCounts == nil {
		ix.sourceCounts = make(map[string]int)
	}
	for name, recs := range ix.sources {
		ix.sourceCounts[name] = len(recs)
	}
	return nil
}
