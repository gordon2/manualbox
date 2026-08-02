package doc_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"unicode/utf8"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
)

// This is the measurement the heading rule will be built on, taken from both real
// manuals rather than from a generated one — a generated document has as many
// fonts as its generator was told to write, which settles nothing.
//
// It asserts only the properties that must not silently change, and logs the
// distribution. The line between the two is deliberate: the share of characters
// in a given face is a fact about someone else's typesetting and asserting it
// would break on a manifest bump, but a run losing its font, or either weight
// signal going to zero, is a defect here.

// fontStat accumulates one font's share of a document.
type fontStat struct {
	size         float64
	family       string
	weight       doc.Weight
	oblique      bool
	runs         int
	chars        int
	markedBold   int
	markedItalic int
}

// fontProfile summarizes every run of a document by the font it is set in.
//
// Characters are counted in runes, not bytes: half of the sequential manual is
// Cyrillic, Greek, Hebrew, Arabic or CJK, where bytes run a third higher and
// would make those sections look like more of the document than they are.
type fontProfile struct {
	pages, runs, chars int
	unresolved         int
	byFont             map[string]*fontStat
	bySize             map[float64]int
	byWeight           map[doc.Weight]int
}

func profileFonts(pages []doc.PageRuns) *fontProfile {
	p := &fontProfile{
		pages:    len(pages),
		byFont:   make(map[string]*fontStat),
		bySize:   make(map[float64]int),
		byWeight: make(map[doc.Weight]int),
	}
	for i := range pages {
		for j := range pages[i].Runs {
			r := &pages[i].Runs[j]
			n := utf8.RuneCountInString(r.Text)
			p.runs++
			p.chars += n
			if r.Font.Family == "" && r.Font.Size == 0 {
				p.unresolved++
				continue
			}
			p.bySize[r.Font.Size] += n
			p.byWeight[r.Font.Weight] += n

			key := fmt.Sprintf("%g|%s", r.Font.Size, r.Font.Family)
			s := p.byFont[key]
			if s == nil {
				s = &fontStat{
					size: r.Font.Size, family: r.Font.Family,
					weight: r.Font.Weight, oblique: r.Font.Oblique,
				}
				p.byFont[key] = s
			}
			s.runs++
			s.chars += n
			if r.Font.MarkedBold {
				s.markedBold++
			}
			if r.Font.MarkedItalic {
				s.markedItalic++
			}
		}
	}
	return p
}

// report logs the distribution in descending share of characters.
func (p *fontProfile) report(t *testing.T, name string) {
	t.Helper()
	t.Logf("%s: %d pages, %d runs, %d chars; %d runs with no resolvable font",
		name, p.pages, p.runs, p.chars, p.unresolved)

	stats := make([]*fontStat, 0, len(p.byFont))
	for _, s := range p.byFont {
		stats = append(stats, s)
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].chars > stats[j].chars })

	// Characters per run is logged because it is what separates a heading from
	// emphasis at the same size and weight: a heading is a short run, a bold lead-in
	// or a table label is shorter still, and body copy is long.
	t.Logf("  %-8s %-40s %-9s %8s %6s %8s %7s",
		"size", "family", "name-says", "chars", "share", "runs", "ch/run")
	shown := 0
	for _, s := range stats {
		if shown == 14 {
			t.Logf("  ... and %d more fonts", len(stats)-shown)
			break
		}
		shown++
		says := s.weight.String()
		if s.oblique {
			says += "+obl"
		}
		marks := ""
		if s.markedBold > 0 {
			marks += fmt.Sprintf(" <b> on %d/%d", s.markedBold, s.runs)
		}
		if s.markedItalic > 0 {
			marks += fmt.Sprintf(" <i> on %d/%d", s.markedItalic, s.runs)
		}
		t.Logf("  %-8g %-40s %-9s %8d %5.1f%% %8d %7.1f%s",
			s.size, s.family, says, s.chars, 100*float64(s.chars)/float64(p.chars),
			s.runs, float64(s.chars)/float64(s.runs), marks)
	}

	sizes := make([]float64, 0, len(p.bySize))
	for size := range p.bySize {
		sizes = append(sizes, size)
	}
	sort.Slice(sizes, func(i, j int) bool { return p.bySize[sizes[i]] > p.bySize[sizes[j]] })
	line := ""
	for i, size := range sizes {
		if i == 8 {
			break
		}
		line += fmt.Sprintf("  %g: %.1f%%", size, 100*float64(p.bySize[size])/float64(p.chars))
	}
	t.Logf("  chars by size:%s", line)

	// The two signals side by side. Which of them carries the document is not the
	// same on the two manuals, which is the finding that says neither can be
	// dropped: see the commit that added Font.
	boldChars, boldRuns := 0, 0
	for _, s := range p.byFont {
		if s.markedBold > 0 {
			boldRuns += s.markedBold
			boldChars += s.chars * s.markedBold / s.runs
		}
	}
	t.Logf("  poppler marked bold: %d runs, about %d chars (%.1f%%)",
		boldRuns, boldChars, 100*float64(boldChars)/float64(p.chars))

	for _, w := range []doc.Weight{doc.WeightUnknown, doc.WeightLight, doc.WeightRegular,
		doc.WeightMedium, doc.WeightSemibold, doc.WeightBold, doc.WeightHeavy} {
		if p.byWeight[w] == 0 {
			continue
		}
		t.Logf("  name says %-9s %8d chars %5.1f%%", w,
			p.byWeight[w], 100*float64(p.byWeight[w])/float64(p.chars))
	}
}

// markedBoldChars and markedBoldRuns count what poppler itself called bold.
func (p *fontProfile) markedBoldRuns() int {
	n := 0
	for _, s := range p.byFont {
		n += s.markedBold
	}
	return n
}

// heavierThanRegularChars is what the family names claim above regular weight —
// the signal poppler's markup does not carry.
func (p *fontProfile) heavierThanRegularChars() int {
	n := 0
	for w, chars := range p.byWeight {
		if w >= doc.WeightMedium {
			n += chars
		}
	}
	return n
}

// TestFontDistributionOfBothManuals records what the two real documents are set
// in, and asserts the parts a change must not break.
func TestFontDistributionOfBothManuals(t *testing.T) {
	if !extern.Available(extern.PDFToHTML) {
		t.Skip("pdftohtml is not installed")
	}

	_, columnPath := columnFixture(t)
	_, sequentialPath := loadFixture(t)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"parallel-columns manual", columnPath},
		{"sequential manual", sequentialPath},
	} {
		pages, err := doc.ExtractRuns(context.Background(), tc.path)
		if err != nil {
			t.Fatalf("%s: ExtractRuns: %v", tc.name, err)
		}
		p := profileFonts(pages)
		p.report(t, tc.name)

		// Every run of a real manual must resolve to a font. This is the assertion
		// that fails if the fontspec table is scoped to a page: poppler declares each
		// id once, on the page that first uses it, so 83% of the columns manual's
		// runs and 95% of the sequential manual's refer back to an earlier page.
		if p.unresolved != 0 {
			t.Errorf("%s: %d of %d runs have no font — a fontspec id is declared once "+
				"per document, on the page that first uses it, so the table cannot be "+
				"scoped to a page", tc.name, p.unresolved, p.runs)
		}

		// Both weight signals must keep contributing, for the same reason the column
		// language test checks both of its sources: either one collapsing to zero is
		// invisible in a total, and each catches emphasis the other cannot see.
		if got := p.markedBoldRuns(); got == 0 {
			t.Errorf("%s: poppler marked no run bold; it marks emphasis no family "+
				"name admits, such as the columns manual's FuturaBQ", tc.name)
		}
		if got := p.heavierThanRegularChars(); got*100/p.chars < 3 {
			t.Errorf("%s: only %d of %d chars are in a face whose name claims more "+
				"than regular weight; Medium alone is 17.2%% of the columns manual and "+
				"poppler marks none of it", tc.name, got, p.chars)
		}
	}
}
