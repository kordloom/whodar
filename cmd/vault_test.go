package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/keyring"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/vault"
)

// writeOrgCSV writes a small org chart and returns its path.
func writeOrgCSV(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "people.csv")
	content := "name,email,title,team,topics\n" +
		"Jane Roe,jane@x.com,Staff Engineer,Billing,retries;idempotency\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

// TestEncryptedIndexRoundTrip indexes with a key set, confirms the file on disk
// is encrypted with no plaintext email, then reads it back through ask.
func TestEncryptedIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key, _ := keyring.GenerateKey()
	t.Setenv(keyring.EnvKey, key)
	t.Setenv(keyring.EnvPassphrase, "")
	csv := writeOrgCSV(t, dir)

	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", csv); err != nil {
		t.Fatalf("index: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !vault.IsEncrypted(raw) {
		t.Fatal("index on disk is not encrypted")
	}
	if strings.Contains(string(raw), "jane@x.com") {
		t.Fatal("plaintext email found in encrypted index")
	}

	out, _, err := runCmd(t, "ask", "--data-dir", dir, "who owns billing retries")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !strings.Contains(string(out), "jane@x.com") {
		t.Fatalf("ask did not decrypt the index:\n%s", out)
	}
}

// TestEncryptedIndexNeedsKey verifies a read without the key fails clearly and
// points at the key variables, rather than exposing anything.
func TestEncryptedIndexNeedsKey(t *testing.T) {
	dir := t.TempDir()
	key, _ := keyring.GenerateKey()
	t.Setenv(keyring.EnvKey, key)
	t.Setenv(keyring.EnvPassphrase, "")
	csv := writeOrgCSV(t, dir)
	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", csv); err != nil {
		t.Fatalf("index: %v", err)
	}

	t.Setenv(keyring.EnvKey, "")
	_, _, err := runCmdStdin(t, "", "ask", "--data-dir", dir, "who owns billing")
	if err == nil {
		t.Fatal("ask without key: want error")
		return
	}
	if !strings.Contains(err.Error(), keyring.EnvKey) {
		t.Errorf("error does not name %s: %v", keyring.EnvKey, err)
	}
}

// TestVaultStatusAndKeygen exercises the status and keygen subcommands.
func TestVaultStatusAndKeygen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(keyring.EnvKey, "")
	t.Setenv(keyring.EnvPassphrase, "")

	out, _, err := runCmd(t, "vault", "status", "--data-dir", dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(string(out), "no index yet") || !strings.Contains(string(out), "none") {
		t.Errorf("status without key or index:\n%s", out)
	}

	kout, _, err := runCmd(t, "vault", "keygen")
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(kout)), "export "+keyring.EnvKey+"=") {
		t.Errorf("keygen output: %s", kout)
	}
}

// TestVaultEncryptDecrypt builds a plain index, encrypts it in place with a key,
// then decrypts it back and confirms it still answers.
func TestVaultEncryptDecrypt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(keyring.EnvKey, "")
	t.Setenv(keyring.EnvPassphrase, "")
	csv := writeOrgCSV(t, dir)
	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", csv); err != nil {
		t.Fatalf("index: %v", err)
	}
	idx := filepath.Join(dir, "index.json")
	if raw, _ := os.ReadFile(idx); vault.IsEncrypted(raw) {
		t.Fatal("index unexpectedly encrypted with no key")
	}

	key, _ := keyring.GenerateKey()
	t.Setenv(keyring.EnvKey, key)
	if _, _, err := runCmd(t, "vault", "encrypt", "--data-dir", dir); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if raw, _ := os.ReadFile(idx); !vault.IsEncrypted(raw) {
		t.Fatal("index not encrypted after vault encrypt")
	}

	if _, _, err := runCmd(t, "vault", "decrypt", "--data-dir", dir); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if raw, _ := os.ReadFile(idx); vault.IsEncrypted(raw) {
		t.Fatal("index still encrypted after vault decrypt")
	}
	out, _, err := runCmd(t, "ask", "--data-dir", dir, "who owns retries")
	if err != nil {
		t.Fatalf("ask after decrypt: %v", err)
	}
	if !strings.Contains(string(out), "jane@x.com") {
		t.Fatalf("ask after decrypt:\n%s", out)
	}
}

// TestVaultCoversConversations verifies encrypt, decrypt, and status all cover
// the conversation store and not only the index. That file holds retained
// message text, so a key that sealed the index alone would leave the more
// sensitive of the two readable while status called the install encrypted.
func TestVaultCoversConversations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(keyring.EnvKey, "")
	t.Setenv(keyring.EnvPassphrase, "")
	csv := writeOrgCSV(t, dir)
	if _, _, err := runCmd(t, "index", "--data-dir", dir, "--source", "org-csv", "--file", csv); err != nil {
		t.Fatalf("index: %v", err)
	}

	const secret = "we rotated the signing key by hand"
	store := episode.New()
	store.Add(episode.Episode{
		ID: "slack:C1:1", Source: "slack", Kind: episode.KindThread, Place: "billing",
		Participants: []model.ID{"jane@x.com"},
		Occurred:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Archive:      []episode.Note{{Author: "jane@x.com", Text: secret}},
	})
	eps := filepath.Join(dir, "episodes.json")
	if err := store.Save(eps); err != nil {
		t.Fatalf("save conversations: %v", err)
	}
	if raw, _ := os.ReadFile(eps); !bytes.Contains(raw, []byte(secret)) {
		t.Fatal("setup: the conversation text should start readable on disk")
	}

	key, _ := keyring.GenerateKey()
	t.Setenv(keyring.EnvKey, key)
	if _, _, err := runCmd(t, "vault", "encrypt", "--data-dir", dir); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	raw, err := os.ReadFile(eps)
	if err != nil {
		t.Fatalf("read conversations: %v", err)
	}
	if !vault.IsEncrypted(raw) {
		t.Error("conversation store not encrypted after vault encrypt")
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Error("retained message text still readable on disk after vault encrypt")
	}

	out, _, err := runCmd(t, "vault", "status", "--data-dir", dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Count(string(out), "encrypted") < 2 {
		t.Errorf("status does not report both files as encrypted:\n%s", out)
	}

	if _, _, err := runCmd(t, "vault", "decrypt", "--data-dir", dir); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if raw, _ := os.ReadFile(eps); !bytes.Contains(raw, []byte(secret)) {
		t.Error("conversation store not readable again after vault decrypt")
	}
}
