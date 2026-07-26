package doc

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/gordon2/manualbox/internal/extern"
)

// Positioned text is read with pdftohtml, not pdftotext, because a column is a
// geometric fact and pdftotext reports no coordinates at all. It is the input
// [DetectColumns] and [ColumnLanguages] were written against, and until this
// existed nothing could supply it: both were only ever called with coordinates
// written by hand in a test, so a disagreement between what poppler reports and
// what those tests assumed could not be caught.
//
// The cost is measured, whole-document, poppler 26.07.0:
//
//	560-page, 15 MB manual   1.79 s   3.8 MB of XML   34,413 runs
//	68-page, 9 MB manual     3.18 s   920 KB of XML    7,493 runs
//
// So it is the same order as the pdftotext pass it runs beside (1.8 s on the
// first document) and stays inside the free stages. One invocation over the whole
// document rather than one per page, for the reason [ExtractText] gives: process
// startup dominates everything else.
//
// Coordinates come back at 1.5 times the PDF's own points — 108 dpi against 72 —
// which is not a detail to work around but the property that makes the geometry
// checkable: a `pdftoppm -r 108` raster matches this space 1:1, so a detected
// column can be drawn on the rendered page and looked at. Measured on both
// fixtures: 918/612.283 and 892/595.276, 850/566.929.

// PageRuns is one page's positioned text and the page box it sits in.
type PageRuns struct {
	// No is the 1-based page number in the original PDF.
	No int
	// Width and Height are the page box as poppler reports it — the PDF's own
	// size scaled by 1.5. Carried per page rather than per document because
	// nothing guarantees a manual's pages are one size, and every threshold in
	// [DetectColumns] is a fraction of the page it is judging.
	Width, Height float64
	// Runs is the page's text, in the order the tool emitted it.
	Runs []TextRun
}

// HasText reports whether any text was found on the page.
func (p *PageRuns) HasText() bool { return len(p.Runs) > 0 }

// ExtractRuns reads every page's positioned text with pdftohtml.
//
// It never mutates the file and calls nothing remote, so like [ExtractText] it is
// a pure function of the bytes and safe to re-run — which is what lets the probe
// job be idempotent.
//
// pdftohtml is optional at runtime. A caller that cannot get runs must still be
// able to probe a document, so the error from a missing tool is returned plainly
// for the caller to degrade on rather than treated as a failed document.
func ExtractRuns(ctx context.Context, path string) ([]PageRuns, error) {
	bin, err := extern.Require(extern.PDFToHTML)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	// -xml for the coordinate form, -i to skip images (nothing here reads them,
	// and writing them would put files beside the blob store), -stdout to keep
	// the output in memory. Deliberately no -hidden: the flags are exactly the
	// ones the fixture's run counts and the detector's thresholds were measured
	// with, and -hidden would add text those measurements never saw.
	// #nosec G204 -- see ProbeInfo: bin comes from extern's own tool table and
	// path is a blob-store path derived from a validated SHA-256 digest.
	cmd := exec.CommandContext(ctx, bin, "-xml", "-i", "-enc", "UTF-8", "-stdout", path)
	out := &limitedBuffer{limit: maxExtractedBytes}
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return nil, fmt.Errorf("%w (limit %d bytes)", errOutputTooLarge, maxExtractedBytes)
		}
		return nil, fmt.Errorf("doc: pdftohtml failed: %w: %s",
			err, redact(strings.TrimSpace(errOut.String()), path))
	}

	pages, err := parsePDFXML(out.buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("doc: reading pdftohtml output for %s: %w", redact(path, path), err)
	}
	return pages, nil
}

// parsePDFXML reads poppler's pdf2xml form into pages of runs.
func parsePDFXML(data []byte) ([]PageRuns, error) {
	// Unmarshalling rather than a hand-rolled scan, because the payload is real
	// XML: the measured manual carries 57 escaped entities in body text, and a
	// regex over the raw bytes reports "GmbH &amp; Co. KG" as its own text.
	//
	// The DOCTYPE poppler emits names an external DTD. Go's decoder never
	// resolves one, so it is inert here — worth knowing rather than worth
	// stripping.
	var doc pdfXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	pages := make([]PageRuns, 0, len(doc.Pages))
	for i := range doc.Pages {
		p := &doc.Pages[i]
		if p.Number < 1 {
			// Page numbers reach the language map and the database, where a wrong
			// one mislabels a whole section. A tool that stops emitting them should
			// say so here rather than have positions guessed for it.
			return nil, fmt.Errorf("page %d of %d carries no page number", i+1, len(doc.Pages))
		}
		out := PageRuns{
			No:     p.Number,
			Width:  p.Width,
			Height: p.Height,
			Runs:   make([]TextRun, 0, len(p.Texts)),
		}
		for j := range p.Texts {
			t := &p.Texts[j]
			out.Runs = append(out.Runs, TextRun{
				X: t.Left, Y: t.Top, Width: t.Width, Height: t.Height, Text: t.Text,
			})
		}
		pages = append(pages, out)
	}
	return pages, nil
}

// pdfXML mirrors the part of poppler's pdf2xml output this code reads. Font
// specs, images and the producer are ignored: the geometry and the characters are
// the whole input to column detection.
type pdfXML struct {
	Pages []xmlPage `xml:"page"`
}

type xmlPage struct {
	Number int       `xml:"number,attr"`
	Width  float64   `xml:"width,attr"`
	Height float64   `xml:"height,attr"`
	Texts  []xmlText `xml:"text"`
}

// xmlText is one <text> element: a positioned run of characters.
//
// Coordinates are read as floats although poppler currently prints integers,
// since nothing in the format promises that and a truncated coordinate would
// move a column boundary.
type xmlText struct {
	Top    float64
	Left   float64
	Width  float64
	Height float64
	Text   string
}

// UnmarshalXML collects the element's characters including those inside child
// elements.
//
// This is the whole reason for a custom unmarshaller, and it is not cosmetic.
// Go's `,chardata` skips the content of child elements, and poppler wraps a
// styled run's text in <b> or <i> — measured on the column fixture, 355 runs are
// wrapped and every one of them is wrapped whole, so `,chardata` returns nothing
// at all for them. Among those 355 are the printed language tabs D, PL and UA.
//
// The effect was measured both ways over that document's 169 columns rather than
// argued from the tabs' importance, and the first guess was wrong:
//
//	                       naive  correct
//	columns named            166      167
//	named by printed tag       0       53
//	named by its alphabet    166      114
//	tag/alphabet conflicts     0        1
//
// So the count barely moves. What collapses is attribution: 53 columns stop being
// named by the document's own printed tab and are named by their letters instead,
// and the one place where the two disagree stops being detectable. The count
// survives only because this manual's five languages have distinguishable
// alphabets — and a manual whose languages share one is precisely the case the
// printed tag outranks every other signal for, where nothing would be left.
//
// `,innerxml` fails the other way, keeping the tags and the raw entities.
func (t *xmlText) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		target := map[string]*float64{
			"top":    &t.Top,
			"left":   &t.Left,
			"width":  &t.Width,
			"height": &t.Height,
		}[attr.Name.Local]
		if target == nil {
			continue
		}
		if _, err := fmt.Sscanf(attr.Value, "%g", target); err != nil {
			return fmt.Errorf("text %s=%q: %w", attr.Name.Local, attr.Value, err)
		}
	}

	var text strings.Builder
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			// A truncated element is a broken document, not an empty one: io.EOF
			// here means the tool's output was cut off mid-run.
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("text element is unterminated")
			}
			return err
		}
		switch v := tok.(type) {
		case xml.CharData:
			text.Write(v)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth == 0 {
				t.Text = text.String()
				return nil
			}
			depth--
		}
	}
}
