package logging

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/config"
)

func TestLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"debug":    slog.LevelDebug,
		"info":     slog.LevelInfo,
		"warn":     slog.LevelWarn,
		"error":    slog.LevelError,
		"":         slog.LevelInfo,
		"nonsense": slog.LevelInfo,
	} {
		if got := Level(in); got != want {
			t.Errorf("Level(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf strings.Builder
	log := New(config.Log{Level: "warn", Format: "text"}, &buf)

	log.Info("should be dropped")
	log.Warn("should appear")

	out := buf.String()
	if strings.Contains(out, "should be dropped") {
		t.Errorf("info record logged at warn level: %s", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("warn record missing: %s", out)
	}
}

func TestNewJSONFormat(t *testing.T) {
	var buf strings.Builder
	New(config.Log{Level: "info", Format: "json"}, &buf).Info("hello", "k", "v")

	out := buf.String()
	if !strings.HasPrefix(out, "{") || !strings.Contains(out, `"msg":"hello"`) {
		t.Errorf("expected JSON output, got: %s", out)
	}
}

func TestSourceOnlyAtDebug(t *testing.T) {
	var debugBuf, infoBuf strings.Builder
	New(config.Log{Level: "debug", Format: "json"}, &debugBuf).Debug("m")
	New(config.Log{Level: "info", Format: "json"}, &infoBuf).Info("m")

	if !strings.Contains(debugBuf.String(), `"source"`) {
		t.Errorf("debug level should include source: %s", debugBuf.String())
	}
	if strings.Contains(infoBuf.String(), `"source"`) {
		t.Errorf("info level should not include source: %s", infoBuf.String())
	}
}

func TestDiscard(t *testing.T) {
	// Should not panic and should swallow everything.
	Discard().Error("dropped", "key", "value")
}
