package db

import "time"

// The schema stores timestamps as INTEGER Unix milliseconds in UTC. These
// helpers are the only place that representation is converted, so the choice
// stays reversible: changing the storage format means changing this file and the
// migrations, not every call site.

// Now returns the current time in the schema's representation.
func Now() int64 { return Millis(time.Now()) }

// Millis converts a time to stored form.
func Millis(t time.Time) int64 { return t.UTC().UnixMilli() }

// Time converts a stored value back to a time, in UTC.
func Time(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// MillisPtr converts an optional time to an optional stored value, for nullable
// columns such as finished_at.
func MillisPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := Millis(*t)
	return &ms
}

// TimePtr converts an optional stored value back to an optional time.
func TimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := Time(*ms)
	return &t
}
