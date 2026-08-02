package doc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gordon2/manualbox/internal/extern"
)

// pageSeparator is what pdftotext writes between pages: a form feed. Splitting
// on it is what makes one invocation over the whole document equivalent to one
// invocation per page, at a fraction of the cost.
const pageSeparator = "\f"

// Bounds on the poppler subprocesses.
//
// Neither existed at first, and the job context alone is not a bound: it is
// cancelled only at shutdown, while the worker renews its lease for as long as
// the handler runs. A pdftotext that never terminates therefore held a worker for
// ever, and the default pool is two workers — so two such documents stopped all
// ingest permanently.
//
// The limits are set from measurement with generous headroom. A 560-page, 15 MB
// manual takes 0.06 s for pdfinfo and 1.8 s for pdftotext, and yields 1.4 MB of
// text.
const (
	infoTimeout    = 30 * time.Second
	extractTimeout = 5 * time.Minute
	// maxExtractedBytes caps the text held in memory. A PDF with heavily
	// compressed content streams is a small upload that expands enormously, and
	// the whole extraction is buffered before it is split into pages.
	maxExtractedBytes = 64 << 20
)

// errOutputTooLarge is returned when a tool produces more output than the cap.
var errOutputTooLarge = errors.New("doc: extracted text exceeds the size limit")

// limitedBuffer collects output up to a cap and then refuses more, so a runaway
// tool fails its job instead of exhausting the server's memory.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.limit {
		return 0, errOutputTooLarge
	}
	return l.buf.Write(p)
}

// redact replaces a filesystem path with its final component.
//
// Blob paths sit under the data directory, which normally lives in a home
// directory and so carries an operating-system username. These messages reach
// `documents.last_error`, the API, and the log — and a user pasting that into a
// public issue is threat 2 in docs/design/privacy.md. The base name is the
// content digest, which identifies the document precisely and reveals nothing.
func redact(msg, path string) string {
	if path == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, path, filepath.Base(path))
	if dir := filepath.Dir(path); dir != "" && dir != "." && dir != string(filepath.Separator) {
		msg = strings.ReplaceAll(msg, dir, "…")
	}
	return msg
}

// Info is what stage 0 discovers: the free, instant facts about a document.
//
// This is deliberately the cheapest possible question. It decides whether the
// document is processable at all and whether it is large enough to need the
// user's permission before anything is spent on it.
type Info struct {
	// Pages is the page count.
	Pages int
	// Encrypted reports whether the PDF is password-protected. An encrypted file
	// cannot be extracted from and is stored as-is.
	Encrypted bool
	// Tagged reports whether the PDF carries structure tags. Tagged PDFs have
	// usable reading order; untagged ones need geometry to recover it.
	Tagged bool
	// Producer and Creator identify the authoring tool, which is a useful hint
	// about layout conventions.
	Producer string
	Creator  string
	// WidthPts and HeightPts are the first page's dimensions.
	WidthPts, HeightPts float64
}

// ProbeInfo runs pdfinfo. Measured at 0.06 s on a 560-page, 15 MB document, so
// it is safe to call on upload rather than in a job.
func ProbeInfo(ctx context.Context, path string) (Info, error) {
	bin, err := extern.Require(extern.PDFInfo)
	if err != nil {
		return Info{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()

	// #nosec G204 -- bin is resolved by extern from its own tool table; path is a
	// blob-store path derived from a validated SHA-256 digest.
	cmd := exec.CommandContext(ctx, bin, path)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return Info{}, fmt.Errorf("doc: pdfinfo failed: %w: %s",
			err, redact(strings.TrimSpace(errOut.String()), path))
	}

	info := Info{}
	for line := range strings.Lines(out.String()) {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Pages":
			info.Pages, _ = strconv.Atoi(value)
		case "Encrypted":
			// pdfinfo prints "no" or a description of the encryption in use.
			info.Encrypted = value != "no"
		case "Tagged":
			info.Tagged = value == "yes"
		case "Producer":
			info.Producer = value
		case "Creator":
			info.Creator = value
		case "Page size":
			info.WidthPts, info.HeightPts = parsePageSize(value)
		}
	}
	if info.Pages <= 0 {
		// The digest, never the directory: this message reaches the API and the log.
		return Info{}, fmt.Errorf("doc: pdfinfo reported no pages for %s", filepath.Base(path))
	}
	return info, nil
}

// parsePageSize reads pdfinfo's "612.283 x 413.858 pts" form.
func parsePageSize(v string) (w, h float64) {
	fields := strings.Fields(v)
	if len(fields) < 3 {
		return 0, 0
	}
	w, _ = strconv.ParseFloat(fields[0], 64)
	h, _ = strconv.ParseFloat(fields[2], 64)
	return w, h
}

// ErrNoTextLayer is returned when a document yields no extractable text at all,
// which means the OCR or vision path is required.
var ErrNoTextLayer = errors.New("doc: no text layer")

// ExtractText runs pdftotext once over the whole document and splits the result
// into pages.
//
// One invocation, not one per page: measured at 1.76 s for 560 pages against
// roughly 0.1 s of process startup per page had it been called 560 times. The
// cost of extracting every page is low enough that there is no reason to sample.
func ExtractText(ctx context.Context, path string, pageCount int) ([]Page, error) {
	bin, err := extern.Require(extern.PDFToText)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	// -enc UTF-8 is explicit rather than relying on the build default, because
	// every downstream signal counts runes and a Latin-1 fallback would silently
	// corrupt every non-Latin section.
	// #nosec G204 -- see ProbeInfo.
	cmd := exec.CommandContext(ctx, bin, "-enc", "UTF-8", path, "-")
	out := &limitedBuffer{limit: maxExtractedBytes}
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return nil, fmt.Errorf("%w (limit %d bytes)", errOutputTooLarge, maxExtractedBytes)
		}
		return nil, fmt.Errorf("doc: pdftotext failed: %w: %s",
			err, redact(strings.TrimSpace(errOut.String()), path))
	}

	chunks := strings.Split(out.buf.String(), pageSeparator)
	// pdftotext emits a trailing separator after the final page, so the split
	// leaves an empty tail. Drop it rather than reporting a phantom blank page.
	if n := len(chunks); n > 0 && strings.TrimSpace(chunks[n-1]) == "" && n > pageCount {
		chunks = chunks[:n-1]
	}

	pages := make([]Page, 0, len(chunks))
	for i, body := range chunks {
		pages = append(pages, newPage(i+1, body))
	}
	return pages, nil
}

// Page is one page of a document and everything the free signals could tell
// about it.
type Page struct {
	// No is the 1-based page number in the original PDF.
	No int
	// Text is the extracted text, trimmed.
	Text string
	// Chars is the rune count. Deliberately runes and not bytes: a page of
	// Cyrillic or CJK has roughly twice the bytes for the same amount of writing,
	// so a byte-based threshold would judge scripts differently from each other.
	Chars int
	// Script is the dominant Unicode script, empty when there is no text.
	Script string
	// Tag is the language code the page prints on itself, empty when absent.
	// Unvalidated at this stage: it is a candidate, not a conclusion.
	Tag string
	// TagCandidates holds every standalone code-shaped token on the page, in
	// reading order. Needed because a right-to-left page does not put its tab
	// first: pdftotext emits the section heading ahead of it, and on many pages
	// it falls outside any small window from the top. Widening the window
	// indiscriminately is unsafe — NO, IT, IS, AS, BE and MY are all valid
	// language codes and all ordinary English words — so candidates are narrowed
	// by cross-checking against the codes the printed index knows about. See
	// [EffectiveTags].
	TagCandidates []string
	// Folio is the page number printed in the page's own footer, which differs
	// from No by the length of the front matter. Nil when the page prints none.
	Folio *int
	// Lang is the resolved language, filled in by reconciliation.
	Lang string
	// LangSource records which signal resolved Lang.
	LangSource string
}

// newPage derives the free per-page facts from extracted text.
func newPage(no int, body string) Page {
	text := strings.TrimSpace(body)
	p := Page{No: no, Text: text, Chars: len([]rune(text))}
	if text == "" {
		return p
	}
	p.Script = DominantScript(text)
	p.Tag = pageTag(text)
	p.TagCandidates = pageTagCandidates(text)
	p.Folio = pageFolio(text)
	return p
}

// HasText reports whether the page yielded any extractable text.
func (p Page) HasText() bool { return p.Chars > 0 }

// nonBlankLines returns up to limit trimmed, non-empty lines from the start of
// the text.
func nonBlankLines(text string, limit int) []string {
	out := make([]string, 0, limit)
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) == limit {
			break
		}
	}
	return out
}

// stripFormatting removes Unicode formatting characters, which carry no content
// but do break naive matching.
//
// This is not hygiene, it is required for correctness on right-to-left documents.
// A Hebrew or Arabic page wraps its Latin-script furniture in bidirectional
// embedding marks, so the language tab that reads "HE" is actually the five-rune
// sequence RLE LRE H E PDF PDF. Matching two ASCII letters against that fails,
// and the entire Hebrew and Arabic sections of a manual go unlabelled — which is
// exactly what happened before this existed.
//
// Category Cf covers the bidi controls (U+202A-U+202E, U+2066-U+2069), the
// directional marks (U+200E, U+200F), the byte-order mark and the soft hyphen.
// Zero-width space is category Zs and is stripped explicitly.
func stripFormatting(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || r == '​' {
			return -1
		}
		return r
	}, s)
}

// maxRunesInFolio bounds how long a line can be and still be a page number.
const maxRunesInFolio = 4

// pageFolio reads the page number printed on the page itself.
//
// It is the last purely numeric line, because a page number sits in the footer
// and pdftotext emits text in reading order. Folios are what let a printed
// index's claimed page be resolved to a real PDF page without assuming a global
// offset: on the measured fixture the offset is a constant +6, but that is a
// property of that document's front matter, not a constant of the format.
func pageFolio(text string) *int {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(stripFormatting(lines[i]))
		if line == "" || len([]rune(line)) > maxRunesInFolio {
			continue
		}
		if n, err := strconv.Atoi(line); err == nil && n > 0 {
			return &n
		}
	}
	return nil
}

// maxRunesInCodeLine bounds how long a line can be and still be considered a
// bare language tag. "ZH-HK" is the longest real example.
const maxRunesInCodeLine = 6

// pageTag finds a language code printed alone on its own line near the top of
// the page.
//
// Many manuals print each page's language in a corner tab, and pdftotext puts it
// first in reading order. It is the cheapest accurate language signal available —
// but it is a candidate only. Contents pages list language codes the same way and
// produce false positives, and a bare two-letter token like ON or TV is not a
// language at all. Both are filtered later, by run length and by script
// agreement. See docs/design/language-detection.md.
func pageTag(text string) string {
	for _, line := range nonBlankLines(text, 3) {
		line = strings.TrimSpace(stripFormatting(line))
		if line == "" || len([]rune(line)) > maxRunesInCodeLine {
			continue
		}
		if !looksLikeLanguageCode(line) || !PlausibleCodeToken(line) {
			continue
		}
		// A single letter cannot be trusted from position alone. On the measured
		// 34-language manual, page 511 of the Cantonese section opens with a
		// figure label "F" and carries its real ZH-HK tag on the next line;
		// reading the F as French split that section in two. Keep looking — the
		// real tag is often the line below — and let EffectiveTags adopt a single
		// letter only where the document's own contents table lists it.
		if singleLetterNeedsSupport(line) {
			continue
		}
		return strings.ToUpper(line)
	}
	return ""
}

// pageTagCandidates returns every standalone code-shaped token on the page, in
// reading order and deduplicated.
//
// Unlike [pageTag] this searches the whole page, because a right-to-left page's
// language tab is not near the start of the extracted text. The result is
// therefore permissive and must be narrowed before use — see [EffectiveTags].
func pageTagCandidates(text string) []string {
	var out []string
	seen := make(map[string]bool, 4)
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(stripFormatting(line))
		if line == "" || len([]rune(line)) > maxRunesInCodeLine {
			continue
		}
		if !looksLikeLanguageCode(line) || !PlausibleCodeToken(line) {
			continue
		}
		upper := strings.ToUpper(line)
		if seen[upper] {
			continue
		}
		seen[upper] = true
		out = append(out, upper)
	}
	return out
}

// looksLikeLanguageCode reports whether s has the shape of a printed language
// code.
//
// Manuals do not agree on the shape. The measured pair uses two-letter codes
// (EN, DE, ZH-HK) and one-and-three-letter ones (D, PL, RUS, UA, KAZ) — the
// second manual marks five languages and an exactly-two-letter matcher reads two
// of them.
//
// So one to three ASCII letters, optionally with a region. That is deliberately
// permissive and cannot be the whole test: a lone "D" is also a list marker, a
// size and a diagram label. Narrowing is [EffectiveTags]'s job, using the
// document's own contents-table vocabulary, and for a single letter that
// corroboration is required rather than preferred — see [singleLetterNeedsSupport].
func looksLikeLanguageCode(s string) bool {
	base, region, hasRegion := strings.Cut(s, "-")
	if !isASCIILetters(base, 1, 3) {
		return false
	}
	if hasRegion && !isASCIILetters(region, 2, 2) {
		return false
	}
	return true
}

// singleLetterNeedsSupport reports whether a code is too short to stand alone.
//
// "D" for German is real and common, but a single letter is the most ambiguous
// token on a page. It is believed only where something else agrees: the printed
// index listing it, or the column's own alphabet.
func singleLetterNeedsSupport(code string) bool {
	base, _, _ := strings.Cut(code, "-")
	return len(base) == 1
}

func isASCIILetters(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for i := range s {
		if s[i] > unicode.MaxASCII || !unicode.IsLetter(rune(s[i])) {
			return false
		}
	}
	return true
}
