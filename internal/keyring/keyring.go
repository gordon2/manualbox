// Package keyring manages the key that protects manualbox's most sensitive
// fields: serial numbers, receipts, and purchase prices.
//
// # The problem it solves
//
// A key the user can remember cannot survive an unattended restart — a household
// server should come back after a power cut without someone connecting to type a
// passphrase. A key that survives restarts is a file or an environment variable,
// which nobody can remember, and which is useless for recovering a backup onto a
// different machine.
//
// Both are needed, so neither encrypts the data directly. Instead:
//
//  1. A random 32-byte data key is generated once. It never leaves the process and
//     is never shown to anyone.
//  2. That data key is *wrapped* (encrypted) separately by each unlocking method:
//     a passphrase the user chose, and/or a high-entropy operational key held
//     outside the data directory.
//  3. Any single wrap opens the same data key.
//
// This gives two paths with different jobs. The operational key unlocks the
// instance on boot with no human present. The passphrase is the recovery path: it
// still works when the machine, its config, and its key file are all gone, which
// is exactly the situation someone restoring a backup is in.
//
// It also makes key management cheap. Adding a passphrase, removing one, or
// rotating the operational key rewraps a 32-byte secret — it never re-encrypts the
// stored data.
//
// # What this protects against
//
// One thing: a copy of the data directory escaping. Backups land in cloud storage,
// on drives that get sold, and on network shares that turn out to be reachable.
// The wrapped key is stored *in* the data directory precisely because it is
// inert — without a passphrase or the operational key it is 32 bytes of noise.
//
// It does not protect a running server from being compromised. The data key is in
// memory, and on a machine using an operational key that key is reachable too.
// That threat is answered by authentication and by not exposing the instance, not
// by encryption.
package keyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/crypto/argon2"
)

// Sizes, all in bytes.
const (
	dataKeySize = 32 // AES-256
	saltSize    = 16
	nonceSize   = 12 // AES-GCM standard nonce
)

// Argon2id parameters for deriving a key-encryption key from a passphrase.
//
// Far heavier than the login hash in internal/auth: a login is verified online
// against a rate-limited server, while a wrapped key sits in a stolen backup where
// an attacker can grind offline for as long as they like.
//
// 256 MiB and 4 passes measures at about 200 ms on an Apple-silicon laptop, and
// noticeably longer on a Raspberry Pi. That is paid once when unlocking — never
// per request — and it makes millions of offline guesses expensive. Unlocking with
// an operational key skips the KDF entirely, which is what keeps an unattended
// boot instant.
const (
	kdfMemory  = 256 * 1024 // KiB
	kdfTime    = 4
	kdfThreads = 4
)

// Wrap kinds.
const (
	// KindPassphrase is unlocked by something the user remembers. The recovery path.
	KindPassphrase = "passphrase"
	// KindOperational is unlocked by a high-entropy key held outside the data
	// directory. The unattended-boot path.
	KindOperational = "operational"
)

var (
	// ErrLocked is returned when no supplied secret opened any wrap.
	ErrLocked = errors.New("keyring: no passphrase or operational key opened the keyring")
	// ErrNotFound is returned when no keyring file exists yet.
	ErrNotFound = errors.New("keyring: no keyring has been created")
	// ErrWrongSecret is returned when a specific secret failed to unwrap.
	ErrWrongSecret = errors.New("keyring: that secret is not correct")
	// ErrLastWrap is returned when removing a wrap would make the data
	// permanently unrecoverable.
	ErrLastWrap = errors.New("keyring: refusing to remove the only remaining way to unlock the data")
)

// Wrap is one encrypted copy of the data key.
type Wrap struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`

	// Salt and the KDF parameters are per-wrap, so raising the cost for new
	// passphrases does not invalidate existing ones.
	Salt       string `json:"salt,omitempty"`
	KDFMemory  uint32 `json:"kdf_memory,omitempty"`
	KDFTime    uint32 `json:"kdf_time,omitempty"`
	KDFThreads uint8  `json:"kdf_threads,omitempty"`

	// Ciphertext is the wrapped data key: nonce ‖ AES-GCM(data key).
	Ciphertext string `json:"ciphertext"`
}

// File is the on-disk keyring. It is safe to back up: every field is either
// public or inert without a secret the file does not contain.
type File struct {
	Version int    `json:"version"`
	Wraps   []Wrap `json:"wraps"`
}

// Keyring holds the unwrapped data key for the life of the process.
type Keyring struct {
	dataKey []byte
	file    File
	path    string
}

// Filename is the keyring's name inside the data directory.
const Filename = "keyring.json"

// Path returns the keyring location for a data directory.
func Path(dataDir string) string { return filepath.Join(dataDir, Filename) }

// Secrets are the things that might open a keyring. Both are optional; at least
// one must be present.
type Secrets struct {
	Passphrase     string
	OperationalKey string // base64, as produced by NewOperationalKey
}

// NewOperationalKey returns a fresh high-entropy key for unattended unlocking,
// encoded for an environment variable or a secrets manager.
func NewOperationalKey() (string, error) {
	raw := make([]byte, dataKeySize)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("keyring: generate operational key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Create builds a new keyring with a wrap for each secret provided and writes it
// to path.
func Create(path string, secrets Secrets, labels map[string]string) (*Keyring, error) {
	if secrets.Passphrase == "" && secrets.OperationalKey == "" {
		return nil, errors.New("keyring: a passphrase or an operational key is required")
	}

	dataKey := make([]byte, dataKeySize)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, fmt.Errorf("keyring: generate data key: %w", err)
	}

	k := &Keyring{dataKey: dataKey, file: File{Version: 1}, path: path}

	if secrets.Passphrase != "" {
		if err := k.addPassphraseWrap(secrets.Passphrase, labels[KindPassphrase]); err != nil {
			return nil, err
		}
	}
	if secrets.OperationalKey != "" {
		if err := k.addOperationalWrap(secrets.OperationalKey, labels[KindOperational]); err != nil {
			return nil, err
		}
	}
	if err := k.save(); err != nil {
		return nil, err
	}
	return k, nil
}

// Open reads a keyring and unlocks it with whichever secret works.
//
// Both secrets are tried so that a deployment can pass an operational key while a
// human can still recover with a passphrase, without either needing to know which
// wraps exist.
func Open(path string, secrets Secrets) (*Keyring, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-configured data directory
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, fmt.Errorf("keyring: read %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("keyring: parse %s: %w", path, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("keyring: unsupported keyring version %d", f.Version)
	}
	if len(f.Wraps) == 0 {
		return nil, fmt.Errorf("keyring: %s contains no wraps", path)
	}

	for _, w := range f.Wraps {
		var kek []byte
		switch w.Kind {
		case KindPassphrase:
			if secrets.Passphrase == "" {
				continue
			}
			kek, err = passphraseKEK(secrets.Passphrase, w)
		case KindOperational:
			if secrets.OperationalKey == "" {
				continue
			}
			kek, err = operationalKEK(secrets.OperationalKey)
		default:
			continue
		}
		if err != nil {
			continue
		}

		dataKey, err := unwrap(kek, w)
		if err != nil {
			continue // wrong secret for this wrap; try the next
		}
		return &Keyring{dataKey: dataKey, file: f, path: path}, nil
	}
	return nil, ErrLocked
}

// Wraps describes the configured unlocking methods, for `doctor` and the UI. It
// exposes no secret material.
func (k *Keyring) Wraps() []Wrap {
	out := make([]Wrap, len(k.file.Wraps))
	copy(out, k.file.Wraps)
	for i := range out {
		out[i].Ciphertext = ""
		out[i].Salt = ""
	}
	return out
}

// Has reports whether a wrap of the given kind exists.
func (k *Keyring) Has(kind string) bool {
	return slices.ContainsFunc(k.file.Wraps, func(w Wrap) bool { return w.Kind == kind })
}

// AddPassphrase adds a passphrase wrap, so a user who started with only an
// operational key gains a recovery path without re-encrypting anything.
func (k *Keyring) AddPassphrase(passphrase, label string) error {
	if passphrase == "" {
		return errors.New("keyring: passphrase must not be empty")
	}
	if err := k.addPassphraseWrap(passphrase, label); err != nil {
		return err
	}
	return k.save()
}

// AddOperationalKey adds an operational wrap.
func (k *Keyring) AddOperationalKey(key, label string) error {
	if key == "" {
		return errors.New("keyring: operational key must not be empty")
	}
	if err := k.addOperationalWrap(key, label); err != nil {
		return err
	}
	return k.save()
}

// RemoveWrap deletes a wrap by ID, refusing to remove the last one.
//
// The refusal is the point: a keyring with no wraps is data nobody can ever read
// again, and there is no way to warn someone after the fact.
func (k *Keyring) RemoveWrap(id string) error {
	idx := slices.IndexFunc(k.file.Wraps, func(w Wrap) bool { return w.ID == id })
	if idx < 0 {
		return fmt.Errorf("keyring: no wrap with id %s", id)
	}
	if len(k.file.Wraps) == 1 {
		return ErrLastWrap
	}
	k.file.Wraps = slices.Delete(k.file.Wraps, idx, idx+1)
	return k.save()
}

// Verify reports whether a secret opens this keyring, without changing anything.
//
// This exists so someone can *test* their recovery passphrase. An untested backup
// is not a backup, and discovering a forgotten passphrase during an actual restore
// is discovering it far too late.
func (k *Keyring) Verify(secrets Secrets) error {
	other, err := Open(k.path, secrets)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return ErrWrongSecret
		}
		return err
	}
	if subtle.ConstantTimeCompare(other.dataKey, k.dataKey) != 1 {
		return ErrWrongSecret
	}
	return nil
}

// Encrypt seals a value with the data key. The nonce is prepended, so the result
// is self-contained and safe to store in a single column.
func (k *Keyring) Encrypt(plaintext []byte) ([]byte, error) {
	gcm, err := k.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("keyring: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a value produced by Encrypt. A modified ciphertext fails rather
// than returning altered plaintext, because GCM authenticates.
func (k *Keyring) Decrypt(sealed []byte) ([]byte, error) {
	gcm, err := k.gcm()
	if err != nil {
		return nil, err
	}
	if len(sealed) < nonceSize {
		return nil, errors.New("keyring: ciphertext is too short")
	}
	plaintext, err := gcm.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("keyring: decrypt: %w", err)
	}
	return plaintext, nil
}

// Index returns a stable keyed digest of a value, for exact-match lookup of a
// field whose plaintext is not stored.
//
// This is what keeps serial numbers searchable while encrypted: store the
// ciphertext for display and the index for finding. It is keyed with a subkey
// derived from the data key, not the data key itself, so the index cannot be used
// to attack the encryption. Substring search is not possible — which is fine,
// because nobody types half a serial number.
func (k *Keyring) Index(value string) (string, error) {
	subkey, err := k.subkey("manualbox/index/v1")
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, subkey)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// --- internals ---

func (k *Keyring) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.dataKey)
	if err != nil {
		return nil, fmt.Errorf("keyring: init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyring: init GCM: %w", err)
	}
	return gcm, nil
}

// subkey derives a purpose-specific key so that distinct uses of the data key
// cannot interfere with one another.
func (k *Keyring) subkey(purpose string) ([]byte, error) {
	out, err := hkdf.Key(sha256.New, k.dataKey, nil, purpose, 32)
	if err != nil {
		return nil, fmt.Errorf("keyring: derive subkey: %w", err)
	}
	return out, nil
}

func (k *Keyring) addPassphraseWrap(passphrase, label string) error {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("keyring: generate salt: %w", err)
	}
	w := Wrap{
		ID:         newID(),
		Kind:       KindPassphrase,
		Label:      label,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		KDFMemory:  kdfMemory,
		KDFTime:    kdfTime,
		KDFThreads: kdfThreads,
	}
	kek := argon2.IDKey([]byte(passphrase), salt, w.KDFTime, w.KDFMemory, w.KDFThreads, dataKeySize)

	sealed, err := wrapKey(kek, k.dataKey)
	if err != nil {
		return err
	}
	w.Ciphertext = sealed
	k.file.Wraps = append(k.file.Wraps, w)
	return nil
}

func (k *Keyring) addOperationalWrap(key, label string) error {
	kek, err := operationalKEK(key)
	if err != nil {
		return err
	}
	sealed, err := wrapKey(kek, k.dataKey)
	if err != nil {
		return err
	}
	k.file.Wraps = append(k.file.Wraps, Wrap{
		ID:         newID(),
		Kind:       KindOperational,
		Label:      label,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Ciphertext: sealed,
	})
	return nil
}

func passphraseKEK(passphrase string, w Wrap) ([]byte, error) {
	salt, err := base64.RawStdEncoding.DecodeString(w.Salt)
	if err != nil {
		return nil, fmt.Errorf("keyring: decode salt: %w", err)
	}
	if w.KDFMemory == 0 || w.KDFTime == 0 || w.KDFThreads == 0 {
		return nil, errors.New("keyring: wrap is missing its KDF parameters")
	}
	return argon2.IDKey([]byte(passphrase), salt, w.KDFTime, w.KDFMemory, w.KDFThreads, dataKeySize), nil
}

// operationalKEK turns the stored operational key into a key-encryption key.
//
// It passes through HKDF rather than being used directly so that the same key
// material could safely serve another purpose later.
func operationalKEK(key string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		// Tolerate padded base64, since people paste from many places.
		raw, err = base64.StdEncoding.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("keyring: operational key is not valid base64: %w", err)
		}
	}
	if len(raw) < 16 {
		return nil, errors.New("keyring: operational key is too short")
	}
	out, err := hkdf.Key(sha256.New, raw, nil, "manualbox/operational-kek/v1", dataKeySize)
	if err != nil {
		return nil, fmt.Errorf("keyring: derive operational KEK: %w", err)
	}
	return out, nil
}

func wrapKey(kek, dataKey []byte) (string, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return "", fmt.Errorf("keyring: init wrap cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("keyring: init wrap GCM: %w", err)
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("keyring: generate wrap nonce: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, dataKey, nil)), nil
}

func unwrap(kek []byte, w Wrap) ([]byte, error) {
	sealed, err := base64.RawStdEncoding.DecodeString(w.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < nonceSize {
		return nil, errors.New("keyring: wrap ciphertext is too short")
	}
	return gcm.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
}

// save writes the keyring atomically, so an interrupted write cannot destroy the
// only copy of the wrapped key.
func (k *Keyring) save() error {
	data, err := json.MarshalIndent(k.file, "", "  ")
	if err != nil {
		return fmt.Errorf("keyring: encode: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(k.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("keyring: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".keyring-*")
	if err != nil {
		return fmt.Errorf("keyring: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("keyring: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("keyring: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keyring: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("keyring: set permissions: %w", err)
	}
	if err := os.Rename(tmpName, k.path); err != nil {
		return fmt.Errorf("keyring: commit: %w", err)
	}
	return nil
}

func newID() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "wrap"
	}
	return "wrap_" + hex.EncodeToString(raw)
}
