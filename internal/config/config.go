// Package config loads manualbox configuration from defaults, an optional YAML
// file, and environment variables, in that order of increasing precedence.
//
// The guiding rule: a fresh install with no configuration at all must be fully
// usable. Text extraction, OCR, search, schedules, notifications, and the
// calendar feed all run locally with no keys. AI providers are opt-in, and an
// unset provider is a normal state rather than an error.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "MANUALBOX_"

// Config is the full application configuration.
type Config struct {
	Server    Server    `yaml:"server"`
	Content   Content   `yaml:"content"`
	Providers Providers `yaml:"providers"`
	Jobs      Jobs      `yaml:"jobs"`
	// LOG_ prefix: bare MANUALBOX_LEVEL and MANUALBOX_FORMAT would be ambiguous
	// (level of what?), and MANUALBOX_LOG_LEVEL is what anyone would guess.
	Log Log `yaml:"log" envPrefix:"LOG_"`
}

// Server holds HTTP and filesystem settings.
type Server struct {
	// Addr is the listen address.
	Addr string `yaml:"addr" env:"ADDR"`
	// BaseURL is the externally reachable origin. It is embedded in
	// notifications and in the ICS calendar feed, which calendar clients fetch
	// from outside the container, so a wrong value shows up as unreachable
	// links rather than as a startup failure.
	BaseURL string `yaml:"base_url" env:"BASE_URL"`
	// DataDir holds the SQLite database and the blob store.
	DataDir string `yaml:"data_dir" env:"DATA_DIR"`
	// TrustProxy makes the server honour X-Forwarded-For and X-Forwarded-Proto.
	// Only enable it behind a reverse proxy that overwrites those headers.
	TrustProxy bool `yaml:"trust_proxy" env:"TRUST_PROXY"`
	// ReadTimeout bounds reading a request. Uploads can be large and slow, so
	// this is generous by default.
	ReadTimeout time.Duration `yaml:"read_timeout" env:"READ_TIMEOUT"`
	// WriteTimeout bounds writing a response. Zero disables it, which the SSE
	// progress stream requires.
	WriteTimeout time.Duration `yaml:"write_timeout" env:"WRITE_TIMEOUT"`
	// MaxUploadBytes caps a single uploaded file.
	MaxUploadBytes int64 `yaml:"max_upload_bytes" env:"MAX_UPLOAD_BYTES"`
}

// Content describes the languages the household reads.
type Content struct {
	// Languages are the BCP-47 tags to translate into, most preferred first.
	// The first entry is the primary reading language and the default for
	// generated cheat sheets.
	Languages []string `yaml:"languages" env:"LANGUAGES" envSeparator:","`
	// OCRLanguages are the tesseract language codes to load. Empty means derive
	// them from Languages.
	OCRLanguages []string `yaml:"ocr_languages" env:"OCR_LANGUAGES" envSeparator:","`
}

// Providers configures the pluggable adapters. Every slot may be empty; an
// empty slot disables the corresponding feature rather than failing.
type Providers struct {
	Convert   Provider `yaml:"convert" envPrefix:"CONVERT_"`
	OCR       Provider `yaml:"ocr" envPrefix:"OCR_"`
	Translate Provider `yaml:"translate" envPrefix:"TRANSLATE_"`
	Extract   Provider `yaml:"extract" envPrefix:"EXTRACT_"`
}

// Provider identifies one adapter and its credentials.
type Provider struct {
	// Kind selects the adapter. Empty means the feature is disabled.
	Kind string `yaml:"kind" env:"KIND"`
	// Model is the model identifier, for adapters that take one.
	Model string `yaml:"model" env:"MODEL"`
	// APIKey authenticates against a hosted provider. Never logged.
	APIKey Secret `yaml:"api_key" env:"API_KEY"`
	// BaseURL overrides the provider endpoint. Required for self-hosted
	// OpenAI-compatible servers such as Ollama, vLLM, or LM Studio.
	BaseURL string `yaml:"base_url" env:"BASE_URL"`
	// Options carries adapter-specific settings without a schema change.
	Options map[string]string `yaml:"options" env:"OPTIONS"`
}

// Enabled reports whether an adapter is configured.
func (p Provider) Enabled() bool { return p.Kind != "" }

// Jobs tunes the background worker pool.
type Jobs struct {
	// Workers is the number of concurrent job workers.
	Workers int `yaml:"workers" env:"WORKERS"`
	// PollInterval is how often idle workers look for new work.
	PollInterval time.Duration `yaml:"poll_interval" env:"POLL_INTERVAL"`
	// LeaseDuration is how long a claimed job stays claimed. A worker that dies
	// without releasing its job has it reclaimed after this long.
	LeaseDuration time.Duration `yaml:"lease_duration" env:"LEASE_DURATION"`
	// MaxAttempts is how many times a failing job is retried.
	MaxAttempts int `yaml:"max_attempts" env:"MAX_ATTEMPTS"`
}

// Log configures structured logging.
type Log struct {
	// Level is one of debug, info, warn, error.
	Level string `yaml:"level" env:"LEVEL"`
	// Format is text or json.
	Format string `yaml:"format" env:"FORMAT"`
}

// Default returns the configuration used when nothing is set. It is a complete,
// working setup: local PDF text extraction and local OCR, no network, no keys.
func Default() Config {
	return Config{
		Server: Server{
			Addr:           ":7745",
			BaseURL:        "http://localhost:7745",
			DataDir:        "./data",
			TrustProxy:     false,
			ReadTimeout:    5 * time.Minute,
			WriteTimeout:   0,
			MaxUploadBytes: 512 << 20, // 512 MiB
		},
		Content: Content{
			Languages: []string{"en"},
		},
		Providers: Providers{
			// Local and free by default.
			Convert: Provider{Kind: "poppler"},
			OCR:     Provider{Kind: "tesseract"},
			// Opt-in: no default AI provider, no default cloud dependency.
			Translate: Provider{},
			Extract:   Provider{},
		},
		Jobs: Jobs{
			Workers:       2,
			PollInterval:  2 * time.Second,
			LeaseDuration: 15 * time.Minute,
			MaxAttempts:   3,
		},
		Log: Log{Level: "info", Format: "text"},
	}
}

// Load builds the configuration from defaults, then the YAML file at path if it
// is non-empty and exists, then environment variables.
//
// A path given explicitly must exist; the conventional ./config.yaml is
// optional. That distinction matters because silently ignoring a mistyped
// --config path is the kind of failure a user debugs for an hour.
func Load(path string) (Config, error) {
	cfg := Default()

	explicit := path != ""
	if !explicit {
		path = "config.yaml"
	}

	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path
	switch {
	case err == nil:
		// Decode strictly so a typo'd key is reported rather than ignored.
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if decErr := dec.Decode(&cfg); decErr != nil {
			return Config{}, fmt.Errorf("parse config file %s: %w", path, decErr)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// No config file is the normal case.
	default:
		return Config{}, fmt.Errorf("read config file %s: %w", path, err)
	}

	if err := env.ParseWithOptions(&cfg, env.Options{Prefix: EnvPrefix}); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}

	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

// normalize canonicalizes values that later code is allowed to assume are clean.
func (c *Config) normalize() error {
	abs, err := filepath.Abs(c.Server.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data_dir %q: %w", c.Server.DataDir, err)
	}
	c.Server.DataDir = abs
	c.Server.BaseURL = strings.TrimRight(c.Server.BaseURL, "/")
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))

	for i, tag := range c.Content.Languages {
		c.Content.Languages[i] = strings.TrimSpace(tag)
	}
	c.Content.Languages = dedupe(c.Content.Languages)

	if len(c.Content.OCRLanguages) == 0 {
		c.Content.OCRLanguages = tesseractCodes(c.Content.Languages)
	}
	return nil
}

// Validate reports every problem it finds at once, so a misconfigured install
// takes one restart to fix rather than one per mistake.
func (c Config) Validate() error {
	var errs []error

	if c.Server.Addr == "" {
		errs = append(errs, errors.New("server.addr must not be empty"))
	}
	if u, err := url.Parse(c.Server.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, fmt.Errorf("server.base_url %q must be an absolute URL like http://host:port", c.Server.BaseURL))
	}
	if c.Server.MaxUploadBytes <= 0 {
		errs = append(errs, errors.New("server.max_upload_bytes must be positive"))
	}

	if len(c.Content.Languages) == 0 {
		errs = append(errs, errors.New("content.languages must list at least one language"))
	}
	for _, tag := range c.Content.Languages {
		if _, err := language.Parse(tag); err != nil {
			errs = append(errs, fmt.Errorf("content.languages: %q is not a valid BCP-47 language tag", tag))
		}
	}

	if c.Jobs.Workers < 1 {
		errs = append(errs, errors.New("jobs.workers must be at least 1"))
	}
	if c.Jobs.PollInterval <= 0 {
		errs = append(errs, errors.New("jobs.poll_interval must be positive"))
	}
	if c.Jobs.LeaseDuration <= 0 {
		errs = append(errs, errors.New("jobs.lease_duration must be positive"))
	}
	if c.Jobs.MaxAttempts < 1 {
		errs = append(errs, errors.New("jobs.max_attempts must be at least 1"))
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q must be one of debug, info, warn, error", c.Log.Level))
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format %q must be text or json", c.Log.Format))
	}

	// Providers are validated for internal consistency only. Whether a named
	// adapter exists is the provider registry's business, and whether it works
	// is discovered when it is first used.
	for name, p := range map[string]Provider{
		"convert":   c.Providers.Convert,
		"ocr":       c.Providers.OCR,
		"translate": c.Providers.Translate,
		"extract":   c.Providers.Extract,
	} {
		if !p.Enabled() {
			continue
		}
		if p.BaseURL != "" {
			if u, err := url.Parse(p.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
				errs = append(errs, fmt.Errorf("providers.%s.base_url %q must be an absolute URL", name, p.BaseURL))
			}
		}
	}

	return errors.Join(errs...)
}

// PrimaryLanguage is the household's main reading language.
func (c Config) PrimaryLanguage() string {
	if len(c.Content.Languages) == 0 {
		return "en"
	}
	return c.Content.Languages[0]
}

// DBPath is the SQLite database location.
func (c Config) DBPath() string { return filepath.Join(c.Server.DataDir, "manualbox.db") }

// BlobDir is the content-addressed blob store location.
func (c Config) BlobDir() string { return filepath.Join(c.Server.DataDir, "blobs") }

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// tesseractISO3 maps the base of a BCP-47 tag to the ISO 639-2/T code tesseract
// names its trained-data files with. Only languages with a differing code need
// an entry; anything else falls through to the three-letter form the x/text
// library derives.
var tesseractISO3 = map[string]string{
	"zh": "chi_sim",
	"ko": "kor",
	"ja": "jpn",
}

// tesseractCodes converts reading languages into tesseract language codes,
// always including English because manuals routinely mix it in.
func tesseractCodes(tags []string) []string {
	out := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		tag, err := language.Parse(t)
		if err != nil {
			continue
		}
		base, _ := tag.Base()
		if code, ok := tesseractISO3[base.String()]; ok {
			out = append(out, code)
			continue
		}
		out = append(out, base.ISO3())
	}
	return dedupe(append(out, "eng"))
}
