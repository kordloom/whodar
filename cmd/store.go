package cmd

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

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
	ui := prompt.New(cmd.InOrStdin(), cmd.ErrOrStderr(), cmd.ErrOrStderr())
	if !ui.Interactive() {
		return nil, fmt.Errorf(
			"%w: set %s or %s (see `whodar vault keygen`)", err, keyring.EnvKey, keyring.EnvPassphrase)
	}
	pass, perr := ui.Secret("Index passphrase")
	if perr != nil {
		return nil, perr
	}
	pc := vault.NewPassphraseCipher([]byte(pass))
	o.setCodec(pc)
	return loadWith(o.indexPath(), pc)
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
