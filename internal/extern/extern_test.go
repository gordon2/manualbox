package extern

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// goTool is a stand-in for a real external tool: it is guaranteed present in
// any environment that can run this test, so the probe path is exercised
// without depending on poppler or tesseract being installed.
var goTool = Tool{
	Name:        "go",
	Purpose:     "stand in for a real tool in tests",
	VersionArgs: []string{"version"},
	Install:     map[string]string{"default": "https://go.dev/dl/"},
}

var missingTool = Tool{
	Name:    "manualbox-definitely-not-installed",
	Purpose: "be absent",
	Install: map[string]string{"default": "you cannot install this"},
}

func TestProbeFound(t *testing.T) {
	s := Probe(context.Background(), goTool)

	if !s.Found {
		t.Fatalf("go should be on PATH, got err: %v", s.Err)
	}
	if s.Path == "" {
		t.Error("Path should be set when found")
	}
	if s.Err != nil {
		t.Errorf("Err should be nil when found, got %v", s.Err)
	}
	// `go version` prints e.g. "go version go1.25.7 darwin/arm64".
	if !strings.HasPrefix(s.Version, "1.") {
		t.Errorf("Version = %q, want a dotted version extracted from the banner", s.Version)
	}
}

func TestProbeMissing(t *testing.T) {
	s := Probe(context.Background(), missingTool)

	if s.Found {
		t.Fatal("tool should not be found")
	}
	if !errors.Is(s.Err, ErrNotFound) {
		t.Errorf("Err should wrap ErrNotFound, got %v", s.Err)
	}
	if s.Path != "" || s.Version != "" {
		t.Error("Path and Version should stay empty when not found")
	}
}

func TestRequireMissingErrorIsActionable(t *testing.T) {
	_, err := Require(missingTool)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("should wrap ErrNotFound, got %v", err)
	}
	// The whole point of this error is that a user can act on it, so it must
	// carry the name, the reason, and the fix.
	msg := err.Error()
	for _, want := range []string{missingTool.Name, "be absent", "you cannot install this"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should contain %q, got: %s", want, msg)
		}
	}
}

func TestRequireCaches(t *testing.T) {
	first, err := Require(goTool)
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	second, err := Require(goTool)
	if err != nil {
		t.Fatalf("Require (cached): %v", err)
	}
	if first != second {
		t.Errorf("cached lookup returned a different path: %q vs %q", first, second)
	}
	if !Available(goTool) {
		t.Error("Available should be true for go")
	}
	if Available(missingTool) {
		t.Error("Available should be false for a missing tool")
	}
}

func TestProbeAllCoversEveryKnownTool(t *testing.T) {
	got := ProbeAll(context.Background())

	if len(got) != len(All()) {
		t.Fatalf("ProbeAll returned %d statuses, want %d", len(got), len(All()))
	}
	// Results must line up with All() order and be fully populated, since the
	// concurrent fill-by-index is easy to get subtly wrong.
	for i, want := range All() {
		if got[i].Name != want.Name {
			t.Errorf("status %d is %q, want %q — ProbeAll must preserve All() order", i, got[i].Name, want.Name)
		}
		if got[i].Purpose == "" {
			t.Errorf("status %d (%s) has no Purpose; the slot was never written", i, got[i].Name)
		}
	}
}

func TestInstallHintFallsBackToDefault(t *testing.T) {
	s := Status{Tool: Tool{Install: map[string]string{"default": "fallback"}}}
	if got := s.InstallHint(); got != "fallback" {
		t.Errorf("InstallHint = %q, want the default entry", got)
	}

	s = Status{Tool: Tool{Install: map[string]string{runtime.GOOS: "specific", "default": "fallback"}}}
	if got := s.InstallHint(); got != "specific" {
		t.Errorf("InstallHint = %q, want the platform-specific entry", got)
	}
}

func TestEveryKnownToolHasHelpfulMetadata(t *testing.T) {
	// These strings end up in front of a user who is stuck, so they are part of
	// the contract rather than decoration.
	for _, tool := range All() {
		if tool.Purpose == "" {
			t.Errorf("%s: Purpose must explain what the tool is for", tool.Name)
		}
		if tool.Install["default"] == "" {
			t.Errorf("%s: Install needs a default hint for unknown platforms", tool.Name)
		}
		for _, os := range []string{"darwin", "linux"} {
			if tool.Install[os] == "" {
				t.Errorf("%s: Install needs a %s hint", tool.Name, os)
			}
		}
	}
}
