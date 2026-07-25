package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	// A fresh install with zero configuration must boot. If this ever fails,
	// the out-of-the-box experience is broken.
	cfg := Default()
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize default: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must be valid, got: %v", err)
	}
}

func TestDefaultNeedsNoAIProvider(t *testing.T) {
	cfg := Default()

	// Local, free, offline adapters are on by default...
	if !cfg.Providers.Convert.Enabled() {
		t.Error("convert should default to a local adapter so PDFs work with no setup")
	}
	if !cfg.Providers.OCR.Enabled() {
		t.Error("ocr should default to a local adapter")
	}
	// ...and anything that could call out to a paid API is not.
	if cfg.Providers.Translate.Enabled() {
		t.Error("translate must be opt-in, not enabled by default")
	}
	if cfg.Providers.Extract.Enabled() {
		t.Error("extract must be opt-in, not enabled by default")
	}
}

func TestLoadNoFile(t *testing.T) {
	t.Chdir(t.TempDir()) // no config.yaml here

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("a missing conventional config file must be fine: %v", err)
	}
	if cfg.Server.Addr != ":7745" {
		t.Errorf("Addr = %q, want the default :7745", cfg.Server.Addr)
	}
}

func TestLoadExplicitMissingFileFails(t *testing.T) {
	// A mistyped --config path must be loud, not silently ignored.
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("explicitly requested config file that does not exist must be an error")
	}
}

func TestLoadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write(t, path, `
server:
  addr: ":9000"
  base_url: "https://manuals.example.com/"
content:
  languages: ["de", "uk", "en"]
providers:
  translate:
    kind: claude
    model: claude-sonnet-5
    api_key: sk-ant-secret
jobs:
  workers: 4
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Addr != ":9000" {
		t.Errorf("Addr = %q, want :9000", cfg.Server.Addr)
	}
	// Trailing slash must be trimmed so URL joining never doubles it.
	if cfg.Server.BaseURL != "https://manuals.example.com" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", cfg.Server.BaseURL)
	}
	if got, want := cfg.PrimaryLanguage(), "de"; got != want {
		t.Errorf("PrimaryLanguage = %q, want %q", got, want)
	}
	if cfg.Jobs.Workers != 4 {
		t.Errorf("Workers = %d, want 4", cfg.Jobs.Workers)
	}
	// Unset fields keep their defaults rather than becoming zero values.
	if cfg.Jobs.MaxAttempts != Default().Jobs.MaxAttempts {
		t.Errorf("MaxAttempts = %d, want the default %d", cfg.Jobs.MaxAttempts, Default().Jobs.MaxAttempts)
	}
	if cfg.Providers.Translate.APIKey.Reveal() != "sk-ant-secret" {
		t.Error("api key should load from YAML")
	}
}

func TestLoadYAMLUnknownKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// "listen" is a plausible guess for what the field is called, which is the
	// realistic way people get this wrong — not a letter transposition.
	write(t, path, "server:\n  listen: \":9000\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("an unknown config key must be reported, not silently ignored")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	write(t, path, "server:\n  addr: \":9000\"\nproviders:\n  translate:\n    kind: deepl\n")

	// Top-level field: nested struct inherits only the global prefix.
	t.Setenv("MANUALBOX_ADDR", ":9999")
	// Provider slots carry their own prefix so each slot is addressable.
	t.Setenv("MANUALBOX_TRANSLATE_KIND", "claude")
	t.Setenv("MANUALBOX_TRANSLATE_API_KEY", "sk-from-env")
	t.Setenv("MANUALBOX_LANGUAGES", "fr,es")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Addr != ":9999" {
		t.Errorf("Addr = %q, want env to win over YAML", cfg.Server.Addr)
	}
	if cfg.Providers.Translate.Kind != "claude" {
		t.Errorf("Translate.Kind = %q, want env to win over YAML", cfg.Providers.Translate.Kind)
	}
	if cfg.Providers.Translate.APIKey.Reveal() != "sk-from-env" {
		t.Errorf("api key should come from env")
	}
	if got := strings.Join(cfg.Content.Languages, ","); got != "fr,es" {
		t.Errorf("Languages = %q, want fr,es", got)
	}
}

// TestEnvVariableNames pins the exact environment variable each field reads.
//
// This exists because a wrong name fails silently: MANUALBOX_LOG_FORMAT=json was
// documented in the Dockerfile and compose file while the code actually read
// MANUALBOX_FORMAT, so JSON logging simply never happened and nothing complained.
// Any rename now shows up here rather than in someone's log aggregator.
func TestEnvVariableNames(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		env, value string
		check      func(Config) string
	}{
		{"MANUALBOX_ADDR", ":1234", func(c Config) string { return c.Server.Addr }},
		{"MANUALBOX_BASE_URL", "https://manuals.example.com", func(c Config) string { return c.Server.BaseURL }},
		{"MANUALBOX_LANGUAGES", "de", func(c Config) string { return c.Content.Languages[0] }},
		{"MANUALBOX_LOG_LEVEL", "debug", func(c Config) string { return c.Log.Level }},
		{"MANUALBOX_LOG_FORMAT", "json", func(c Config) string { return c.Log.Format }},
		{"MANUALBOX_TRANSLATE_KIND", "claude", func(c Config) string { return c.Providers.Translate.Kind }},
		{"MANUALBOX_TRANSLATE_MODEL", "claude-sonnet-5", func(c Config) string { return c.Providers.Translate.Model }},
		{"MANUALBOX_TRANSLATE_API_KEY", "sk-test", func(c Config) string { return c.Providers.Translate.APIKey.Reveal() }},
		{"MANUALBOX_EXTRACT_KIND", "claude", func(c Config) string { return c.Providers.Extract.Kind }},
		{"MANUALBOX_CONVERT_KIND", "vision", func(c Config) string { return c.Providers.Convert.Kind }},
		{"MANUALBOX_OCR_KIND", "vision", func(c Config) string { return c.Providers.OCR.Kind }},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := tc.check(cfg); got != tc.value {
				t.Errorf("%s=%q had no effect; field reads %q", tc.env, tc.value, got)
			}
		})
	}
}

func TestNormalizeDedupesAndAbsolutizes(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MANUALBOX_LANGUAGES", "de,de,en, uk ")
	t.Setenv("MANUALBOX_DATA_DIR", "./relative-data")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := strings.Join(cfg.Content.Languages, ","), "de,en,uk"; got != want {
		t.Errorf("Languages = %q, want %q (deduped and trimmed)", got, want)
	}
	if !filepath.IsAbs(cfg.Server.DataDir) {
		t.Errorf("DataDir = %q, want an absolute path", cfg.Server.DataDir)
	}
	if !strings.HasPrefix(cfg.DBPath(), cfg.Server.DataDir) {
		t.Errorf("DBPath %q should live under DataDir %q", cfg.DBPath(), cfg.Server.DataDir)
	}
}

func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	cfg := Default()
	cfg.Server.Addr = ""
	cfg.Server.BaseURL = "not-a-url"
	cfg.Content.Languages = []string{"klingon-nope"}
	cfg.Jobs.Workers = 0
	cfg.Log.Level = "loud"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	// One restart per fix is a bad experience; all problems should surface now.
	for _, want := range []string{"server.addr", "server.base_url", "content.languages", "jobs.workers", "log.level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestTesseractCodes(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"de", "uk"}, "deu,ukr,eng"},
		{[]string{"en"}, "eng"},
		{[]string{"zh"}, "chi_sim,eng"},
		{[]string{"pt-BR"}, "por,eng"},
		{[]string{"bogus-tag"}, "eng"},
	} {
		if got := strings.Join(tesseractCodes(tc.in), ","); got != tc.want {
			t.Errorf("tesseractCodes(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSecretDoesNotLeak covers every accidental path a credential could escape
// through. This is the test that matters most in this package.
func TestSecretDoesNotLeak(t *testing.T) {
	const want = "sk-ant-super-secret-value"
	s := Secret(want)

	// The Sprintf calls below are intentionally "redundant" — the whole point is
	// to prove the fmt verb path redacts, so they must go through fmt.
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"String", s.String()},
		{"fmt %s", fmt.Sprintf("%s", s)}, //nolint:gocritic,staticcheck // exercising the fmt path is the test
		{"fmt %v", fmt.Sprintf("%v", s)}, //nolint:gocritic,staticcheck // exercising the fmt path is the test
		{"fmt %q", fmt.Sprintf("%q", s)},
		{"fmt %#v", fmt.Sprintf("%#v", s)},
		{"fmt struct", fmt.Sprintf("%v", Provider{APIKey: s})},
		{"slog value", s.LogValue().String()},
	} {
		if strings.Contains(tc.got, want) {
			t.Errorf("%s leaked the secret: %s", tc.name, tc.got)
		}
	}

	if b, err := json.Marshal(Provider{APIKey: s}); err != nil {
		t.Fatalf("marshal: %v", err)
	} else if strings.Contains(string(b), want) {
		t.Errorf("JSON leaked the secret: %s", b)
	}

	// A real slog handler, since that is how it will actually be used.
	var buf strings.Builder
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("test", "key", s, "cfg", Provider{APIKey: s})
	if strings.Contains(buf.String(), want) {
		t.Errorf("slog handler leaked the secret: %s", buf.String())
	}

	// And it must still be readable where it is genuinely needed.
	if s.Reveal() != want {
		t.Error("Reveal must return the true value")
	}
	if !s.Set() || Secret("").Set() {
		t.Error("Set is wrong")
	}
	if Secret("").String() != "" {
		t.Error("an empty secret should render as empty, not as a redaction marker")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
