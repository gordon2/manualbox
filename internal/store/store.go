// Package store is a content-addressed blob store on the local filesystem.
//
// Every uploaded file — the original PDF, a photographed page, an extracted
// illustration — is stored under the SHA-256 of its bytes and never modified
// afterwards. That gives three properties manualbox depends on:
//
//   - Originals are preserved exactly. Everything else (canonical HTML,
//     translations, extracted plans) is derived and can be regenerated, so the
//     original is the one thing that must survive verbatim.
//   - Deduplication is free. Uploading the same manual twice, or the same
//     warranty card against two devices, costs one copy.
//   - Integrity is verifiable. The filename *is* the checksum, so bit rot and
//     truncated writes are detectable.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// digestLen is the length of a hex-encoded SHA-256.
const digestLen = sha256.Size * 2

var (
	// ErrNotFound is returned when no blob has the requested digest.
	ErrNotFound = errors.New("blob not found")
	// ErrBadDigest is returned when a digest is not a valid hex SHA-256.
	ErrBadDigest = errors.New("invalid blob digest")
)

// Store holds blobs under a root directory.
type Store struct {
	root string
}

// Ref identifies stored bytes.
type Ref struct {
	// SHA256 is the lowercase hex digest, which is also the storage key.
	SHA256 string
	// Size is the length in bytes.
	Size int64
}

// New opens (creating if needed) a blob store rooted at dir.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("store: root directory is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve %q: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("store: create %s: %w", abs, err)
	}
	if err := os.MkdirAll(filepath.Join(abs, "tmp"), 0o750); err != nil {
		return nil, fmt.Errorf("store: create temp dir: %w", err)
	}
	return &Store{root: abs}, nil
}

// Put streams r into the store and returns its reference.
//
// The digest is not known until the bytes have been read, so the content goes to
// a temporary file first and is renamed into place once its name is known. The
// rename is atomic, so a crash mid-upload leaves a temp file to clean up rather
// than a corrupt blob that appears complete — which matters because a truncated
// blob whose name claims a digest it does not have would be undetectable
// without re-hashing everything.
//
// If the blob already exists, the upload is discarded and the existing entry is
// returned.
func (s *Store) Put(ctx context.Context, r io.Reader) (Ref, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "upload-*")
	if err != nil {
		return Ref{}, fmt.Errorf("store: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// Remove the temp file unless it was successfully renamed away. Both errors
	// are ignored deliberately: on the success path the file is already closed
	// and gone, and on the failure path the original error is the useful one.
	renamed := false
	defer func() {
		_ = tmp.Close()
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return Ref{}, fmt.Errorf("store: write blob: %w", err)
	}

	// These bytes are the user's irreplaceable original; make them durable
	// before the rename advertises them as complete.
	if err := tmp.Sync(); err != nil {
		return Ref{}, fmt.Errorf("store: sync blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Ref{}, fmt.Errorf("store: close blob: %w", err)
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	ref := Ref{SHA256: digest, Size: size}

	final, err := s.pathFor(digest)
	if err != nil {
		return Ref{}, err
	}

	// Identical bytes are already stored: keep the existing copy.
	if _, err := os.Stat(final); err == nil {
		return ref, nil
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return Ref{}, fmt.Errorf("store: create blob directory: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		// A concurrent Put of the same content may have won the race, which is a
		// success for us: the bytes are identical by construction.
		if _, statErr := os.Stat(final); statErr == nil {
			return ref, nil
		}
		return Ref{}, fmt.Errorf("store: commit blob: %w", err)
	}
	renamed = true

	// Blobs are immutable; make them read-only to the owning user so an
	// accidental write fails loudly instead of silently invalidating the digest.
	if err := os.Chmod(final, 0o400); err != nil {
		return Ref{}, fmt.Errorf("store: set blob permissions: %w", err)
	}
	return ref, nil
}

// Open returns a reader for a stored blob. The caller must close it.
//
// The result is an [io.ReadSeekCloser] so HTTP handlers can serve it with
// http.ServeContent, which needs Seek for range requests — that is what lets the
// PDF viewer in the reader UI fetch one page at a time.
func (s *Store) Open(digest string) (io.ReadSeekCloser, error) {
	path, err := s.pathFor(digest)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path comes from pathFor, which accepts only [0-9a-f]{64} and
	// joins it under the store root, so no caller-controlled traversal is
	// possible. See TestPathTraversalRejected.
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, digest)
		}
		return nil, fmt.Errorf("store: open blob %s: %w", digest, err)
	}
	return f, nil
}

// ReadAll returns the whole blob. Intended for small blobs; prefer [Store.Open]
// for anything that could be a large original.
func (s *Store) ReadAll(digest string) ([]byte, error) {
	f, err := s.Open(digest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// Stat reports the stored size.
func (s *Store) Stat(digest string) (Ref, error) {
	path, err := s.pathFor(digest)
	if err != nil {
		return Ref{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Ref{}, fmt.Errorf("%w: %s", ErrNotFound, digest)
		}
		return Ref{}, fmt.Errorf("store: stat blob %s: %w", digest, err)
	}
	return Ref{SHA256: digest, Size: fi.Size()}, nil
}

// Exists reports whether a blob is stored. An invalid digest is simply absent.
func (s *Store) Exists(digest string) bool {
	_, err := s.Stat(digest)
	return err == nil
}

// Delete removes a blob. Deleting an absent blob is not an error, so cleanup is
// idempotent.
func (s *Store) Delete(digest string) error {
	path, err := s.pathFor(digest)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: delete blob %s: %w", digest, err)
	}
	return nil
}

// Verify re-hashes a stored blob and checks it against its own name, detecting
// bit rot or a partial write. This is the payoff of content addressing.
func (s *Store) Verify(digest string) error {
	f, err := s.Open(digest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("store: read blob %s: %w", digest, err)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != digest {
		return fmt.Errorf("store: blob %s is corrupt: content hashes to %s", digest, got)
	}
	return nil
}

// pathFor maps a digest to its file path, validating the digest first.
//
// Validation is the security boundary: digests reach this function from request
// paths, and without the check a value like "../../../etc/passwd" would escape
// the store. Because a valid digest is only [0-9a-f]{64}, there is no way for a
// separator or a dot to survive.
func (s *Store) pathFor(digest string) (string, error) {
	if err := ValidDigest(digest); err != nil {
		return "", err
	}
	// Two levels of fan-out keep directories small: a flat layout with tens of
	// thousands of blobs makes directory listings and lookups slow on some
	// filesystems.
	return filepath.Join(s.root, digest[0:2], digest[2:4], digest), nil
}

// ValidDigest reports whether digest is a lowercase hex SHA-256.
func ValidDigest(digest string) error {
	if len(digest) != digestLen {
		return fmt.Errorf("%w: expected %d hex characters, got %d", ErrBadDigest, digestLen, len(digest))
	}
	for i := range digest {
		c := digest[i]
		valid := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !valid {
			return fmt.Errorf("%w: %q contains a character that is not lowercase hex", ErrBadDigest, digest)
		}
	}
	return nil
}

// Digest computes the digest of a byte slice, for callers that already hold the
// content in memory.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Root returns the store's root directory.
func (s *Store) Root() string { return s.root }

// Path returns the filesystem path of a stored blob.
//
// Handing out a path rather than a reader exists for one reason: the document
// pipeline shells out to poppler, and an external process needs a real file. It
// is safe because blobs are immutable and stored mode 0400 — the callee can read
// the bytes but cannot alter them, so the digest the filename asserts stays true.
//
// Prefer [Store.Open] for anything in-process. The digest is validated, so a
// caller-supplied value cannot escape the store root.
func (s *Store) Path(digest string) (string, error) {
	path, err := s.pathFor(digest)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, digest)
		}
		return "", fmt.Errorf("store: stat blob %s: %w", digest, err)
	}
	return path, nil
}

// CleanTemp removes leftover temporary uploads, which is how a crash mid-upload
// is reclaimed. Safe to call at startup.
func (s *Store) CleanTemp() error {
	dir := filepath.Join(s.root, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("store: read temp dir: %w", err)
	}

	var errs []error
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "upload-") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ctxReader makes a plain io.Reader cancellable, so an abandoned upload stops
// consuming disk and CPU as soon as the client disconnects.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
