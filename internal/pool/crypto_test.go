package pool

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withMachineID swaps in a deterministic machine id for the duration of a test.
func withMachineID(t *testing.T, id string) {
	t.Helper()
	prev := machineID
	machineID = func() string { return id }
	t.Cleanup(func() { machineID = prev })
}

func TestCrypto_RoundTrip(t *testing.T) {
	withMachineID(t, "test-machine-id")

	plain := []byte(`{"accounts":[{"id":"alice","blob":"sk-ant-secret"}]}`)
	enc, err := encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.HasPrefix(enc, []byte("{")) {
		t.Fatal("ciphertext starts with '{' (looks like plaintext)")
	}
	if !bytes.HasPrefix(enc, storeMagic) {
		t.Fatalf("ciphertext missing magic, got %x", enc[:4])
	}
	if bytes.Contains(enc, []byte("sk-ant-secret")) {
		t.Fatal("plaintext secret leaked into ciphertext")
	}

	got, err := decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", got, plain)
	}
}

func TestCrypto_DecryptWrongKeyFails(t *testing.T) {
	withMachineID(t, "machine-a")
	enc, err := encrypt([]byte("secret payload"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// A blob produced under machine-a must not decrypt under machine-b.
	withMachineID(t, "machine-b")
	if _, err := decrypt(enc); err == nil {
		t.Fatal("decrypt with a foreign machine-id key unexpectedly succeeded")
	}
}

func TestStore_EncryptedSaveLoadRoundTrip(t *testing.T) {
	withMachineID(t, "test-machine-id")
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.json")
	prev := StorePath
	StorePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { StorePath = prev })

	s := &Store{path: path}
	s.Upsert(&Account{ID: "alice", Blob: `{"claudeAiOauth":{"accessToken":"sk-ant-xyz"}}`})
	s.UpsertKey(&APIKey{ID: "k1", Key: "sk-ant-apikey"})
	s.Current = "alice"
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// On disk: encrypted, no plaintext patterns.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.HasPrefix(raw, storeMagic) {
		t.Fatalf("on-disk store not encrypted, prefix = %q", raw[:4])
	}
	if bytes.Contains(raw, []byte("sk-ant")) || bytes.Contains(raw, []byte("alice")) {
		t.Fatal("plaintext leaked into encrypted store on disk")
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Current != "alice" || len(got.Accounts) != 1 || got.Accounts[0].Blob != s.Accounts[0].Blob {
		t.Fatalf("loaded store mismatch: %+v", got)
	}
	if len(got.APIKeys) != 1 || got.APIKeys[0].Key != "sk-ant-apikey" {
		t.Fatalf("loaded keys mismatch: %+v", got.APIKeys)
	}
}

func TestStore_LegacyPlaintextMigration(t *testing.T) {
	withMachineID(t, "test-machine-id")
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.json")
	prev := StorePath
	StorePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { StorePath = prev })

	// Seed a legacy plaintext JSON store.
	legacy := &Store{
		Current:  "bob",
		Accounts: []*Account{{ID: "bob", Blob: `{"claudeAiOauth":{"accessToken":"plain"}}`}},
	}
	plain, _ := json.MarshalIndent(legacy, "", "  ")
	if !bytes.HasPrefix(plain, []byte("{")) {
		t.Fatal("seed is not plaintext JSON")
	}
	if err := os.WriteFile(path, plain, 0o600); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	// Load parses the legacy plaintext.
	s, err := Load()
	if err != nil {
		t.Fatalf("Load legacy: %v", err)
	}
	if s.Current != "bob" || len(s.Accounts) != 1 {
		t.Fatalf("legacy load mismatch: %+v", s)
	}

	// Save re-encrypts; plaintext disappears from disk.
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.HasPrefix(raw, []byte("{")) {
		t.Fatal("store still plaintext after migration Save")
	}
	if !bytes.HasPrefix(raw, storeMagic) {
		t.Fatalf("migrated store not encrypted, prefix = %q", raw[:4])
	}

	// And it still loads back correctly.
	s2, err := Load()
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if s2.Current != "bob" || len(s2.Accounts) != 1 || s2.Accounts[0].Blob != legacy.Accounts[0].Blob {
		t.Fatalf("post-migration load mismatch: %+v", s2)
	}
}
