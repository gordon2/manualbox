package id

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestNewFormat(t *testing.T) {
	got := New(Device)

	if !strings.HasPrefix(got, "dev_") {
		t.Errorf("New(Device) = %q, want a dev_ prefix", got)
	}
	if len(got) != len("dev_")+ulidLen {
		t.Errorf("New(Device) = %q, unexpected length %d", got, len(got))
	}
	if !Valid(Device, got) {
		t.Errorf("New(Device) = %q, which does not validate as a Device id", got)
	}
}

func TestNewIsUnique(t *testing.T) {
	// Same-millisecond collisions are the interesting case, so generate fast.
	const n = 10_000
	seen := make(map[string]struct{}, n)
	for range n {
		v := New(Document)
		if _, dup := seen[v]; dup {
			t.Fatalf("duplicate id generated: %s", v)
		}
		seen[v] = struct{}{}
	}
}

func TestValidRejectsWrongKind(t *testing.T) {
	device := New(Device)

	if Valid(Document, device) {
		t.Error("a device id must not validate as a document id — catching this at the edge is the point of the prefix")
	}
	if !Valid(Device, device) {
		t.Error("a device id should validate as a device id")
	}
}

func TestValidRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"dev",                                 // no separator
		"dev_",                                // no ULID
		"dev_tooshort",                        // wrong length
		"dev_01JQ8ZK3M4N5P6R7S8T9V0W1X",       // 25 chars
		"dev_01JQ8ZK3M4N5P6R7S8T9V0W1X2Z",     // 27 chars
		"dev_01JQ8ZK3M4N5P6R7S8T9V0W1XU",      // U is not in Crockford base32
		"01JQ8ZK3M4N5P6R7S8T9V0W1X2",          // bare ULID, no prefix
		"dev_01jq8zk3m4n5p6r7s8t9v0w1x2!",     // invalid character
		"../../etc/passwd",                    // path traversal attempt
		"dev_01JQ8ZK3M4N5P6R7S8T9V0W1X2 OR 1", // injection attempt
	} {
		if Valid(Device, bad) {
			t.Errorf("Valid(Device, %q) = true, want false", bad)
		}
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should have failed", bad)
		}
	}
}

func TestParseErrorsAreDescriptive(t *testing.T) {
	// These messages end up in 400 responses, so they should say what is wrong.
	for _, tc := range []struct{ in, want string }{
		{"nounderscore", "missing kind prefix"},
		{"dev_short", "26-character ULID"},
	} {
		_, _, err := Parse(tc.in)
		if err == nil {
			t.Fatalf("Parse(%q) should fail", tc.in)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) error = %q, want it to mention %q", tc.in, err, tc.want)
		}
	}
}

func TestTimeRoundTrip(t *testing.T) {
	before := time.Now().Add(-time.Second)
	got, err := Time(New(Job))
	if err != nil {
		t.Fatalf("Time: %v", err)
	}
	after := time.Now().Add(time.Second)

	if got.Before(before) || got.After(after) {
		t.Errorf("embedded time %v is outside the expected window [%v, %v]", got, before, after)
	}
}

func TestLexicalOrderMatchesCreationOrder(t *testing.T) {
	// This is the property that makes these usable as SQLite primary keys and
	// lets "newest first" work without a secondary index. Sleep past the ULID's
	// millisecond resolution so ordering is by timestamp, not just entropy.
	var ids []string
	for range 5 {
		ids = append(ids, New(Device))
		time.Sleep(2 * time.Millisecond)
	}

	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)

	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("lexical order does not match creation order:\n created: %v\n  sorted: %v", ids, sorted)
		}
	}
}

func TestKindPrefixesAreDistinctAndStable(t *testing.T) {
	// Prefixes are baked into stored rows and exported data, so a collision or a
	// later rename would be a migration. Guard the invariant here.
	kinds := []Kind{User, Session, Location, Device, Document, Asset, Plan, Occurrence, Job, Notifier, Term}

	seen := make(map[Kind]struct{}, len(kinds))
	for _, k := range kinds {
		if k == "" {
			t.Error("a kind prefix is empty")
		}
		if strings.Contains(string(k), "_") {
			t.Errorf("kind %q must not contain the separator", k)
		}
		if strings.ToLower(string(k)) != string(k) {
			t.Errorf("kind %q should be lowercase", k)
		}
		if _, dup := seen[k]; dup {
			t.Errorf("kind prefix %q is used twice", k)
		}
		seen[k] = struct{}{}
	}
}
