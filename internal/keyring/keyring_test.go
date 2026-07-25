package keyring

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const passphrase = "seven brass hinges under the stairs"

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), Filename)
}

func TestCreateRequiresSomeSecret(t *testing.T) {
	if _, err := Create(tempPath(t), Secrets{}, nil); err == nil {
		t.Fatal("creating a keyring with no way to unlock it must fail")
	}
}

func TestPassphraseRoundTrip(t *testing.T) {
	path := tempPath(t)

	k, err := Create(path, Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	secret := []byte("SN-4815162342")
	sealed, err := k.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Reopen from disk with only the passphrase, as a restore would.
	reopened, err := Open(path, Secrets{Passphrase: passphrase})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reopened.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("round trip = %q, want %q", got, secret)
	}
}

// TestEitherSecretOpensTheSameData is the whole point of the envelope design: the
// operational key boots the server unattended, and the passphrase recovers a
// backup on a machine that has neither the key file nor the config.
func TestEitherSecretOpensTheSameData(t *testing.T) {
	path := tempPath(t)
	opKey, err := NewOperationalKey()
	if err != nil {
		t.Fatalf("NewOperationalKey: %v", err)
	}

	k, err := Create(path, Secrets{Passphrase: passphrase, OperationalKey: opKey}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sealed, err := k.Encrypt([]byte("receipt total 1299.00"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	for name, secrets := range map[string]Secrets{
		"passphrase only":  {Passphrase: passphrase},
		"operational only": {OperationalKey: opKey},
		"both":             {Passphrase: passphrase, OperationalKey: opKey},
	} {
		t.Run(name, func(t *testing.T) {
			opened, err := Open(path, secrets)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			got, err := opened.Decrypt(sealed)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(got) != "receipt total 1299.00" {
				t.Errorf("decrypted %q", got)
			}
		})
	}
}

func TestWrongSecretsDoNotOpen(t *testing.T) {
	path := tempPath(t)
	opKey, _ := NewOperationalKey()
	if _, err := Create(path, Secrets{Passphrase: passphrase, OperationalKey: opKey}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	otherKey, _ := NewOperationalKey()
	for name, secrets := range map[string]Secrets{
		"empty":               {},
		"wrong passphrase":    {Passphrase: "not the passphrase at all"},
		"wrong op key":        {OperationalKey: otherKey},
		"passphrase as opkey": {OperationalKey: passphrase},
		"both wrong":          {Passphrase: "nope", OperationalKey: otherKey},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(path, secrets); !errors.Is(err, ErrLocked) {
				t.Errorf("Open should fail with ErrLocked, got %v", err)
			}
		})
	}
}

// TestKeyringFileIsInert checks that the file committed to a backup gives nothing
// away. It is stored inside the data directory, so this is load-bearing.
func TestKeyringFileIsInert(t *testing.T) {
	path := tempPath(t)
	opKey, _ := NewOperationalKey()
	k, err := Create(path, Secrets{Passphrase: passphrase, OperationalKey: opKey}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	contents := string(raw)

	for _, secret := range []string{passphrase, opKey} {
		if strings.Contains(contents, secret) {
			t.Errorf("the keyring file contains a secret verbatim:\n%s", contents)
		}
	}
	// The data key itself must not be recoverable from the file.
	if strings.Contains(contents, string(k.dataKey)) {
		t.Error("the keyring file contains the raw data key")
	}

	// Only owner-readable, since it sits next to the data.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("keyring mode is %v; it should not be readable by group or others", perm)
	}
}

func TestAddPassphraseLaterWithoutReEncrypting(t *testing.T) {
	path := tempPath(t)
	opKey, _ := NewOperationalKey()

	// Start with only an operational key, as a Docker deployment would.
	k, err := Create(path, Secrets{OperationalKey: opKey}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sealed, err := k.Encrypt([]byte("SN-000123"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if k.Has(KindPassphrase) {
		t.Fatal("should not have a passphrase wrap yet")
	}

	// Later, the user adds a recovery passphrase.
	if err := k.AddPassphrase(passphrase, "recovery"); err != nil {
		t.Fatalf("AddPassphrase: %v", err)
	}
	if !k.Has(KindPassphrase) {
		t.Error("passphrase wrap should now exist")
	}

	// Data encrypted *before* the passphrase existed must still open with it —
	// that is what makes adding a wrap cheap instead of a migration.
	opened, err := Open(path, Secrets{Passphrase: passphrase})
	if err != nil {
		t.Fatalf("Open with the new passphrase: %v", err)
	}
	got, err := opened.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt data written before the passphrase existed: %v", err)
	}
	if string(got) != "SN-000123" {
		t.Errorf("decrypted %q", got)
	}
}

func TestRemoveWrapRefusesTheLastOne(t *testing.T) {
	path := tempPath(t)
	opKey, _ := NewOperationalKey()
	k, err := Create(path, Secrets{Passphrase: passphrase, OperationalKey: opKey}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wraps := k.Wraps()
	if len(wraps) != 2 {
		t.Fatalf("%d wraps, want 2", len(wraps))
	}

	if err := k.RemoveWrap(wraps[0].ID); err != nil {
		t.Fatalf("RemoveWrap: %v", err)
	}
	// Removing the last one would leave data nobody can ever read, and there is
	// no way to warn someone afterwards.
	if err := k.RemoveWrap(k.Wraps()[0].ID); !errors.Is(err, ErrLastWrap) {
		t.Errorf("removing the final wrap should fail with ErrLastWrap, got %v", err)
	}
	if err := k.RemoveWrap("wrap_doesnotexist"); err == nil {
		t.Error("removing an unknown wrap should fail")
	}
}

// TestVerifyLetsSomeoneTestTheirRecoveryPassphrase — an untested backup is not a
// backup, and finding out during a real restore is finding out too late.
func TestVerify(t *testing.T) {
	path := tempPath(t)
	k, err := Create(path, Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := k.Verify(Secrets{Passphrase: passphrase}); err != nil {
		t.Errorf("the correct passphrase should verify: %v", err)
	}
	if err := k.Verify(Secrets{Passphrase: "misremembered"}); !errors.Is(err, ErrWrongSecret) {
		t.Errorf("a wrong passphrase should report ErrWrongSecret, got %v", err)
	}
}

func TestEncryptionIsAuthenticated(t *testing.T) {
	k, err := Create(tempPath(t), Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sealed, err := k.Encrypt([]byte("SN-tamper-target"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a bit in the ciphertext body. GCM must reject it rather than return
	// altered plaintext.
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := k.Decrypt(tampered); err == nil {
		t.Error("a tampered ciphertext must not decrypt")
	}

	// And in the nonce.
	tampered = bytes.Clone(sealed)
	tampered[0] ^= 0x01
	if _, err := k.Decrypt(tampered); err == nil {
		t.Error("a tampered nonce must not decrypt")
	}

	if _, err := k.Decrypt([]byte("short")); err == nil {
		t.Error("a truncated ciphertext must not decrypt")
	}
}

func TestEncryptionIsNotDeterministic(t *testing.T) {
	k, err := Create(tempPath(t), Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Two devices with the same serial must not produce identical ciphertext, or
	// the database reveals which rows match without decrypting anything.
	a, _ := k.Encrypt([]byte("SN-identical"))
	b, _ := k.Encrypt([]byte("SN-identical"))
	if bytes.Equal(a, b) {
		t.Error("encrypting the same value twice produced identical ciphertext")
	}
}

// TestIndexEnablesExactMatchSearch covers the property that keeps encrypted serial
// numbers findable.
func TestIndexEnablesExactMatchSearch(t *testing.T) {
	path := tempPath(t)
	k, err := Create(path, Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const serial = "SN-4815162342"
	stored, err := k.Index(serial)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Deterministic, so a lookup can hash the query and compare.
	again, _ := k.Index(serial)
	if stored != again {
		t.Error("Index must be deterministic or lookup cannot work")
	}
	// Different values must not collide.
	other, _ := k.Index("SN-9999999999")
	if stored == other {
		t.Error("different serials produced the same index")
	}
	// It must not leak the plaintext.
	if strings.Contains(stored, serial) || strings.Contains(stored, "4815") {
		t.Errorf("the index leaks the serial: %s", stored)
	}
	// It must survive a reopen, or yesterday's rows become unsearchable.
	reopened, err := Open(path, Secrets{Passphrase: passphrase})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if after, _ := reopened.Index(serial); after != stored {
		t.Error("the index changed across a reopen; stored rows would stop matching")
	}
}

func TestIndexKeyIsNotTheEncryptionKey(t *testing.T) {
	k, err := Create(tempPath(t), Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	idx, err := k.Index("value")
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	// The index is derived through HKDF, so it must not simply be a digest under
	// the data key itself.
	if strings.Contains(idx, string(k.dataKey)) {
		t.Error("the index appears to expose the data key")
	}
}

func TestTwoKeyringsAreIndependent(t *testing.T) {
	a, err := Create(tempPath(t), Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := Create(tempPath(t), Secrets{Passphrase: passphrase}, nil)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}

	sealed, _ := a.Encrypt([]byte("secret"))
	// The same passphrase on a different instance must not decrypt: each keyring
	// has its own random data key and its own salt.
	if _, err := b.Decrypt(sealed); err == nil {
		t.Error("a different keyring with the same passphrase decrypted the data")
	}
}

func TestOpenMissingAndCorruptFiles(t *testing.T) {
	dir := t.TempDir()

	if _, err := Open(filepath.Join(dir, "absent.json"), Secrets{Passphrase: passphrase}); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound for a missing keyring, got %v", err)
	}

	for name, contents := range map[string]string{
		"not json":     "{{{",
		"no wraps":     `{"version":1,"wraps":[]}`,
		"bad version":  `{"version":99,"wraps":[{"id":"x","kind":"passphrase","ciphertext":"aa"}]}`,
		"empty object": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, "corrupt.json")
			if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := Open(p, Secrets{Passphrase: passphrase}); err == nil {
				t.Error("a corrupt keyring should not open")
			}
		})
	}
}

func TestWrapsExposeNoSecretMaterial(t *testing.T) {
	k, err := Create(tempPath(t), Secrets{Passphrase: passphrase}, map[string]string{
		KindPassphrase: "my recovery phrase",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Wraps() feeds doctor and the UI, so it must be safe to display.
	for _, w := range k.Wraps() {
		if w.Ciphertext != "" || w.Salt != "" {
			t.Errorf("Wraps() exposed key material: %+v", w)
		}
		if w.ID == "" || w.Kind == "" || w.CreatedAt == "" {
			t.Errorf("Wraps() should still describe the wrap usefully: %+v", w)
		}
	}

	encoded, err := json.Marshal(k.Wraps())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), passphrase) {
		t.Error("serialising Wraps() leaked the passphrase")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	path := tempPath(t)
	if _, err := Create(path, Secrets{Passphrase: passphrase}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// No temporary files may survive; the keyring is the one file whose loss is
	// unrecoverable, so a half-written copy must never be left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".keyring-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}
