package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/keyring"
	"github.com/kordloom/whodar/internal/vault"
)

// newVaultCmd builds the vault command group for index encryption at rest.
func newVaultCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Encrypt the on-disk index and conversations at rest",
		Long: `Encrypt what is on disk so a stolen laptop or a stray backup cannot read your
people graph or the conversations whodar kept. Both files are covered: the
index, and the conversation store beside it that holds retained message text.
Encryption turns on whenever a key is configured:

  WHODAR_INDEX_KEY         a base64 32-byte key, best for automation
  WHODAR_INDEX_PASSPHRASE  a passphrase, prompted if unset on a terminal

Generate a key with "whodar vault keygen". With a key set, every write is
encrypted and every read decrypts. Reading an encrypted file without the key
fails rather than exposing anything.`,
	}
	cmd.AddCommand(
		newVaultStatusCmd(opts), newVaultKeygenCmd(), newVaultEncryptCmd(opts), newVaultDecryptCmd(opts))
	return cmd
}

// newVaultStatusCmd reports whether a key is configured and whether each store
// on disk is encrypted, without decrypting anything.
func newVaultStatusCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the key and encryption state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "index:         %s\n", opts.indexPath())
			fmt.Fprintf(out, "conversations: %s\n", opts.episodePath())
			if src := keyring.Source(); src != "" {
				fmt.Fprintf(out, "key:           configured via %s\n", src)
			} else {
				fmt.Fprintln(out, "key:           none (both files are stored as plain JSON)")
			}
			// Both files are reported, because the conversation store holds
			// retained message text and is the more sensitive of the two. A
			// status that spoke only for the index would call an install
			// encrypted while the conversations sat in plain JSON.
			state, err := fileState(opts.indexPath(), "no index yet")
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "index file:    %s\n", state)
			state, err = fileState(opts.episodePath(), "none kept yet")
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "conversations: %s\n", state)
			return nil
		},
	}
}

// newVaultKeygenCmd prints a fresh key as an export line for the user to save.
func newVaultKeygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Print a new encryption key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := keyring.GenerateKey()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "export %s=%s\n", keyring.EnvKey, key)
			fmt.Fprintln(cmd.ErrOrStderr(),
				"Add this to your shell profile. Anyone with this key can read your index, "+
					"and losing it makes an encrypted index unrecoverable, so store it safely.")
			return nil
		},
	}
}

// newVaultEncryptCmd rewrites the existing plain stores in their encrypted
// form. It covers the conversation store as well as the index, since that is
// where retained message text lives.
func newVaultEncryptCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "encrypt",
		Short: "Encrypt an existing plain index and conversations in place",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := opts.codec()
			if err != nil {
				return err
			}
			if c == nil {
				return fmt.Errorf(
					"%w: no key configured; run `whodar vault keygen` and set %s", ErrBadArgs, keyring.EnvKey)
			}
			enc, err := isEncryptedOnDisk(opts.indexPath())
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: run `whodar index` first", ErrNoIndex)
			}
			if err != nil {
				return err
			}
			w := cmd.ErrOrStderr()
			if enc {
				fmt.Fprintln(w, "index is already encrypted")
			} else {
				ix, err := opts.loadIndex(cmd)
				if err != nil {
					return err
				}
				// Bring the sources sidecar in too so the save rewrites it under
				// the key; leaving it would encrypt the index but not its records.
				if err := opts.loadSources(ix); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				if err := opts.saveIndex(ix); err != nil {
					return err
				}
				fmt.Fprintf(w, "encrypted %s\n", opts.indexPath())
			}
			return sealEpisodes(cmd, opts, w)
		},
	}
}

// newVaultDecryptCmd rewrites the encrypted stores back to plain JSON, needing
// the key that encrypted them.
func newVaultDecryptCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "decrypt",
		Short: "Decrypt the index and conversations back to plain JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			enc, err := isEncryptedOnDisk(opts.indexPath())
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: run `whodar index` first", ErrNoIndex)
			}
			if err != nil {
				return err
			}
			w := cmd.ErrOrStderr()
			if !enc {
				fmt.Fprintln(w, "index is already plain JSON")
			} else {
				ix, err := opts.loadIndex(cmd)
				if err != nil {
					return err
				}
				// Bring the sources sidecar in too so the plain save rewrites it;
				// leaving it would decrypt the index but not its records.
				if err := opts.loadSources(ix); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				if err := ix.Save(opts.indexPath()); err != nil {
					return err
				}
				fmt.Fprintf(w, "decrypted %s\n", opts.indexPath())
			}
			return openEpisodes(cmd, opts, w)
		},
	}
}

// sealEpisodes encrypts the conversation store when one exists and is still
// plain. It holds retained message text, so a key covering only the index
// would leave the more sensitive of the two files readable on a stolen disk.
func sealEpisodes(cmd *cobra.Command, opts *options, w io.Writer) error {
	enc, err := isEncryptedOnDisk(opts.episodePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if enc {
		fmt.Fprintln(w, "conversations are already encrypted")
		return nil
	}
	store, err := opts.loadEpisodes(cmd)
	if err != nil {
		return err
	}
	if err := opts.saveEpisodes(store); err != nil {
		return err
	}
	fmt.Fprintf(w, "encrypted %s\n", opts.episodePath())
	return nil
}

// openEpisodes rewrites the conversation store as plain JSON, so decrypt leaves
// nothing sealed behind.
func openEpisodes(cmd *cobra.Command, opts *options, w io.Writer) error {
	enc, err := isEncryptedOnDisk(opts.episodePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !enc {
		fmt.Fprintln(w, "conversations are already plain JSON")
		return nil
	}
	store, err := opts.loadEpisodes(cmd)
	if err != nil {
		return err
	}
	if err := store.Save(opts.episodePath()); err != nil {
		return err
	}
	fmt.Fprintf(w, "decrypted %s\n", opts.episodePath())
	return nil
}

// fileState describes a store on disk for status: absent, encrypted, or plain.
// absent is what to say when the file is not there yet, which differs between
// the index and the conversations beside it.
func fileState(path, absent string) (string, error) {
	enc, err := isEncryptedOnDisk(path)
	if errors.Is(err, fs.ErrNotExist) {
		return absent, nil
	}
	if err != nil {
		return "", err
	}
	if enc {
		return "encrypted", nil
	}
	return "plain JSON", nil
}

// isEncryptedOnDisk reports whether the file at path carries the vault prefix,
// reading only the prefix rather than the whole index.
func isEncryptedOnDisk(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, vault.MagicLen)
	n, err := io.ReadFull(f, buf)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return vault.IsEncrypted(buf[:n]), nil
}
