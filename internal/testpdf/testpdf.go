// Package testpdf builds small, valid PDFs in memory for tests.
//
// It exists because of two constraints that meet awkwardly. The document pipeline
// can only be tested against a real PDF read by real poppler — a hand-made fake
// would test nothing. But no PDF may be committed to this repository: CI's hygiene
// job rejects every .pdf outright, because a committed document is either someone's
// copyrighted manual or someone's private paperwork.
//
// So the test corpus is generated. A few hundred bytes of PDF per page is enough
// to exercise page counting, text extraction, printed language tags, folios and a
// contents table, with no network access and nothing to commit.
//
// The text is written with a standard Type 1 font, so only Latin-1 characters are
// representable. Non-Latin script behaviour is unit-tested directly against
// strings instead, where no PDF is involved.
package testpdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Page is one page of a generated document.
type Page struct {
	// Headings are drawn above Lines in a second, heavier face at a larger size.
	//
	// They exist because a document written wholly in one font cannot exercise the
	// font a run comes back with, and that is what separates a heading from a
	// paragraph. One face and one size was enough while only geometry was read
	// back; it is not enough now.
	//
	// Two things measured on what poppler actually reports for these, neither of
	// them guessable. It names the family "Helvetica" for the bold face as well as
	// the regular one — a standard font is not embedded, so there is no subset name
	// to read a weight out of — and marks these runs <b> regardless. And it scales
	// the size by the same 1.5 as the coordinates, so headingSize of 17 arrives as
	// 26 and bodySize of 11 as 17. See runs_test.go, which asserts both.
	Headings []string
	// Lines are drawn top to bottom, and come back out of pdftotext in the same
	// order. The first line is therefore where a language tag goes, which is what
	// makes the printed-tag signal testable.
	//
	// They span the full measure from the left margin, so a long line here is how
	// a heading set across several columns is generated.
	Lines []string
	// Columns are blocks of text at their own horizontal offsets, drawn below
	// Lines and all starting at the same height.
	Columns []Column
	// Drawings are vector illustrations, drawn before any text so the text sits
	// over them the way a caption does on a real page.
	//
	// They exist because a document with no vector graphics cannot exercise the
	// figure reader at all, and that turned out to be the only kind of picture the
	// two real manuals contain: `pdfimages` yields not one illustration over their
	// 628 pages, and every diagram in both is drawn. See internal/doc/figures.go.
	// A raster is deliberately not generated here — embedding a JPEG would test a
	// path this project has no document for.
	Drawings []Drawing
}

// Drawing is a scribble in a box: a frame with a fixed number of strokes inside
// it, at a position in PDF points on the 612 by 792 page.
//
// Strokes rather than a shape, because what separates a picture from page
// furniture in internal/doc/figures.go is how many shapes an area holds, and a
// generator that draws one rectangle can only produce furniture. The strokes are
// laid out deterministically so a test can assert an exact count.
type Drawing struct {
	// X and Y are the lower-left corner in PDF points, W and H the size. Poppler
	// reports coordinates at 1.5 times this and measures Y down from the top of the
	// page, so a drawing at Y=400 on a 792-point page arrives with its top edge at
	// 1.5*(792-400-H).
	X, Y, W, H int
	// Strokes is how many lines to draw inside the frame, over and above the frame
	// itself. Each is a separate subpath, so the shape count internal/doc reads back
	// is Strokes plus one.
	Strokes int
}

// Column is a block of lines set at its own horizontal offset.
//
// It exists because a single-column generator cannot exercise the column
// detector, and geometry is the one input the detector has: it needs a real
// gutter in a real PDF read by real poppler, not hand-written coordinates. Every
// other test of it supplies runs directly, which cannot catch a disagreement
// between what this package writes and what poppler reports back.
type Column struct {
	// X is the left edge in PDF points, on the 612 by 792 page this package
	// writes. Poppler's XML reports coordinates at 1.5 times this — 108 dpi
	// against the PDF's 72 — so a column at X=60 arrives as left=90 and the page
	// as 918 by 1188. Measured on both real fixtures: 918/612.283 and 892/595.276.
	X int
	// Lines are drawn top to bottom, as [Page.Lines] are.
	Lines []string
}

// Doc describes a document to generate.
type Doc struct {
	Pages []Page
}

// TaggedSections builds a multi-language document of the shape real appliance
// manuals take: a contents table, then one section per language, every page
// carrying its own language code.
//
// codes are the language codes in document order, pagesPerSection how many pages
// each occupies. When withContents is true a contents page precedes the sections,
// listing each code with a title and its printed page — including, deliberately,
// the printed page rather than the PDF page, so the offset between them has to be
// resolved rather than assumed.
func TaggedSections(codes []string, pagesPerSection int, withContents bool) Doc {
	var d Doc

	if withContents {
		lines := []string{"Contents"}
		printed := 1
		for _, code := range codes {
			lines = append(lines, code, code+" User Manual", fmt.Sprint(printed))
			printed += pagesPerSection
		}
		d.Pages = append(d.Pages, Page{Lines: lines})
	}

	folio := 1
	for _, code := range codes {
		for i := range pagesPerSection {
			body := fmt.Sprintf("Section %s page %d. ", code, i+1) +
				strings.Repeat("Maintenance information for this appliance. ", 3)
			d.Pages = append(d.Pages, Page{Lines: []string{
				code,
				fmt.Sprintf("%s Safety Information", code),
				body,
				fmt.Sprint(folio),
			}})
			folio++
		}
	}
	return d
}

// Blank builds a document of n pages with no extractable text, standing in for a
// scan. It is what exercises the "no text layer" branch of the pipeline.
func Blank(n int) Doc {
	d := Doc{Pages: make([]Page, n)}
	return d
}

// Build renders the document to PDF bytes.
func (d Doc) Build() []byte {
	var buf bytes.Buffer
	// Offsets are byte positions of each object, needed for the cross-reference
	// table. Index 0 is the free head entry, so object numbering starts at 1.
	offsets := []int{0}

	addObject := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets)-1, body)
	}

	buf.WriteString("%PDF-1.4\n")
	// A binary comment marks the file as binary for tools that sniff it.
	buf.WriteString("%\xe2\xe3\xcf\xd3\n")

	// Object numbers are laid out in advance so references can be written before
	// the objects they point at exist.
	const catalogObj, pagesObj, fontObj, boldFontObj = 1, 2, 3, 4
	firstPageObj := 5

	kids := make([]string, 0, len(d.Pages))
	for i := range d.Pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPageObj+i*2))
	}

	addObject(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj))
	addObject(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), len(d.Pages)))
	addObject("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	// The second face is one of the standard 14, so it needs no embedded file and
	// no metrics of its own. Poppler does not report it as a distinct family —
	// see Page.Headings, it calls both of them "Helvetica" — but it does mark its
	// runs bold, which is what makes a weight readable end to end from here.
	addObject("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")

	for i, page := range d.Pages {
		contentObj := firstPageObj + i*2 + 1
		addObject(fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Contents %d 0 R "+
				"/Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> >>",
			pagesObj, contentObj, fontObj, boldFontObj))

		stream := pageStream(page)
		addObject(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	// Cross-reference table. Every entry is exactly 20 bytes, which the format
	// requires and which is the easiest thing to get subtly wrong.
	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(offsets))
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(offsets), catalogObj, xrefOffset)

	return buf.Bytes()
}

// Where text is placed on the 612 by 792 page, in PDF points.
const (
	marginX  = 72
	firstY   = 720
	lineStep = 24
	// bodySize and headingSize are the two sizes written. They differ enough that
	// poppler reports two distinct fontspecs rather than rounding them together.
	bodySize    = 11
	headingSize = 17
	// lastY is the floor. Lines pile up on it rather than running off the page,
	// because a run outside the page box is dropped by the column detector as
	// off-page and would silently vanish from a test's expectations.
	lastY = 40
)

// pageStream renders one page's text as a content stream.
func pageStream(p Page) string {
	if len(p.Headings) == 0 && len(p.Lines) == 0 && len(p.Columns) == 0 &&
		len(p.Drawings) == 0 {
		return ""
	}
	var b strings.Builder

	for _, d := range p.Drawings {
		drawDrawing(&b, d)
	}

	y := firstY
	for _, line := range p.Headings {
		drawIn(&b, "F2", headingSize, marginX, y, line)
		y = nextY(y)
	}
	for _, line := range p.Lines {
		drawLine(&b, marginX, y, line)
		y = nextY(y)
	}
	// Every column starts where the full-measure lines left off, so a heading in
	// Lines sits above all of them rather than beside one.
	for _, col := range p.Columns {
		cy := y
		for _, line := range col.Lines {
			drawLine(&b, col.X, cy, line)
			cy = nextY(cy)
		}
	}
	return b.String()
}

// drawDrawing writes a frame and its strokes as one stroked path per subpath.
//
// Each stroke is its own `m`/`l`/`S` rather than one path with many subpaths,
// because cairo re-emits a multi-subpath stroke as a single <path> element and
// internal/doc counts shapes off those elements' subpaths — separate paths keep
// the two counts equal and the test's arithmetic honest.
func drawDrawing(b *strings.Builder, d Drawing) {
	fmt.Fprintf(b, "0.5 w 0 0 0 RG\n%d %d %d %d re S\n", d.X, d.Y, d.W, d.H)
	for i := range d.Strokes {
		// A diagonal from the left edge to the right, stepped down the frame, so
		// every stroke is inside it and none is axis-aligned — an axis-aligned one
		// would also be read as a ruled line by rules.go, which would make a test
		// of one file depend on the other.
		dy := d.H * (i + 1) / (d.Strokes + 1)
		fmt.Fprintf(b, "%d %d m %d %d l S\n",
			d.X+2, d.Y+dy, d.X+d.W-2, d.Y+dy-d.H/(2*(d.Strokes+1))-1)
	}
}

func drawLine(b *strings.Builder, x, y int, line string) {
	drawIn(b, "F1", bodySize, x, y, line)
}

func drawIn(b *strings.Builder, font string, size, x, y int, line string) {
	fmt.Fprintf(b, "BT /%s %d Tf %d %d Td (%s) Tj ET\n", font, size, x, y, escapeString(line))
}

func nextY(y int) int {
	if y-lineStep < lastY {
		return lastY
	}
	return y - lineStep
}

// escapeString escapes the characters that would otherwise end a PDF string
// literal. Non-Latin-1 runes are replaced rather than mangled, since a standard
// Type 1 font cannot represent them and a silently corrupted glyph would make a
// test failure hard to read.
func escapeString(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 32:
			b.WriteByte(' ')
		case r > 255:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
