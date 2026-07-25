// Package id generates prefixed, time-sortable identifiers.
//
// An identifier looks like "dev_01JQ8ZK3M4N5P6R7S8T9V0W1X2": a short kind prefix
// and a ULID. Three properties earn the format its keep:
//
//   - The prefix makes a bare ID self-describing in a log line, a URL, or a
//     support question, and lets a mistyped foreign key be caught immediately
//     rather than joining to nothing.
//   - The ULID's leading 48 bits are a millisecond timestamp, so IDs sort
//     chronologically. As a SQLite primary key that means inserts append to the
//     B-tree instead of scattering through it, and "most recent first" needs no
//     separate index.
//   - It is still opaque enough to hand out in a URL without leaking a count,
//     which sequential integers do.
package id

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Kind is the type prefix of an identifier.
type Kind string

// Kinds for every entity that gets an identifier. Prefixes are three or four
// characters, lowercase, and never change once released: they are embedded in
// stored rows and exported data forever.
const (
	User       Kind = "usr"
	Session    Kind = "ses"
	Location   Kind = "loc"
	Device     Kind = "dev"
	Document   Kind = "doc"
	Asset      Kind = "ast"
	Plan       Kind = "pln"
	Occurrence Kind = "occ"
	Job        Kind = "job"
	Notifier   Kind = "ntf"
	Term       Kind = "trm"
)

// ulidLen is the length of a canonical Base32-encoded ULID.
const ulidLen = 26

// New returns a fresh identifier of the given kind.
//
// Entropy comes from crypto/rand rather than the package default, because these
// identifiers appear in URLs and an attacker should not be able to walk them.
func New(k Kind) string {
	return string(k) + "_" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// Parse splits an identifier into its kind and ULID, validating both.
func Parse(s string) (Kind, ulid.ULID, error) {
	prefix, rest, found := strings.Cut(s, "_")
	if !found {
		return "", ulid.ULID{}, fmt.Errorf("id %q: missing kind prefix", s)
	}
	if len(rest) != ulidLen {
		return "", ulid.ULID{}, fmt.Errorf("id %q: expected a %d-character ULID, got %d characters", s, ulidLen, len(rest))
	}
	u, err := ulid.ParseStrict(rest)
	if err != nil {
		return "", ulid.ULID{}, fmt.Errorf("id %q: %w", s, err)
	}
	return Kind(prefix), u, nil
}

// Valid reports whether s is a well-formed identifier of the given kind.
//
// Use this on any identifier arriving from a request path or body: it turns a
// wrong-kind reference into a 400 at the edge instead of an empty query result
// deeper in, which is much harder to diagnose.
func Valid(k Kind, s string) bool {
	got, _, err := Parse(s)
	return err == nil && got == k
}

// Time returns the creation timestamp encoded in an identifier. Handy for
// debugging and for ordering without a separate column, though stored rows keep
// their own created_at so the timestamp is never load-bearing.
func Time(s string) (time.Time, error) {
	_, u, err := Parse(s)
	if err != nil {
		return time.Time{}, err
	}
	return ulid.Time(u.Time()), nil
}
