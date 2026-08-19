package cmd

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/keyring"
	"github.com/kordloom/whodar/internal/prompt"
	"github.com/kordloom/whodar/internal/vault"
)

// codec resolves the at-rest codec from the environment once and caches it on
// the options. A nil codec means the index is stored as plain JSON.
func (o *options) codec() (vault.Codec, error) {
	if o.codecResolved {
		return o.codecCache, nil
	}
	c, err := keyring.FromEnv()
	if err != nil {
		return nil, err
	}
	o.codecCache = c
	o.codecResolved = true
	return c, nil
}

// setCodec overrides the cached codec, used after an interactive passphrase so
// the matching save reuses it.
func (o *options) setCodec(c vault.Codec) {
	o.codecCache = c
	o.codecResolved = true
}

// loadIndex reads the index, decrypting when a key is configured. When the file
// is encrypted and no key is set, it prompts for a passphrase on a terminal, or
// returns a clear error pointing at the key variables when input is not a tty.
func (o *options) loadIndex(cmd *cobra.Command) (*index.Index, error) {
	c, err := o.codec()
	if err != nil {
		return nil, err
	}
	ix, err := loadWith(o.indexPath(), c)
	if !errors.Is(err, vault.ErrEncrypted) {
		return ix, err
	}
	return unlockAndLoad(cmd, o, "Index passphrase", func(pc vault.Codec) (*index.Index, error) {
		return loadWith(o.indexPath(), pc)
	})
}

// unlockAndLoad prompts for a passphrase and retries a few times on a wrong
// one, since a typo is the everyday cause of a failed decrypt and re-running
// the whole command to try again is a poor way to spend it. On success it
// records the working codec so a later save reuses it. It is shared by the
// index and episode loaders so both behave alike. When input is not a
// terminal it cannot prompt, so it points at the key variables instead.
func unlockAndLoad[T any](
	cmd *cobra.Command, o *options, label string, load func(vault.Codec) (T, error),
) (T, error) {
	var zero T
	ui := prompt.New(cmd.InOrStdin(), cmd.ErrOrStderr(), cmd.ErrOrStderr())
	if !ui.Interactive() {
		return zero, fmt.Errorf(
			"%w: set %s or %s (see `whodar vault keygen`)",
			vault.ErrEncrypted, keyring.EnvKey, keyring.EnvPassphrase)
	}
	const attempts = 3
	for i := range attempts {
		pass, perr := ui.Secret(label)
		if perr != nil {
			return zero, perr
		}
		pc := vault.NewPassphraseCipher([]byte(pass))
		v, err := load(pc)
		if err == nil {
			o.setCodec(pc)
			return v, nil
		}
		if !errors.Is(err, vault.ErrCorrupt) {
			return zero, err
		}
		if i < attempts-1 {
			fmt.Fprintln(cmd.ErrOrStderr(), "That passphrase did not work. Try again.")
		}
	}
	return zero, fmt.Errorf("%w: gave up after %d attempts", vault.ErrCorrupt, attempts)
}

// loadSources reads the sources sidecar into ix with the same key as the index,
// so a merge can rebuild from every source and not just the one being added.
func (o *options) loadSources(ix *index.Index) error {
	c, err := o.codec()
	if err != nil {
		return err
	}
	return ix.LoadSources(o.indexPath(), index.WithCodec(c))
}

// saveIndex writes the index, encrypting it when a key is configured. It reuses
// any passphrase entered during a preceding loadIndex.
func (o *options) saveIndex(ix *index.Index) error {
	c, err := o.codec()
	if err != nil {
		return err
	}
	if c == nil {
		return ix.Save(o.indexPath())
	}
	return ix.Save(o.indexPath(), index.WithCodec(c))
}

// loadEpisodes reads the episode store, decrypting with the same key as the
// index. A missing file is an empty store, since a run that has never indexed
// episodes has no history rather than a problem. When the file is encrypted
// and no key is set, it prompts on a terminal, so `archive status` reaches an
// encrypted store the same way `ask` reaches an encrypted index.
func (o *options) loadEpisodes(cmd *cobra.Command) (*episode.Store, error) {
	c, err := o.codec()
	if err != nil {
		return nil, err
	}
	store, err := loadEpisodesWith(o.episodePath(), c)
	if !errors.Is(err, vault.ErrEncrypted) {
		return store, err
	}
	return unlockAndLoad(cmd, o, "Conversations passphrase", func(pc vault.Codec) (*episode.Store, error) {
		return loadEpisodesWith(o.episodePath(), pc)
	})
}

// loadEpisodesWith loads the episode store at path with an optional codec.
func loadEpisodesWith(path string, c vault.Codec) (*episode.Store, error) {
	if c == nil {
		return episode.LoadOrNew(path)
	}
	return episode.LoadOrNew(path, episode.WithCodec(c))
}

// saveEpisodes writes the episode store, encrypting it when a key is
// configured.
func (o *options) saveEpisodes(store *episode.Store) error {
	c, err := o.codec()
	if err != nil {
		return err
	}
	if c == nil {
		return store.Save(o.episodePath())
	}
	return store.Save(o.episodePath(), episode.WithCodec(c))
}

// loadWith loads the index at path with an optional codec.
func loadWith(path string, c vault.Codec) (*index.Index, error) {
	if c == nil {
		return index.Load(path)
	}
	return index.Load(path, index.WithCodec(c))
}

// noIndexError wraps a load error as ErrNoIndex only when the index file is
// missing. Other errors, such as an encrypted index with no key, pass through so
// their own guidance survives.
func noIndexError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: run `whodar index` first: %w", ErrNoIndex, err)
	}
	return err
}
