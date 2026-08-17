package pool

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"os/user"
	"sync"
)

// pool.json is encrypted at rest with machine-bound AES-256-GCM. The threat
// model is narrow and worth stating honestly: this defeats credential scanners
// and accidental plaintext leaks (git, screenshots, backups), and makes one
// machine's ciphertext non-portable. It is NOT protection against a targeted
// local attacker — the derivation is open source and the tool decrypts
// unattended, so anyone who can run it here can recover the keys.

// storeMagic tells ciphertext from legacy plaintext JSON, which starts with '{'.
var storeMagic = []byte("CPL1")

const storeNonceSize = 12 // AES-GCM standard nonce

var appSalt = []byte("claude-pool/store-salt/v1") // fixed, not secret

// keyInfo binds the derived key to this purpose and format version.
const keyInfo = "claude-pool-store-v1"

// fallbackMachineID keeps encryption working when the OS machine id is
// unreadable, at the cost of machine-binding on that one machine — never worth
// falling back to plaintext for.
const fallbackMachineID = "claude-pool/no-machine-id"

// A var so tests can inject a deterministic value.
var machineID = readMachineID

// currentUsername mirrors keychainAccount's resolution so the derived key stays
// stable across the same user's invocations.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}

// deriveKey binds the key to this machine and user via HKDF-SHA256.
func deriveKey() ([]byte, error) {
	id := machineID()
	if id == "" {
		id = fallbackMachineID
	}
	secret := []byte(id + "\x00" + currentUsername())
	return hkdf.Key(sha256.New, secret, appSalt, keyInfo, 32)
}

var (
	aeadMu    sync.Mutex
	aeadCache cipher.AEAD
)

// gcm caches the AEAD because deriving the key shells out for the machine id
// (`ioreg` on macOS, ~30ms), and a single `auto` run loads or saves the store
// a dozen times — enough to eat a noticeable slice of the hook's budget.
func gcm() (cipher.AEAD, error) {
	aeadMu.Lock()
	defer aeadMu.Unlock()
	if aeadCache != nil {
		return aeadCache, nil
	}
	key, err := deriveKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	aeadCache = aead
	return aead, nil
}

// resetKeyCache drops the cached AEAD; tests that swap the machine id use it to
// force re-derivation.
func resetKeyCache() {
	aeadMu.Lock()
	defer aeadMu.Unlock()
	aeadCache = nil
}

// encrypt seals plaintext as magic(4) + nonce(12) + GCM ciphertext-with-tag.
func encrypt(plaintext []byte) ([]byte, error) {
	aead, err := gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, storeNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(storeMagic)+storeNonceSize+len(plaintext)+aead.Overhead())
	out = append(out, storeMagic...)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plaintext, nil), nil
}

// decrypt reverses encrypt. Authentication failure means tampering — or, far
// more often, a key derived on a different machine or under a different user.
func decrypt(data []byte) ([]byte, error) {
	if len(data) < len(storeMagic)+storeNonceSize {
		return nil, errors.New("pool: ciphertext too short")
	}
	if string(data[:len(storeMagic)]) != string(storeMagic) {
		return nil, errors.New("pool: bad store magic")
	}
	aead, err := gcm()
	if err != nil {
		return nil, err
	}
	nonce := data[len(storeMagic) : len(storeMagic)+storeNonceSize]
	ct := data[len(storeMagic)+storeNonceSize:]
	return aead.Open(nil, nonce, ct, nil)
}
