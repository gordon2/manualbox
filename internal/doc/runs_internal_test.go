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
// The fontspec ids and families are the real ones: 5 is the body face, 11 the
// face poppler marks bold although its name does not say so, 73 a face whose
// name does, 75 an oblique, 9 the heaviest in the document, and 55 the Medium
// that poppler never marks at all. See Weight in runs.go for the counts each of
// those cases was measured at.
const realShapeXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE pdf2xml SYSTEM "pdf2xml.dtd">

<pdf2xml producer="poppler" version="26.07.0">
<page number="1" position="absolute" top="0" left="0" height="850" width="892">
	<fontspec id="5" size="14" family="HUTKLI+FuturaCon-Lig" color="#231f20"/>
	<fontspec id="9" size="19" family="HUTKLI+Function-Xbold" color="#656262"/>
	<fontspec id="11" size="15" family="HUTKLI+FuturaBQ" color="#231f20"/>
	<fontspec id="55" size="14" family="HUTKLI+FuturaCon-Med" color="#4f4b4c"/>
	<fontspec id="73" size="18" family="GMJEXK+FuturaCon-Bol" color="#231f20"/>
	<fontspec id="75" size="12" family="HUTKLI+Futura-BooObl" color="#231f20"/>
	<text top="17" left="60" width="15" height="25" font="11"><b>D</b></text>
	<text top="62" left="604" width="259" height="17" font="5">Ordinary body text.</text>
	<text top="161" left="61" width="211" height="18" font="73">Widgets &amp; Sprockets GmbH</text>
	<text top="-38" left="332" width="73" height="18" font="73">Parked above the page.</text>
	<text top="200" left="61" width="120" height="18" font="5">mixed <i>styling</i> inside</text>
	<text top="240" left="61" width="0" height="18" font="5">rotated</text>
	<text top="280" left="61" width="90" height="18" font="5"></text>
	<text top="320" left="61" width="88" height="14" font="75"><i>Wholly slanted</i></text>
	<text top="360" left="61" width="140" height="22" font="9"><i><b>Nested emphasis</b></i></text>
	<text top="400" left="61" width="150" height="18" font="55">Emphasis by weight alone</text>
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
	if len(p.Runs) != 10 {
		t.Fatalf("got %d runs, want 10 — every <text> element is a run, including "+
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

// TestParsePDFXMLReadsTheFontOfEachRun checks the font a run resolves to against
// the six real fontspec shapes, and specifically the cases where poppler's <b>/<i>
// markup and the family name disagree — which is why both are carried.
func TestParsePDFXMLReadsTheFontOfEachRun(t *testing.T) {
	pages, err := parsePDFXML([]byte(realShapeXML))
	if err != nil {
		t.Fatalf("parsePDFXML: %v", err)
	}
	runs := pages[0].Runs

	for _, tc := range []struct {
		run  int
		why  string
		want Font
	}{
		{1, "the body face: light by name, marked nothing", Font{
			Size: 14, Family: "HUTKLI+FuturaCon-Lig", Weight: WeightLight}},
		{0, "poppler marks the printed tab bold; FuturaBQ's name does not say so, " +
			"and 78 of the fixture's 78 FuturaBQ runs are this case", Font{
			Size: 15, Family: "HUTKLI+FuturaBQ", Weight: WeightUnknown, MarkedBold: true}},
		{2, "a name that does say bold — Bol, one letter from Boo, which is Book", Font{
			Size: 18, Family: "GMJEXK+FuturaCon-Bol", Weight: WeightBold}},
		{7, "wholly wrapped in <i>, and BooObl is Book plus oblique glued together", Font{
			Size: 12, Family: "HUTKLI+Futura-BooObl", Weight: WeightRegular,
			Oblique: true, MarkedItalic: true}},
		{8, "<i><b> nests on 5 runs of the fixture, so both must be recorded", Font{
			Size: 19, Family: "HUTKLI+Function-Xbold", Weight: WeightHeavy,
			MarkedBold: true, MarkedItalic: true}},
		{9, "the case that makes the markup insufficient on its own: Medium is " +
			"17.2% of the columns manual's characters at its body size, and poppler " +
			"marks not one of its 1,241 runs", Font{
			Size: 14, Family: "HUTKLI+FuturaCon-Med", Weight: WeightMedium}},
	} {
		if got := runs[tc.run].Font; got != tc.want {
			t.Errorf("run %d font = %+v, want %+v — %s", tc.run, got, tc.want, tc.why)
		}
	}

	// A run only partly wrapped is not a styled run. Labelling the whole of
	// `mixed <i>styling</i> inside` italic would name a line after one word of it.
	if f := runs[4].Font; f.MarkedItalic || f.MarkedBold {
		t.Errorf("run with interior markup reported as styled: %+v — the wrapper "+
			"speaks for the whole run or for none of it", f)
	}
	// And the text still has to survive, which is the older measurement this
	// walk exists for.
	if got := runs[4].Text; got != "mixed styling inside" {
		t.Errorf("noticing the markup cost the text: %q", got)
	}
}

// fontScopeXML gives page 2 a run whose font was declared on page 1, and page 3 a
// redeclaration of that same id.
const fontScopeXML = `<pdf2xml producer="poppler" version="26.07.0">
<page number="1" position="absolute" height="850" width="892">
	<fontspec id="0" size="11" family="RJYQHP+MiSansLatin" color="#4d505b"/>
	<text top="10" left="10" width="50" height="11" font="0">Declared here.</text>
</page>
<page number="2" position="absolute" height="850" width="892">
	<text top="10" left="10" width="50" height="11" font="0">Declared on page 1.</text>
</page>
<page number="3" position="absolute" height="850" width="892">
	<fontspec id="0" size="21" family="SJEGLN+MiSansLatin-Demibold" color="#4d505b"/>
	<text top="10" left="10" width="50" height="21" font="0">Redeclared here.</text>
</page>
<page number="4" position="absolute" height="850" width="892">
	<text top="10" left="10" width="50" height="21" font="99">Never declared.</text>
	<text top="40" left="10" width="50" height="21">No font attribute.</text>
</page>
</pdf2xml>
`

// TestFontSpecCarriesForwardAcrossPages is the trap, and it is the opposite of
// the one the format's per-page <fontspec> elements suggest.
//
// Poppler allocates ids once per document and declares each on the page that
// first uses it, so most pages declare none: 29 of the columns manual's 68 pages
// and 167 of the sequential manual's 560. A table scoped to one page therefore
// leaves 83% and 95% of their runs with no font, silently — every one of them
// still has coordinates and text, so nothing else fails. Reverted to a per-page
// table, this test reports the size as 0 on page 2.
func TestFontSpecCarriesForwardAcrossPages(t *testing.T) {
	pages, err := parsePDFXML([]byte(fontScopeXML))
	if err != nil {
		t.Fatalf("parsePDFXML: %v", err)
	}
	if len(pages) != 4 {
		t.Fatalf("got %d pages, want 4", len(pages))
	}

	if got := pages[1].Runs[0].Font; got.Size != 11 || got.Family != "RJYQHP+MiSansLatin" {
		t.Errorf("page 2 run font = %+v, want the id 0 declared on page 1 (11pt "+
			"MiSansLatin) — a page-scoped table resolves nothing on the pages that "+
			"declare no fontspec, which is most of a real manual", got)
	}

	// A page that does redeclare an id wins for its own runs. No page of either
	// fixture does, but that is what a per-page declaration means, and the cost
	// of honouring it is that the table is written before the page's text is read.
	if got := pages[2].Runs[0].Font; got.Size != 21 || got.Weight != WeightSemibold {
		t.Errorf("page 3 run font = %+v, want its own redeclaration of id 0 "+
			"(21pt, semibold), not the one inherited from page 1", got)
	}

	// An id that was never declared leaves the font empty rather than failing the
	// document: a font is an enrichment, and the page still owes the language map
	// its text.
	if got := pages[3].Runs[0]; got.Font != (Font{}) || got.Text != "Never declared." {
		t.Errorf("run with an unknown font id = %+v, want empty font and intact text", got)
	}

	// And an absent font attribute must not read as id 0, which is a real font:
	// poppler numbers fontspec ids from 0, so the zero value of the parsed field
	// cannot double as "no font". Every run of both fixtures does carry the
	// attribute, which is exactly why this would go unnoticed.
	if got := pages[3].Runs[1].Font; got != (Font{}) {
		t.Errorf("run with no font attribute resolved to %+v, want no font — id 0 "+
			"is the first font poppler declares, not a marker for its absence", got)
	}
}

func TestParseFamilyReadsTheWeightAFontNameAdmits(t *testing.T) {
	// Every input is a family measured in one of the two fixtures.
	for _, tc := range []struct {
		family  string
		weight  Weight
		oblique bool
	}{
		{"HUTKLI+FuturaCon-Lig", WeightLight, false},
		{"MyriadPro-Light", WeightLight, false},
		{"AlibabaSans-Light", WeightLight, false},
		{"HUTKLI+Futura-Boo", WeightRegular, false},
		{"HUTKLI+FuturaPT-Book", WeightRegular, false},
		{"ZILBKZ+MiSans-Normal", WeightRegular, false},
		{"NQXXDX+Alibaba-PuHuiTi-R", WeightRegular, false},
		{"HUTKLI+FuturaCon-Med", WeightMedium, false},
		{"GGMMEH+MiSans-Medium", WeightMedium, false},
		{"PMJWFT+Alibaba-PuHuiTi-M", WeightMedium, false},
		{"AlibabaPuHuiTi_2_65_Medium", WeightMedium, false},
		{"SJEGLN+MiSansLatin-Demibold", WeightSemibold, false},
		{"Arimo-SemiBold", WeightSemibold, false},
		{"GMJEXK+FuturaCon-Bol", WeightBold, false},
		{"AAACTX+HarmonyOS_Sans_SC_Bold", WeightBold, false},
		{"RanyBold", WeightBold, false},
		{"Alibaba-PuHuiTi-B", WeightBold, false},
		{"HUTKLI+Function-Xbold", WeightHeavy, false},

		// Slope, including the three names that glue it to the weight.
		{"HUTKLI+Futura-BooObl", WeightRegular, true},
		{"GMJEXK+FuturaCon-BolObl", WeightBold, true},
		{"GMJEXK+FuturaCon-MedObl", WeightMedium, true},

		// Names that admit nothing. Claiming regular for these would be the
		// heuristic overreaching: poppler marks FuturaBQ, FuturaStd and Calibri
		// bold, and 498 of 16,426 runs of plain "MiSans", so "the name is silent"
		// and "the font is regular" are different facts.
		{"HUTKLI+FuturaBQ", WeightUnknown, false},
		{"HUTKLI+FuturaStd", WeightUnknown, false},
		{"BZPIHV+Calibri", WeightUnknown, false},
		{"WVCUZF+MiSans", WeightUnknown, false},
		{"VPWOLH+Arimo", WeightUnknown, false},
		{"QIGZJR+MiSansVF", WeightUnknown, false},
		{"HUTKLI+Helvetica", WeightUnknown, false},
		{"GMJEXK+ZapfDingbatsITC", WeightUnknown, false},
		{"HUTKLI+EuropeanPiStd-1", WeightUnknown, false},
		// A trailing letter inside a longer token is not a weight code. This one
		// is a real family from the sequential manual, and reading its "508R" as
		// regular is what restricting single letters to whole tokens prevents.
		{"FZYOUH_508R--GB1-4", WeightUnknown, false},
		{"", WeightUnknown, false},
	} {
		w, obl := parseFamily(tc.family)
		if w != tc.weight || obl != tc.oblique {
			t.Errorf("parseFamily(%q) = %s/oblique=%t, want %s/oblique=%t",
				tc.family, w, obl, tc.weight, tc.oblique)
		}
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
