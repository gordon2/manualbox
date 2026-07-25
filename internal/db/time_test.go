package db

import (
	"testing"
	"time"
)

func TestMillisRoundTrip(t *testing.T) {
	// Millisecond precision is the documented resolution; anything finer is
	// expected to be truncated.
	want := time.Date(2026, 7, 25, 14, 30, 15, 123_000_000, time.UTC)

	if got := Time(Millis(want)); !got.Equal(want) {
		t.Errorf("round trip = %v, want %v", got, want)
	}
}

func TestMillisNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+5", 5*60*60)
	local := time.Date(2026, 7, 25, 14, 0, 0, 0, zone)

	got := Time(Millis(local))
	if !got.Equal(local) {
		t.Errorf("round trip lost the instant: got %v, want %v", got, local)
	}
	if got.Location() != time.UTC {
		t.Errorf("stored times must come back as UTC, got %v", got.Location())
	}
}

func TestNowIsCurrent(t *testing.T) {
	before := time.Now().Add(-time.Second)
	got := Time(Now())
	after := time.Now().Add(time.Second)

	if got.Before(before) || got.After(after) {
		t.Errorf("Now() = %v, outside [%v, %v]", got, before, after)
	}
}

func TestPointerHelpers(t *testing.T) {
	if MillisPtr(nil) != nil {
		t.Error("MillisPtr(nil) should be nil")
	}
	if TimePtr(nil) != nil {
		t.Error("TimePtr(nil) should be nil")
	}

	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := TimePtr(MillisPtr(&want))
	if got == nil {
		t.Fatal("round trip through pointers lost the value")
	}
	if !got.Equal(want) {
		t.Errorf("round trip = %v, want %v", *got, want)
	}
}
