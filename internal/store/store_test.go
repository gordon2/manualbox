package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestPutPreservesBytesExactly covers the store's central promise: whatever the
// user uploaded comes back identical, forever. Everything else in manualbox is
// derived and regenerable; this is not.
func TestPutPreservesBytesExactly(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x00}},
		{"text", []byte("Entkalken Sie das Gerät alle 3 Monate.")},
		{"all byte values", allBytes()},
		{"pdf-like header", []byte("%PDF-1.7\x00\x01\x02binary\xff\xfe")},
		{"large", randomBytes(t, 3<<20)}, // 3 MiB, past any buffer boundary
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := s.Put(ctx, bytes.NewReader(tc.content))
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			if ref.Size != int64(len(tc.content)) {
				t.Errorf("Size = %d, want %d", ref.Size, len(tc.content))
			}
			if ref.SHA256 != Digest(tc.content) {
				t.Errorf("digest = %s, want %s", ref.SHA256, Digest(tc.content))
			}

			got, err := s.ReadAll(ref.SHA256)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if !bytes.Equal(got, tc.content) {
				t.Errorf("round trip changed the bytes: got %d bytes, want %d", len(got), len(tc.content))
			}
		})
	}
}

func TestPutDeduplicates(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	content := []byte("the same warranty card attached to two devices")

	first, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := s.Put(ctx, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}

	if first != second {
		t.Errorf("identical content produced different refs: %+v vs %+v", first, second)
	}
	if n := countBlobs(t, s); n != 1 {
		t.Errorf("%d files on disk, want 1 — identical uploads must not be stored twice", n)
	}
}

func TestPutIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	s := newStore(t)

	if _, err := s.Put(context.Background(), bytes.NewReader([]byte("done"))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(s.Root(), "tmp"))
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d temp files left behind after a successful Put", len(entries))
	}
}

func TestPutFailureLeavesNoBlob(t *testing.T) {
	s := newStore(t)
	wantErr := errors.New("disk fell over")

	_, err := s.Put(context.Background(), io.MultiReader(
		bytes.NewReader([]byte("partial content")),
		errReader{wantErr},
	))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Put should surface the read error, got %v", err)
	}

	// A failed upload must leave nothing at all: no blob, and no temp file that
	// would leak disk on every retry.
	if n := countBlobs(t, s); n != 0 {
		t.Errorf("%d blobs stored after a failed Put", n)
	}
	entries, err := os.ReadDir(filepath.Join(s.Root(), "tmp"))
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d temp files left behind after a failed Put", len(entries))
	}
}

func TestPutRespectsContextCancellation(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Put(ctx, bytes.NewReader([]byte("abandoned upload"))); !errors.Is(err, context.Canceled) {
		t.Errorf("Put with a cancelled context should fail with context.Canceled, got %v", err)
	}
}

// TestPathTraversalRejected is the security test for this package: digests
// arrive from request paths, and anything other than hex must be refused before
// it becomes a filesystem path.
func TestPathTraversalRejected(t *testing.T) {
	s := newStore(t)

	for _, bad := range []string{
		"",
		"../../../etc/passwd",
		"..",
		"/etc/passwd",
		strings.Repeat("a", 63), // too short
		strings.Repeat("a", 65), // too long
		strings.Repeat("A", 64), // uppercase
		strings.Repeat("g", 64), // not hex
		strings.Repeat("a", 32) + "/" + strings.Repeat("b", 31),
		strings.Repeat("a", 63) + "\x00",
		"aa/" + strings.Repeat("b", 60),
	} {
		if _, err := s.Open(bad); !errors.Is(err, ErrBadDigest) {
			t.Errorf("Open(%q) should fail with ErrBadDigest, got %v", truncate(bad), err)
		}
		if _, err := s.Stat(bad); !errors.Is(err, ErrBadDigest) {
			t.Errorf("Stat(%q) should fail with ErrBadDigest, got %v", truncate(bad), err)
		}
		if err := s.Delete(bad); !errors.Is(err, ErrBadDigest) {
			t.Errorf("Delete(%q) should fail with ErrBadDigest, got %v", truncate(bad), err)
		}
		if s.Exists(bad) {
			t.Errorf("Exists(%q) should be false", truncate(bad))
		}
	}
}

func TestBlobsStayInsideRoot(t *testing.T) {
	s := newStore(t)

	ref, err := s.Put(context.Background(), bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	path, err := s.pathFor(ref.SHA256)
	if err != nil {
		t.Fatalf("pathFor: %v", err)
	}

	rel, err := filepath.Rel(s.Root(), path)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("blob path %q escapes the store root %q", path, s.Root())
	}
}

func TestOpenMissingBlob(t *testing.T) {
	s := newStore(t)
	absent := Digest([]byte("never stored"))

	if _, err := s.Open(absent); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open of an absent blob should be ErrNotFound, got %v", err)
	}
	if _, err := s.Stat(absent); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat of an absent blob should be ErrNotFound, got %v", err)
	}
	if s.Exists(absent) {
		t.Error("Exists should be false for an absent blob")
	}
}

func TestOpenIsSeekable(t *testing.T) {
	s := newStore(t)
	content := []byte("0123456789")

	ref, err := s.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	f, err := s.Open(ref.SHA256)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	// Range requests need Seek; without it the reader UI cannot fetch one page
	// of a large PDF at a time.
	if _, err := f.Seek(5, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll after Seek: %v", err)
	}
	if string(rest) != "56789" {
		t.Errorf("after seeking to 5, read %q, want %q", rest, "56789")
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	s := newStore(t)
	ref, err := s.Put(context.Background(), bytes.NewReader([]byte("temporary")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := s.Delete(ref.SHA256); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s.Exists(ref.SHA256) {
		t.Error("blob still present after Delete")
	}
	// Deleting again must not error, so cleanup paths can be careless.
	if err := s.Delete(ref.SHA256); err != nil {
		t.Errorf("second Delete should be a no-op, got %v", err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	s := newStore(t)
	ref, err := s.Put(context.Background(), bytes.NewReader([]byte("original content")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := s.Verify(ref.SHA256); err != nil {
		t.Fatalf("Verify on an intact blob: %v", err)
	}

	// Simulate bit rot. Blobs are stored read-only, so widen permissions first —
	// which is itself evidence the read-only guard is in place.
	path, _ := s.pathFor(ref.SHA256)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered content!"), 0o600); err != nil {
		t.Fatalf("corrupt blob: %v", err)
	}

	err = s.Verify(ref.SHA256)
	if err == nil {
		t.Fatal("Verify should detect a corrupted blob")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("error should say the blob is corrupt, got: %v", err)
	}
}

func TestStoredBlobsAreReadOnly(t *testing.T) {
	s := newStore(t)
	ref, err := s.Put(context.Background(), bytes.NewReader([]byte("immutable")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	path, _ := s.pathFor(ref.SHA256)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Blobs are immutable by contract; the filesystem should enforce it so a bug
	// fails loudly rather than silently invalidating a digest.
	if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("blob mode is %v, want no write bits", fi.Mode().Perm())
	}
}

// TestConcurrentPutSameContent exercises the rename race: several uploads of
// identical bytes landing at once must all succeed and produce one file.
func TestConcurrentPutSameContent(t *testing.T) {
	s := newStore(t)
	content := randomBytes(t, 256<<10)
	want := Digest(content)

	const n = 16
	refs := make([]Ref, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs[i], errs[i] = s.Put(context.Background(), bytes.NewReader(content))
		}()
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Errorf("Put %d failed: %v", i, errs[i])
			continue
		}
		if refs[i].SHA256 != want {
			t.Errorf("Put %d digest = %s, want %s", i, refs[i].SHA256, want)
		}
	}
	if got := countBlobs(t, s); got != 1 {
		t.Errorf("%d files on disk after %d concurrent identical uploads, want 1", got, n)
	}
	if err := s.Verify(want); err != nil {
		t.Errorf("blob is not intact after concurrent writes: %v", err)
	}
}

func TestConcurrentPutDistinctContent(t *testing.T) {
	s := newStore(t)
	const n = 16

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Put(context.Background(), bytes.NewReader(randomBytes(t, 8<<10)))
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Put %d: %v", i, err)
		}
	}
	if got := countBlobs(t, s); got != n {
		t.Errorf("%d distinct blobs stored, want %d", got, n)
	}
}

func TestCleanTemp(t *testing.T) {
	s := newStore(t)

	// Simulate two crashed uploads plus an unrelated file.
	tmpDir := filepath.Join(s.Root(), "tmp")
	for _, name := range []string{"upload-abc", "upload-def"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("partial"), 0o600); err != nil {
			t.Fatalf("seed temp file: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "keep-me"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	if err := s.CleanTemp(); err != nil {
		t.Fatalf("CleanTemp: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "keep-me" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp dir contains %v, want only [keep-me]", names)
	}
}

func TestNewRequiresDir(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("New(\"\") should fail")
	}
}

func TestFanOutLayout(t *testing.T) {
	s := newStore(t)
	ref, err := s.Put(context.Background(), bytes.NewReader([]byte("layout check")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	path, _ := s.pathFor(ref.SHA256)
	rel, _ := filepath.Rel(s.Root(), path)
	parts := strings.Split(rel, string(filepath.Separator))

	// Two levels of fan-out keep directories small as the store grows.
	if len(parts) != 3 {
		t.Fatalf("layout is %v, want <aa>/<bb>/<digest>", parts)
	}
	if parts[0] != ref.SHA256[0:2] || parts[1] != ref.SHA256[2:4] || parts[2] != ref.SHA256 {
		t.Errorf("layout %v does not match the digest %s", parts, ref.SHA256)
	}
}

func TestValidDigest(t *testing.T) {
	if err := ValidDigest(Digest([]byte("x"))); err != nil {
		t.Errorf("a real digest should validate, got %v", err)
	}
	if err := ValidDigest("nope"); !errors.Is(err, ErrBadDigest) {
		t.Errorf("want ErrBadDigest, got %v", err)
	}
}

func countBlobs(t *testing.T, s *Store) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(s.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip the staging area; only committed blobs count.
		if d.IsDir() {
			if path != s.Root() && filepath.Base(path) == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk store: %v", err)
	}
	return count
}

func allBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func truncate(s string) string {
	if len(s) > 30 {
		return s[:30] + "..."
	}
	return s
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
