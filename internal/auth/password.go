package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters.
//
// These are the OWASP minimum recommendation (19 MiB, 2 iterations, 1 lane)
// rather than something heavier, because manualbox is expected to run on a NAS or
// a Raspberry Pi where a 64 MiB-per-login hash would be painful. Argon2id at
// these settings still costs tens of milliseconds per attempt, which is what
// makes password guessing impractical.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned when a stored hash cannot be parsed.
var ErrInvalidHash = errors.New("password hash is malformed")

// hashParams are the parameters recovered from an encoded hash, so a hash created
// under older settings can still be verified.
type hashParams struct {
	memory      uint32
	time        uint32
	parallelism uint8
}

// argonParallelism picks the number of lanes, bounded so a many-core host does
// not make hashes unverifiable on a small one.
func argonParallelism() uint8 {
	return uint8(min(max(runtime.NumCPU(), 1), 4)) //nolint:gosec // bounded to 1..4
}

// HashPassword returns a PHC-encoded argon2id hash.
//
// The encoding carries the parameters and salt alongside the digest, so the cost
// settings above can be raised later without invalidating existing passwords.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	lanes := argonParallelism()
	digest := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, lanes, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, lanes,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// VerifyPassword reports whether password matches the encoded hash, and whether
// the hash should be recomputed because it used weaker parameters than current.
//
// The comparison is constant-time: a timing difference between "wrong on the
// first byte" and "wrong on the last" would leak the digest one byte at a time.
func VerifyPassword(encoded, password string) (ok, needsRehash bool, err error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}

	// decodeHash bounds the digest length, so this conversion cannot overflow.
	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.parallelism, uint32(len(want))) //nolint:gosec // length validated in decodeHash
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}

	stale := params.memory < argonMemory || params.time < argonTime
	return true, stale, nil
}

// Bounds on the decoded components of a stored hash. These keep a malformed or
// hostile hash from driving an absurd allocation, and make the digest length safe
// to convert to uint32 for argon2.
const (
	minDigestLen = 16
	maxDigestLen = 64
	maxSaltLen   = 64
)

// decodeHash parses a PHC-encoded argon2id hash.
func decodeHash(encoded string) (params hashParams, salt, digest []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, digest
	if len(parts) != 6 || parts[0] != "" {
		return hashParams{}, nil, nil, fmt.Errorf("%w: expected 5 fields, got %d", ErrInvalidHash, len(parts)-1)
	}
	if parts[1] != "argon2id" {
		return hashParams{}, nil, nil, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("%w: unreadable version: %w", ErrInvalidHash, err)
	}
	if version != argon2.Version {
		return hashParams{}, nil, nil, fmt.Errorf("%w: unsupported argon2 version %d", ErrInvalidHash, version)
	}

	var p hashParams // populated below, returned as the named result
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.parallelism); err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("%w: unreadable parameters: %w", ErrInvalidHash, err)
	}
	if p.memory == 0 || p.time == 0 || p.parallelism == 0 {
		return hashParams{}, nil, nil, fmt.Errorf("%w: parameters must be non-zero", ErrInvalidHash)
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("%w: unreadable salt: %w", ErrInvalidHash, err)
	}
	digest, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("%w: unreadable digest: %w", ErrInvalidHash, err)
	}
	if len(salt) == 0 || len(salt) > maxSaltLen {
		return hashParams{}, nil, nil, fmt.Errorf("%w: salt length %d is out of range", ErrInvalidHash, len(salt))
	}
	if len(digest) < minDigestLen || len(digest) > maxDigestLen {
		return hashParams{}, nil, nil, fmt.Errorf("%w: digest length %d is out of range", ErrInvalidHash, len(digest))
	}

	return p, salt, digest, nil
}

// dummyHash is verified against when an account does not exist, so that a
// missing email and a wrong password take the same amount of time. Without it,
// response latency reveals which addresses are registered.
var dummyHash string

func init() {
	h, err := HashPassword("manualbox-timing-equalization-placeholder")
	if err != nil {
		panic("auth: cannot hash the timing placeholder: " + err.Error())
	}
	dummyHash = h
}
