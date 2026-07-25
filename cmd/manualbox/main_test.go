package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut strings.Builder
	if err := run(context.Background(), []string{"version"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "manualbox") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestRunHelp(t *testing.T) {
	var out, errOut strings.Builder
	if err := run(context.Background(), []string{"help"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"serve", "doctor", "MANUALBOX_"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help should mention %q, got:\n%s", want, out.String())
		}
	}
}

func TestRunNoArgsAndUnknownCommand(t *testing.T) {
	for _, args := range [][]string{{}, {"frobnicate"}} {
		var out, errOut strings.Builder
		if err := run(context.Background(), args, &out, &errOut); err == nil {
			t.Errorf("run(%v) should fail", args)
		}
		// Usage goes to stderr on failure so it is visible without --help.
		if !strings.Contains(errOut.String(), "Usage:") {
			t.Errorf("run(%v) should print usage to stderr, got: %q", args, errOut.String())
		}
	}
}

func TestRunDoctor(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Chdir(t.TempDir()) // no stray config.yaml
	t.Setenv("MANUALBOX_DATA_DIR", data)
	t.Setenv("MANUALBOX_LANGUAGES", "de,en")

	var out, errOut strings.Builder
	if err := run(context.Background(), []string{"doctor"}, &out, &errOut); err != nil {
		t.Fatalf("doctor on a writable temp dir should succeed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Configuration", "Providers", "External tools", data, "de, en"} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output should contain %q, got:\n%s", want, got)
		}
	}
	// Local defaults are reported as active.
	for _, want := range []string{"poppler", "tesseract"} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor should report the default local adapter %q", want)
		}
	}
}

func TestRunDoctorRedactsAPIKey(t *testing.T) {
	const key = "sk-ant-do-not-print-me"
	t.Chdir(t.TempDir())
	t.Setenv("MANUALBOX_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("MANUALBOX_TRANSLATE_KIND", "claude")
	t.Setenv("MANUALBOX_TRANSLATE_API_KEY", key)

	var out, errOut strings.Builder
	if err := run(context.Background(), []string{"doctor"}, &out, &errOut); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	combined := out.String() + errOut.String()
	if strings.Contains(combined, key) {
		t.Errorf("doctor leaked the API key:\n%s", combined)
	}
	if !strings.Contains(out.String(), "api key set") {
		t.Error("doctor should confirm a key is present without printing it")
	}
}

func TestRunDoctorReportsConfiguredFeaturesAccurately(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MANUALBOX_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("MANUALBOX_TRANSLATE_KIND", "claude")
	t.Setenv("MANUALBOX_EXTRACT_KIND", "claude")

	var out, errOut strings.Builder
	if err := run(context.Background(), []string{"doctor"}, &out, &errOut); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	// With both AI slots configured, doctor must not claim anything is missing.
	if strings.Contains(out.String(), "No provider configured") {
		t.Errorf("doctor claimed a provider is unconfigured when both are set:\n%s", out.String())
	}
}

func TestRunDoctorBadConfigPathFails(t *testing.T) {
	var out, errOut strings.Builder
	err := run(context.Background(), []string{"doctor", "-config", filepath.Join(t.TempDir(), "missing.yaml")}, &out, &errOut)
	if err == nil {
		t.Fatal("an explicit missing config path should fail")
	}
}
