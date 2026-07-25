package config

import (
	"encoding/json"
	"log/slog"
)

// Secret is a string that refuses to reveal itself through any of the
// incidental paths a value normally leaks by: fmt verbs, slog attributes, JSON
// responses, and YAML round-trips. Read the real value with [Secret.Reveal],
// which is deliberately noisy at the call site.
//
// API keys reaching a log aggregator or an error report is the single most
// likely way this application could leak a user's credential, so the default
// rendering is redacted and revealing is the explicit, greppable exception.
type Secret string

const redacted = "«redacted»"

// String implements [fmt.Stringer], covering %s, %v, and Print-family calls.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// GoString implements [fmt.GoStringer], covering the %#v verb.
func (s Secret) GoString() string { return s.String() }

// LogValue implements [slog.LogValuer] so structured log attributes redact too.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON keeps secrets out of API responses and error payloads.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// MarshalYAML keeps secrets out of any config dump.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// Reveal returns the underlying value. Call it only where the secret is
// actually used, never to move it into another string.
func (s Secret) Reveal() string { return string(s) }

// Set reports whether a value is present.
func (s Secret) Set() bool { return s != "" }
