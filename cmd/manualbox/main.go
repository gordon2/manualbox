// Command manualbox is the manualbox server and its operational commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/logging"
)

// Set via -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	// os.Exit is confined to this one line so that every deferred cleanup in
	// exitCode — signal handler teardown today, database and worker shutdown
	// once serve is wired up — actually runs before the process ends.
	os.Exit(exitCode())
}

func exitCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "manualbox: %v\n", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("no command given")
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(ctx, rest, stdout, stderr)
	case "doctor":
		return cmdDoctor(ctx, rest, stdout)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "manualbox %s (%s)\n", version, commit)
		return nil
	case "help", "--help", "-h":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `manualbox — your household's manuals, searchable and in your language

Usage:
  manualbox <command> [flags]

Commands:
  serve      Run the web server and background workers
  doctor     Report configuration and which optional tools are available
  version    Print the version
  help       Show this help

Flags (serve, doctor):
  -config <path>   Path to a YAML config file (default: ./config.yaml if present)

Configuration comes from defaults, then the config file, then MANUALBOX_*
environment variables. Run "manualbox doctor" to see what was resolved.
`)
}

// loadConfig applies the -config flag shared by serve and doctor.
func loadConfig(args []string, stderr io.Writer, name string) (config.Config, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "path to a YAML config file")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, err
	}
	return config.Load(*path)
}

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := loadConfig(args, stderr, "serve")
	if err != nil {
		return err
	}

	if err := ensureDataDir(cfg); err != nil {
		return err
	}

	log := logging.New(cfg.Log, stdout)
	log.Info("starting manualbox",
		"version", version,
		"commit", commit,
		"addr", cfg.Server.Addr,
		"data_dir", cfg.Server.DataDir,
		"languages", cfg.Content.Languages,
	)

	// TODO(M0): open the database, run migrations, start the job workers, and
	// serve the API. Wired up in the "HTTP API skeleton" step.
	_ = ctx
	return errors.New("serve is not wired up yet — the HTTP server lands in the next M0 step")
}

func cmdDoctor(ctx context.Context, args []string, stdout io.Writer) error {
	cfg, err := loadConfig(args, stdout, "doctor")
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "manualbox %s (%s)\n\n", version, commit)

	// Each section gets its own tabwriter so column widths are computed per
	// section — long absolute paths in one table must not stretch the others.
	section := func(title string, body func(w io.Writer)) {
		fmt.Fprintf(stdout, "%s\n", title)
		tw := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
		body(tw)
		tw.Flush()
		fmt.Fprintln(stdout)
	}

	dataDirNote, dataDirOK := "writable", true
	if err := ensureDataDir(cfg); err != nil {
		dataDirNote, dataDirOK = "NOT WRITABLE: "+err.Error(), false
	}

	section("Configuration", func(w io.Writer) {
		fmt.Fprintf(w, "  data dir\t%s\t%s\n", cfg.Server.DataDir, dataDirNote)
		fmt.Fprintf(w, "  database\t%s\t\n", cfg.DBPath())
		fmt.Fprintf(w, "  blobs\t%s\t\n", cfg.BlobDir())
		fmt.Fprintf(w, "  listen\t%s\t\n", cfg.Server.Addr)
		fmt.Fprintf(w, "  base url\t%s\t\n", cfg.Server.BaseURL)
		fmt.Fprintf(w, "  languages\t%s\t\n", join(cfg.Content.Languages))
		fmt.Fprintf(w, "  ocr languages\t%s\t\n", join(cfg.Content.OCRLanguages))
	})

	section("Providers", func(w io.Writer) {
		for _, slot := range []struct {
			name     string
			p        config.Provider
			disabled string
		}{
			{"convert", cfg.Providers.Convert, "document conversion unavailable"},
			{"ocr", cfg.Providers.OCR, "OCR disabled"},
			{"translate", cfg.Providers.Translate, "translation disabled"},
			{"extract", cfg.Providers.Extract, "maintenance extraction disabled"},
		} {
			if !slot.p.Enabled() {
				fmt.Fprintf(w, "  %s\t(not configured)\t%s\n", slot.name, slot.disabled)
				continue
			}
			detail := slot.p.Kind
			if slot.p.Model != "" {
				detail += " / " + slot.p.Model
			}
			var notes []string
			if slot.p.APIKey.Set() {
				notes = append(notes, "api key set")
			}
			if slot.p.BaseURL != "" {
				notes = append(notes, slot.p.BaseURL)
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", slot.name, detail, join(notes))
		}
	})

	statuses := extern.ProbeAll(ctx)
	available := 0
	section("External tools", func(w io.Writer) {
		for _, s := range statuses {
			if s.Found {
				available++
				fmt.Fprintf(w, "  ok\t%s %s\t%s\n", s.Name, s.Version, s.Purpose)
				continue
			}
			fmt.Fprintf(w, "  --\t%s (missing)\t%s\n", s.Name, s.Purpose)
			fmt.Fprintf(w, "  \t\tinstall: %s\n", s.InstallHint())
		}
	})

	fmt.Fprintf(stdout, "%d of %d optional external tools available.\n", available, len(statuses))
	if available < len(statuses) {
		fmt.Fprintln(stdout, "Missing tools only disable the features that need them; manualbox still runs.")
	}
	// Name exactly which features are off, rather than implying nothing is set up.
	var off []string
	if !cfg.Providers.Translate.Enabled() {
		off = append(off, "translation")
	}
	if !cfg.Providers.Extract.Enabled() {
		off = append(off, "maintenance extraction")
	}
	if len(off) > 0 {
		fmt.Fprintf(stdout, "No provider configured for: %s. Everything else works; set one to enable %s.\n",
			join(off), pluralize(len(off), "it", "them"))
	}

	if !dataDirOK {
		return errors.New("data directory is not writable")
	}
	return nil
}

// ensureDataDir creates the data directory tree and verifies it is writable,
// which is the one filesystem problem worth failing fast on.
func ensureDataDir(cfg config.Config) error {
	for _, dir := range []string{cfg.Server.DataDir, cfg.BlobDir()} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	probe := filepath.Join(cfg.Server.DataDir, ".write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("data directory %s is not writable: %w", cfg.Server.DataDir, err)
	}
	return os.Remove(probe)
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
