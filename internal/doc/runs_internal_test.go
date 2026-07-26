package doc

import (
	"strings"
	"testing"
)

// Unit tests for the pdf2xml reader, against the shapes actually measured in
// poppler's output. No poppler and no PDF: runs_test.go drives the real tool.

// realShapeXML reproduces every form the column fixture's 7,493 runs take,
// with neutral text. Each one is there because it was measured, and the counts
// are quoted where they matter.
const realShapeXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE pdf2xml SYSTEM "pdf2xml.dtd">

<pdf2xml producer="poppler" version="26.07.0">
<page number="1" position="absolute" top="0" left="0" height="850" width="892">
	<fontspec id="5" size="14" family="HUTKLI+FuturaCon-Lig" color="#231f20"/>
	<text top="17" left="60" width="15" height="25" font="11"><b>D</b></text>
	<text top="62" left="604" width="259" height="17" font="5">Ordinary body text.</text>
	<text top="161" left="61" width="211" height="18" font="73">Widgets &amp; Sprockets GmbH</text>
	<text top="-38" left="332" width="73" height="18" font="73">Parked above the page.</text>
	<text top="200" left="61" width="120" height="18" font="5">mixed <i>styling</i> inside</text>
	<text top="240" left="61" width="0" height="18" font="5">rotated</text>
	<text top="280" left="61" width="90" height="18" font="5"></text>
</page>
<page number="2" position="absolute" top="0" left="0" height="850" width="892">
</page>
</pdf2xml>
`

func TestParsePDFXMLReadsTheShapesPopplerEmits(t *testing.T) {
	pages, err := parsePDFXML([]byte(realShapeXML))
	if err != nil {
		t.Fatalf("parsePDFXML: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}

	p := &pages[0]
	if p.No != 1 {
		t.Errorf("page number = %d, want 1", p.No)
	}
	if p.Width != 892 || p.Height != 850 {
		t.Errorf("page box = %gx%g, want 892x850", p.Width, p.Height)
	}
	if len(p.Runs) != 7 {
		t.Fatalf("got %d runs, want 7 — every <text> element is a run, including "+
			"the empty and the off-page ones, which the detector counts and reports",
			len(p.Runs))
	}

	// The styled-tag case is the one that matters most. 355 of the fixture's runs
	// wrap their text in <b> or <i>, every one of them whole, and the printed
	// language tabs D, PL and UA are among them. Go's `,chardata` returns "" for
	// exactly these; runs.go records what that measurably costs on the real
	// document, and columns_fixture_test.go pins the split it costs it in.
	if got := p.Runs[0].Text; got != "D" {
		t.Errorf("run wrapped in <b> read as %q, want %q — the printed language "+
			"tabs are styled runs, and losing them loses the page-tag signal", got, "D")
	}
	if got := p.Runs[2].Text; got != "Widgets & Sprockets GmbH" {
		t.Errorf("entity read as %q, want the unescaped form", got)
	}
	if got := p.Runs[4].Text; got != "mixed styling inside" {
		t.Errorf("run with interior markup read as %q, want %q", got, "mixed styling inside")
	}

	// Coordinates are passed through as poppler gives them, negatives included.
	// 218 of the fixture's 769 runs on one page are parked above the top edge, and
	// dropping them here would hide the fact from DetectColumns, which counts them
	// as off-page and reports the count — that is what stops them merging two of
	// that page's three columns.
	if got := p.Runs[3].Y; got != -38 {
		t.Errorf("off-page run y = %g, want -38 kept as measured", got)
	}
	// Poppler reports rotated text with width 0. The detector drops it and says so;
	// the reader must not silently discard it first.
	if got := p.Runs[5].Width; got != 0 {
		t.Errorf("rotated run width = %g, want 0 kept as measured", got)
	}

	if pages[1].HasText() {
		t.Error("a page with no <text> elements should report no text")
	}
	if pages[1].Width != 892 {
		t.Errorf("a page with no text still has a box; got width %g", pages[1].Width)
	}
}

func TestParsePDFXMLRejectsAPageWithNoNumber(t *testing.T) {
	// A page number reaches the language map and the database, where a wrong one
	// mislabels a whole section. Guessing from position would be silent.
	const noNumber = `<pdf2xml><page position="absolute" height="850" width="892">
	<text top="1" left="1" width="1" height="1">x</text></page></pdf2xml>`

	if _, err := parsePDFXML([]byte(noNumber)); err == nil {
		t.Fatal("a page with no number was accepted")
	} else if !strings.Contains(err.Error(), "page number") {
		t.Errorf("error does not say what was missing: %v", err)
	}
}

func TestParsePDFXMLRejectsMalformedOutput(t *testing.T) {
	if _, err := parsePDFXML([]byte(`<pdf2xml><page number="1"><text top="1"`)); err == nil {
		t.Fatal("truncated XML was accepted")
	}
}
