// Package extern discovers the optional external command-line tools manualbox
// can use: poppler for PDF text, structure, and page rendering, and tesseract
// for OCR.
//
// Every tool here is optional. When one is missing the feature that needs it is
// unavailable and says so, but the application still starts and everything else
// still works. That is why probing is a first-class package rather than a
// startup assertion — the answer to "is this installed?" has to be reportable
// to the user, not fatal.
package extern

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// probeTimeout bounds a single version probe. A hung binary must not hang boot.
const probeTimeout = 5 * time.Second

// Tool describes an external binary manualbox knows how to use.
type Tool struct {
	// Name is the executable name looked up on PATH.
	Name string
	// Purpose is a short phrase for the doctor output.
	Purpose string
	// VersionArgs produce a version banner. Several of these tools print it to
	// stderr and exit non-zero, so both streams are captured and the exit code
	// is ignored when a version can still be parsed.
	VersionArgs []string
	// Install maps GOOS to an install hint.
	Install map[string]string
}

// Status is the result of probing a [Tool].
type Status struct {
	Tool
	// Found reports whether the binary is on PATH.
	Found bool
	// Path is the resolved absolute path when found.
	Path string
	// Version is the parsed version string, empty if it could not be read.
	Version string
	// Err explains why probing failed, if it did.
	Err error
}

// InstallHint returns the hint for the current platform.
func (s Status) InstallHint() string {
	if h, ok := s.Install[runtime.GOOS]; ok {
		return h
	}
	return s.Install["default"]
}

// Well-known tools. Each poppler utility is listed separately because they can
// genuinely be packaged apart, and because a precise "pdftoppm is missing" beats
// a vague "install poppler".
var (
	PDFToText = Tool{
		Name:        "pdftotext",
		Purpose:     "extract text and layout from PDFs with a text layer",
		VersionArgs: []string{"-v"},
		Install:     popplerInstall,
	}
	PDFToPPM = Tool{
		Name:        "pdftoppm",
		Purpose:     "render PDF pages to images for OCR and vision conversion",
		VersionArgs: []string{"-v"},
		Install:     popplerInstall,
	}
	PDFImages = Tool{
		Name:        "pdfimages",
		Purpose:     "extract embedded illustrations from PDFs",
		VersionArgs: []string{"-v"},
		Install:     popplerInstall,
	}
	PDFInfo = Tool{
		Name:        "pdfinfo",
		Purpose:     "read PDF page count and metadata",
		VersionArgs: []string{"-v"},
		Install:     popplerInstall,
	}
	// PDFToHTML is listed separately from pdftotext because it answers a question
	// pdftotext cannot. Only its XML output carries font size, family and weight,
	// and those are what separate a heading from a paragraph. Measured on the
	// fixture's English section: body text is 11pt regular at 58% of characters,
	// while 17pt *regular* is safety body copy at another 15% — so a
	// "larger than body means heading" rule promotes prose. Weight is the
	// discriminator, and pdftotext does not report it.
	PDFToHTML = Tool{
		Name:        "pdftohtml",
		Purpose:     "read font size and weight, which is how headings are found",
		VersionArgs: []string{"-v"},
		Install:     popplerInstall,
	}
	Tesseract = Tool{
		Name:        "tesseract",
		Purpose:     "OCR scanned manuals and phone photos",
		VersionArgs: []string{"--version"},
		Install: map[string]string{
			"darwin":  "brew install tesseract tesseract-lang",
			"linux":   "apt install tesseract-ocr tesseract-ocr-all",
			"default": "https://tesseract-ocr.github.io/tessdoc/Installation.html",
		},
	}
)

var popplerInstall = map[string]string{
	"darwin":  "brew install poppler",
	"linux":   "apt install poppler-utils",
	"default": "https://poppler.freedesktop.org/",
}

// All is every tool manualbox may use, in doctor-report order.
func All() []Tool {
	return []Tool{PDFToText, PDFToHTML, PDFToPPM, PDFImages, PDFInfo, Tesseract}
}

// ErrNotFound is returned by [Require] when a tool is not installed.
var ErrNotFound = errors.New("external tool not found")

// versionPattern pulls the first dotted version number out of a banner.
var versionPattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// Probe looks up a tool and reads its version.
func Probe(ctx context.Context, t Tool) Status {
	s := Status{Tool: t}

	path, err := exec.LookPath(t.Name)
	if err != nil {
		s.Err = fmt.Errorf("%w: %s", ErrNotFound, t.Name)
		return s
	}
	s.Found, s.Path = true, path

	if len(t.VersionArgs) == 0 {
		return s
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// #nosec G204 -- the name comes from this package's own tool table, never
	// from user input, and is resolved through LookPath.
	cmd := exec.CommandContext(ctx, path, t.VersionArgs...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out

	// Deliberately ignoring the exit status: `pdftotext -v` exits non-zero while
	// still printing a perfectly good version banner.
	_ = cmd.Run()

	if v := versionPattern.FindString(out.String()); v != "" {
		s.Version = v
	} else if banner := strings.TrimSpace(out.String()); banner != "" {
		s.Version = firstLine(banner)
	}
	return s
}

// ProbeAll probes every known tool concurrently.
func ProbeAll(ctx context.Context) []Status {
	tools := All()
	out := make([]Status, len(tools))
	var wg sync.WaitGroup
	for i, t := range tools {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = Probe(ctx, t)
		}()
	}
	wg.Wait()
	return out
}

// resolved caches successful lookups keyed by tool name. Tools do not get
// installed or removed while the process runs, so paying for the PATH walk once
// is enough.
var resolved sync.Map // tool name -> resolved absolute path

// Require resolves a tool's path for an adapter that needs it, returning an
// error that names the tool and how to install it. Adapters should call this at
// construction so a missing binary surfaces as a clear configuration problem
// rather than as a failed job halfway through a document.
func Require(t Tool) (string, error) {
	if cached, ok := resolved.Load(t.Name); ok {
		if path, ok := cached.(string); ok {
			return path, nil
		}
	}
	path, err := exec.LookPath(t.Name)
	if err != nil {
		hint := Status{Tool: t}.InstallHint()
		return "", fmt.Errorf("%w: %s (needed to %s; install with: %s)", ErrNotFound, t.Name, t.Purpose, hint)
	}
	resolved.Store(t.Name, path)
	return path, nil
}

// Available reports whether a tool is installed, for capability checks that
// should degrade rather than fail.
func Available(t Tool) bool {
	_, err := Require(t)
	return err == nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
