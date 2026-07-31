package doc

import (
	"strings"
	"testing"
)

// Hermetic tests for the visual-to-logical repair in bidi.go. No poppler and no
// PDF: every string here is written out in both orders, so what the tool returns
// and what the page prints can be read side by side.
//
// The strings are the ones bidi.go's header measured on the sequential manual's
// Hebrew page 185 and Arabic page 201. They are written the way Go source holds
// them — a rune sequence — so `שומיש תולבגה` below is the VISUAL reading, the runes
// in the order poppler emits them, and `הגבלות שימוש` is what the page prints.
// An editor that reorders bidi text for display makes the two look confusingly
// alike; the tests compare runes, which do not care.
//
// Arabic is unshaped in both tools — isolated letter forms rather than the
// presentation forms the page prints — and that is a property of the font's glyph
// map, not of the ordering. The Arabic cases below are therefore written in the
// same unshaped form the pipeline actually sees.

// TestVisualToLogicalReproducesTheReferenceReading is the reference case from
// bidi.go's header: the run reads `שומיש תולבגה` where the page prints
// `הגבלות שימוש`, "usage restrictions".
//
// The run-level cases are the ones the pipeline actually feeds this function:
// [joinRunsRightToLeft] calls it once per run, and on page 185 the digit is a run
// of its own.
func TestVisualToLogicalReproducesTheReferenceReading(t *testing.T) {
	for _, tc := range []struct {
		name           string
		visual, prints string
	}{
		{"the header's worked example, page 185", "שומיש תולבגה", "הגבלות שימוש"},
		{"a Hebrew heading", "תוחיטב תוארוה", "הוראות בטיחות"},
		{"Arabic, unshaped in both tools, page 201", "ةمالسلا تاداشرإ", "إرشادات السلامة"},

		{"a run that is only a digit", "8", "8"},
		{"a run that is only a two-digit number", "10", "10"},
		{"a run that is only a Latin product name", "MopExtend", "MopExtend"},
		{"a run that is only a Latin phrase", "Dreame L40 Ultra", "Dreame L40 Ultra"},

		{"an empty run", "", ""},
		{"a run of spaces", "   ", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := visualToLogical(tc.visual); got != tc.prints {
				t.Errorf("visualToLogical(%q)\n = %q\nwant %q", tc.visual, got, tc.prints)
			}
		})
	}
}

// TestALeftToRightIslandIsNotReversed is the reason the repair is not a whole-string
// reversal. `8` is printed `8` inside Hebrew prose and `MopExtend` reads forwards, so
// each island has to be put back the way it was.
//
// Stated as "the island survives and its reversal does not appear" rather than as a
// whole expected string, because the island's own SPACING is a separate and currently
// wrong thing — see TestAnIslandKeepsTheSpaceThatSeparatesIt, which was that defect.
//
// One digit is not enough to see this: a naive whole-string reversal is
// indistinguishable on `8` and wrong on `10`, which is why both are here.
func TestALeftToRightIslandIsNotReversed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		visual   string
		want     string // the island, forwards
		reversed string // what a whole-string reversal would have produced
	}{
		{"one digit, indistinguishable from a naive reversal", "8 ליגל תחתמ", "8", ""},
		{"two digits, where a naive reversal shows", "10 ליגל תחתמ", "10", "01"},
		{"two digits, interior", "ליגל 21 תחתמ", "21", "12"},
		{"a Latin product name", "תשרבמ MopExtend רישכמ", "MopExtend", "dnetxEpoM"},
		{"a Latin phrase stays one island", "שדח Dreame L40 Ultra רישכמ", "Dreame L40 Ultra", "artlU 04L emaerD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := visualToLogical(tc.visual)
			if !strings.Contains(got, tc.want) {
				t.Errorf("visualToLogical(%q)\n = %q\ndoes not contain the island %q", tc.visual, got, tc.want)
			}
			if tc.reversed != "" && strings.Contains(got, tc.reversed) {
				t.Errorf("visualToLogical(%q)\n = %q\nholds %q — the island was reversed with the line",
					tc.visual, got, tc.reversed)
			}
			// The Hebrew around the island is reversed, which is the other half: the
			// island rule must not have exempted the whole line.
			if strings.Contains(got, "תחתמ") {
				t.Errorf("visualToLogical(%q) = %q still reads the Hebrew visually", tc.visual, got)
			}
		})
	}
}

// TestATrailingSpaceIsNotDraggedIntoAnIsland is [leftToRightIsland]'s own note: a
// space counts as part of an island only when the next rune does too, so
// `Dreame L40 Ultra` survives as one island while a space at the end of one does not
// join it.
func TestATrailingSpaceIsNotDraggedIntoAnIsland(t *testing.T) {
	for _, tc := range []struct {
		name string
		rs   string
		i    int
		want bool
	}{
		{"a Latin letter", "MopExtend", 0, true},
		{"a European digit", "8", 0, true},
		{"an Arabic-Indic digit", "٨", 0, true},
		{"a Hebrew letter", "ם", 0, false},
		{"an Arabic letter", "ا", 0, false},

		{"a space between two Latin words", "L40 Ultra", 3, true},
		{"a space between a digit and a letter", "8 x", 1, true},
		{"a space before Hebrew ends the island", "10 ם", 2, false},
		{"a space at the end of the string ends the island", "10 ", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := []rune(tc.rs)
			if got := leftToRightIsland(rs, tc.i); got != tc.want {
				t.Errorf("leftToRightIsland(%q, %d) = %v, want %v", tc.rs, tc.i, got, tc.want)
			}
		})
	}
}

// TestAnIslandKeepsTheSpaceThatSeparatesIt is the defect this test was written to
// pin, now fixed rather than pinned.
//
// [leftToRightIsland] used to take a space whose NEXT rune was left to right — which
// is what keeps `Dreame L40 Ultra` in one piece — wherever it sat, including where
// the rune before it was Hebrew. The island put back was then " MopExtend", the space
// landed on the wrong side of it, and after [collapseSpaces] the word before it was
// glued on: `מכשירMopExtend` for a page printing `מכשיר MopExtend`. That is a lost
// word boundary and therefore a lost search hit.
//
// A space now has to sit BETWEEN two left-to-right runes to belong to an island. The
// case is real rather than constructed: poppler emits both scripts in one run on
// five of the sequential manual's right-to-left pages — `Wi-Fi ןווחמ` on 189,
// `Class 1 רזייל` on 188 — and it is invisible on page 185, where every digit is a
// run of its own, which is why the pdftotext comparison never caught it.
func TestAnIslandKeepsTheSpaceThatSeparatesIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		visual string
		want   string
	}{
		{"a Latin name between two Hebrew words", "תשרבמ MopExtend רישכמ", "מכשיר MopExtend מברשת"},
		{"a digit at the end of a Hebrew phrase", "8 ליגל תחתמ", "מתחת לגיל 8"},
		{"a hyphenated Latin name, from page 189", "ןווחמ Wi-Fi", "Wi-Fi מחוון"},
		{"a phrase whose own spaces must survive", "שדח Dreame L40 Ultra רישכמ", "מכשיר Dreame L40 Ultra חדש"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := visualToLogical(tc.visual); got != tc.want {
				t.Errorf("visualToLogical(%q)\n = %q\nwant %q", tc.visual, got, tc.want)
			}
		})
	}
}
func TestVisualToLogicalIsItsOwnInverse(t *testing.T) {
	for _, s := range []string{
		"שומיש תולבגה",
		"הגבלות שימוש",
		"תוחיטב תוארוה",
		"ةمالسلا تاداشرإ",
		"8",
		"10",
		"MopExtend",
		"Dreame L40 Ultra",
		"8 ליגל תחתמ",
		"ליגל 21 תחתמ",
		"תשרבמ MopExtend רישכמ",
		"שדח Dreame L40 Ultra רישכמ",
		"",
		"   ",
	} {
		t.Run(s, func(t *testing.T) {
			once := visualToLogical(s)
			if twice := visualToLogical(once); twice != s {
				t.Errorf("visualToLogical twice over %q\n = %q\nby way of %q", s, twice, once)
			}
		})
	}
}

// TestLineIsRightToLeftByMajorityOfTheStrongCharacters covers the direction test.
// By majority rather than by the first character, for the reason the function's own
// note gives: the Unicode P2 rule wants the first character in LOGICAL order, and
// logical order is what has been lost.
//
// The region's language now decides and the majority is the fallback, so each case
// states both answers. The row that made the change necessary is the URL line: its
// Latin outweighs its Hebrew, so the majority reads it left to right and the six
// lines like it across pages 188 to 207 were never repaired. See [lineIsRightToLeft].
func TestLineIsRightToLeftByMajorityOfTheStrongCharacters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		texts     []string
		want      bool // with no region language to go on
		wantInRTL bool // inside a region a right-to-left language was named for
	}{
		{"Hebrew", []string{"שומיש תולבגה"}, true, true},
		{"Arabic, unshaped", []string{"ةمالسلا تاداشرإ"}, true, true},
		{"mostly Hebrew with a Latin island", []string{"תשרבמ MopExtend רישכמ"}, true, true},
		{"Hebrew and a digit across two runs", []string{"8", "ליגל תחתמ"}, true, true},

		// Page 188: about 55 Latin letters of URL against 30 Hebrew. The majority gets
		// this wrong, the region gets it right, and this row is the whole reason the
		// region is asked first.
		{"a Hebrew line carrying a URL", []string{
			"https://global.dreametech.com/pages/user-manuals-and-faqs :האבה תבותכב ןייעל שי",
		}, false, true},
		{"Dreamehome in a Hebrew line, page 191", []string{"Dreamehome תייצקלפא"}, false, true},

		{"Latin", []string{"Sicherheitshinweise"}, false, false},
		{"Cyrillic", []string{"Меры предосторожности"}, false, false},
		{"Greek", []string{"Οδηγίες ασφαλείας"}, false, false},
		{"Japanese", []string{"安全上のご注意"}, false, false},

		// A line with no right-to-left character is left alone whatever its region
		// says: reversing its runes is a no-op, but reversing the ORDER of its runs is
		// not, so a two-run Latin line in a Hebrew region would come out backwards.
		{"Latin inside a right-to-left region", []string{"Wi-Fi", "5 GHz"}, false, false},
		{"no strong characters at all", []string{"", "  ", "10 – 22"}, false, false},
		{"nothing at all", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := make([]TextRun, len(tc.texts))
			for i, s := range tc.texts {
				runs[i] = TextRun{Text: s}
			}
			if got := lineIsRightToLeft(runs, false); got != tc.want {
				t.Errorf("lineIsRightToLeft(%q, no region language) = %v, want %v",
					tc.texts, got, tc.want)
			}
			if got := lineIsRightToLeft(runs, true); got != tc.wantInRTL {
				t.Errorf("lineIsRightToLeft(%q, right-to-left region) = %v, want %v",
					tc.texts, got, tc.wantInRTL)
			}
		})
	}
}

// TestARightToLeftLanguageIsNamedByItsBaseTag pins the list [IsRightToLeftLanguage]
// keeps, including that it reads a regional tag through BaseLanguage the way every
// other language decision in this package does.
func TestARightToLeftLanguageIsNamedByItsBaseTag(t *testing.T) {
	for _, tc := range []struct {
		lang string
		want bool
	}{
		{"he", true}, {"ar", true}, {"fa", true}, {"ur", true},
		{"he-IL", true}, {"ar-EG", true},
		{"de", false}, {"ru", false}, {"ja", false}, {"", false},
		{"pt-BR", false},
	} {
		if got := IsRightToLeftLanguage(tc.lang); got != tc.want {
			t.Errorf("IsRightToLeftLanguage(%q) = %v, want %v", tc.lang, got, tc.want)
		}
	}
}

// TestJoinRunsRightToLeftTakesTheRunsFromTheRightmost uses the geometry bidi.go's
// header measured: page 185's second paragraph is three runs at x=89 (width 555),
// x=643 (width 9, the digit 8) and x=653 (width 207), and the line begins at the
// RIGHT, so the run at 653 is read first and the run at 89 last.
//
// The boxes overlap by a point where the widest run ends (89+555 = 644) and the digit
// begins (643), so no space is inserted there — the gap rule sees -1. That is the
// measured geometry and not a rounding of it, so the expected reading below carries
// the join it produces.
func TestJoinRunsRightToLeftTakesTheRunsFromTheRightmost(t *testing.T) {
	runs := []TextRun{
		{X: 89, Y: 300, Width: 555, Height: 22, Text: "םידליל תתל ןיא"},
		{X: 643, Y: 300, Width: 9, Height: 22, Text: "8"},
		{X: 653, Y: 300, Width: 207, Height: 22, Text: "ליגל תחתמ"},
	}
	const want = "מתחת לגיל 8אין לתת לילדים"

	got := joinRunsRightToLeft(runs)
	if got != want {
		t.Errorf("joinRunsRightToLeft\n = %q\nwant %q", got, want)
	}

	// Reading order, stated separately so a failure says which half broke: the
	// rightmost run's words come first and the leftmost run's last.
	first, last := strings.Index(got, "מתחת"), strings.Index(got, "אין")
	if first < 0 || last < 0 {
		t.Fatalf("joinRunsRightToLeft = %q, missing one of the two Hebrew chunks", got)
	}
	if first > last {
		t.Errorf("joinRunsRightToLeft = %q reads the run at x=89 before the one at x=653", got)
	}

	// The runs slice itself must not be reordered: the caller's geometry is
	// computed from it and every other reader of a line wants it left to right.
	for i, wantX := range []float64{89, 643, 653} {
		if runs[i].X != wantX {
			t.Errorf("runs[%d].X = %g after the join, want %g — the slice was reordered",
				i, runs[i].X, wantX)
		}
	}
	if runs[1].Text != "8" {
		t.Errorf("runs[1].Text = %q after the join, want the untouched visual %q", runs[1].Text, "8")
	}
}

// TestRegionBlocksReadsARightToLeftLineLogically is the wiring: the repair lives in
// one place, textLine.finish, so a block built from Hebrew runs must come out in the
// order the page is written in — that text is what the reader shows and what the
// search index holds. A left-to-right line in the same page must be untouched.
func TestRegionBlocksReadsARightToLeftLineLogically(t *testing.T) {
	const (
		pageWidth  = 918
		pageHeight = 620
		body       = 17
	)
	// Two paragraphs on one page: Hebrew at the top, German lower down, far enough
	// apart that the gap rule keeps them separate blocks.
	//
	// The German line is TWO runs on one baseline, which is what makes it a real
	// control: a line of one run reads the same in either direction, so it would
	// pass even if the direction test said every line was right to left.
	hebrew := []string{"שומיש תולבגה", "תוחיטב תוארוה"}

	p := &PageRuns{No: 185, Width: pageWidth, Height: pageHeight}
	y := 20.0
	for _, s := range hebrew {
		p.Runs = append(p.Runs, TextRun{X: 200, Y: y, Width: 600, Height: body + 5, Text: s,
			Font: Font{Size: body, Family: "Test-Face"}})
		y += 22
	}
	y += 66
	for _, r := range []struct {
		x, w float64
		text string
	}{
		{55, 240, "Lesen Sie die Anleitung"},
		{300, 200, "vor der Verwendung"},
	} {
		p.Runs = append(p.Runs, TextRun{X: r.x, Y: y, Width: r.w, Height: body + 5, Text: r.text,
			Font: Font{Size: body, Family: "Test-Face"}})
	}

	got := RegionBlocks(p, &Region{Page: 185, X0: 0, X1: pageWidth, Lang: "he"}, nil, nil)
	if len(got) != 2 {
		t.Fatalf("got %d blocks, want the Hebrew paragraph and the German one: %+v", len(got), got)
	}

	// Both lines are repaired, and their order down the page is unchanged: the
	// repair is inside a line, not across them.
	const wantHebrew = "הגבלות שימוש הוראות בטיחות"
	if got[0].Text != wantHebrew {
		t.Errorf("the Hebrew block reads\n %q\nwant %q", got[0].Text, wantHebrew)
	}
	for _, visual := range hebrew {
		if strings.Contains(got[0].Text, visual) {
			t.Errorf("the Hebrew block %q still holds the visual run %q", got[0].Text, visual)
		}
	}

	const wantGerman = "Lesen Sie die Anleitung vor der Verwendung"
	if got[1].Text != wantGerman {
		t.Errorf("the left-to-right block reads\n %q\nwant %q — it must not have been touched",
			got[1].Text, wantGerman)
	}
}

// TestAMultiRunLeftToRightIslandKeepsItsOrder is a regression this file shipped and
// the verifier caught, so the geometry is the real one.
//
// pdftohtml splits a run at a font change, and page 204 of the sequential manual sets
// the punctuation of its support URL in one font and the words in another — so
// `https://global.dreametech.com/pages/user-manuals-and-faqs` arrives as SEVENTEEN
// runs, broken at every `:`, `/`, `.` and `-`. Reversing the order of a line's runs
// is right for the Arabic prose beside it and wrong for those seventeen, and the line
// came out `faqs-and- manuals-user/pages/com.dreametech.global://https`.
//
// Page 188's Hebrew twin prints the same URL as ONE run and never showed it, which is
// the same way the character-level version of this bug hid from the pdftotext
// comparison. A line is not a reliable witness to how poppler will cut it up.
func TestAMultiRunLeftToRightIslandKeepsItsOrder(t *testing.T) {
	const url = "https://global.dreametech.com/pages/user-manuals-and-faqs"
	parts := []string{"https", "://", "global", ".", "dreametech", ".", "com", "/",
		"pages", "/", "user", "-", "manuals", "-", "and", "-", "faqs"}

	var runs []TextRun
	x := 300.0
	for _, p := range parts {
		w := float64(len([]rune(p))) * 6
		runs = append(runs, TextRun{X: x, Y: 100, Width: w, Height: 14, Text: p})
		x += w
	}
	// The Arabic prose sits to the right of the URL, as it does on the page, so it is
	// read first.
	const arabic = "لىإ لاقتنلاا ىجرُي"
	runs = append(runs, TextRun{X: x + 8, Y: 100, Width: 200, Height: 14, Text: arabic})

	got := joinRunsRightToLeft(runs)
	if !strings.Contains(got, url) {
		t.Errorf("joinRunsRightToLeft(...)\n = %q\ndoes not hold the URL %q in one piece", got, url)
	}
	if !strings.HasPrefix(got, visualToLogical(arabic)) {
		t.Errorf("joinRunsRightToLeft(...)\n = %q\ndoes not begin with the Arabic, which is "+
			"the rightmost run and therefore read first", got)
	}
}
