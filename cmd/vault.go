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
		Short: "Encrypt the on-disk index at rest",
		Long: `Encrypt the index at rest so a stolen disk or a stray backup cannot read your
people graph. Encryption turns on whenever a key is configured:

  WHODAR_INDEX_KEY         a base64 32-byte key, best for automation
  WHODAR_INDEX_PASSPHRASE  a passphrase, prompted if unset on a terminal

Generate a key with "whodar vault keygen". With a key set, every index write is
encrypted and every read decrypts. Reading an encrypted index without the key
fails rather than exposing anything.`,
	}
	cmd.AddCommand(
		newVaultStatusCmd(opts), newVaultKeygenCmd(), newVaultEncryptCmd(opts), newVaultDecryptCmd(opts))
	return cmd
}

// newVaultStatusCmd reports whether a key is configured and whether the index on
// disk is encrypted, without decrypting anything.
func newVaultStatusCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the key and encryption state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "index: %s\n", opts.indexPath())
			if src := keyring.Source(); src != "" {
				fmt.Fprintf(out, "key:   configured via %s\n", src)
			} else {
				fmt.Fprintln(out, "key:   none (index is stored as plain JSON)")
			}
			state, err := indexState(opts.indexPath())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "file:  %s\n", state)
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

// newVaultEncryptCmd rewrites an existing plain index in its encrypted form.
func newVaultEncryptCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "encrypt",
		Short: "Encrypt an existing plain index in place",
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
			if enc {
				fmt.Fprintln(cmd.ErrOrStderr(), "index is already encrypted")
				return nil
			}
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return err
			}
			if err := opts.saveIndex(ix); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "encrypted %s\n", opts.indexPath())
			return nil
		},
	}
}

// newVaultDecryptCmd rewrites an encrypted index back to plain JSON, needing the
// key that encrypted it.
func newVaultDecryptCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "decrypt",
		Short: "Decrypt the index back to plain JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			enc, err := isEncryptedOnDisk(opts.indexPath())
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: run `whodar index` first", ErrNoIndex)
			}
			if err != nil {
				return err
			}
			if !enc {
				fmt.Fprintln(cmd.ErrOrStderr(), "index is already plain JSON")
				return nil
			}
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return err
			}
			if err := ix.Save(opts.indexPath()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "decrypted %s\n", opts.indexPath())
			return nil
		},
	}
}

// indexState describes the on-disk index for status: absent, encrypted, or plain.
func indexState(path string) (string, error) {
	enc, err := isEncryptedOnDisk(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "no index yet", nil
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
