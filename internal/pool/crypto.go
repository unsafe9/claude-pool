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
)

// pool.json is encrypted at rest with machine-bound AES-256-GCM. The threat
// model is narrow and honest: this defeats credential scanners (info-stealers
// grepping for sk-ant-, JWTs, JSON key names) and accidental plaintext leaks
// (git commits, screenshots, backups), and makes one machine's ciphertext
// non-portable. It is NOT protection against a targeted local attacker — the
// key-derivation code is open source and the tool must decrypt unattended, so
// anyone who can run it on this machine can recover the keys. No passphrase,
// no OS keychain, no biometrics.

// storeMagic prefixes every encrypted blob. Load uses it to tell ciphertext
// from legacy plaintext JSON (which starts with '{').
var storeMagic = []byte("CPL1")

const storeNonceSize = 12 // AES-GCM standard nonce

// appSalt is a fixed application salt for HKDF. It is not secret.
var appSalt = []byte("claude-pool/store-salt/v1")

// keyInfo binds the derived key to this purpose and format version.
const keyInfo = "claude-pool-store-v1"

// fallbackMachineID stands in when the OS machine id cannot be read. Encryption
// must always happen (plaintext-avoidance is the priority), so we still derive a
// key — this just weakens the machine-binding for that one machine.
const fallbackMachineID = "claude-pool/no-machine-id"

// machineID returns a stable per-machine identifier (see crypto_*.go for the
// platform sources). It is a var so tests can inject a deterministic value.
var machineID = readMachineID

// currentUsername mirrors keychainAccount's resolution (user.Current with $USER
// fallback) so the derived key is stable across the same user's invocations.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}

// deriveKey returns a 32-byte AES-256 key bound to this machine and user via
// HKDF-SHA256. A failed machine-id lookup degrades to the fallback rather than
// erroring, so encryption never silently falls back to plaintext.
func deriveKey() ([]byte, error) {
	id := machineID()
	if id == "" {
		id = fallbackMachineID
	}
	secret := []byte(id + "\x00" + currentUsername())
	return hkdf.Key(sha256.New, secret, appSalt, keyInfo, 32)
}

func gcm() (cipher.AEAD, error) {
	key, err := deriveKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
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

// decrypt reverses encrypt. It errors on a bad magic, a short blob, or a failed
// authentication (tampered ciphertext, or a key derived on a different machine).
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
