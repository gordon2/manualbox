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

// Font is the typeface a run is set in, as far as poppler's XML reveals it.
//
// It exists because size alone cannot find a heading. Measured over both
// fixtures, whole-document, in characters — TestFontDistributionOfBothManuals
// prints all of this and more:
//
//	                        sequential manual       parallel-columns manual
//	the body size            11: 64.1% of chars     14: 84.0% of chars
//	the next size up         17: 14.7%              17:  5.5%
//	  ...which is set in     MiSans, regular        FuturaCon-Lig, the body face
//	heavier at the body size MiSans-Medium 5.1%     FuturaCon-Med 17.2%
//
// Row three is the trap [extern.PDFToHTML] records: the larger face is not a
// heading face, it is what the safety text is set in, so "larger than body means
// heading" promotes prose — and it is 14.7% of a 560-page manual. Row four is the
// other half of it, and it is worse on the columns manual, where 84.0% of the
// characters are one size and so size discriminates almost nothing: what
// separates that document's emphasis from its body is only the weight of the
// face at the same size.
//
// Weight and slope are therefore carried from two independent signals rather
// than one, because poppler reports them two ways and the two disagree in both
// directions. See [Weight] for the disagreement and its numbers.
type Font struct {
	// Size is the size poppler declares for the run's fontspec — in the same
	// 1.5-scaled space as the coordinates and [PageRuns.Height], not in the PDF's
	// own points. Measured by writing a known PDF: 11pt and 17pt text come back
	// as 17 and 26. So it is directly comparable to a run's Height and to the
	// page box, and a caller must not print it as a point size.
	//
	// Read as a float although poppler currently prints integers, for the reason
	// [xmlText] gives about coordinates.
	Size float64 `json:"size,omitempty"`
	// Family is the family verbatim, subset prefix and all — "HUTKLI+FuturaCon-Lig",
	// not "FuturaCon-Lig". Kept raw on purpose: [Weight] and Oblique below are
	// conclusions drawn from this string by a name heuristic, and a caller that
	// distrusts one of them must be able to see what it was concluded from.
	// The prefix is not noise either; it identifies the embedded subset, and two
	// subsets sharing a base name can differ in real weight (measured below).
	Family string `json:"family,omitempty"`
	// Weight is what the family name says the weight is. WeightUnknown when the
	// name says nothing, which is the zero value and the honest reading.
	Weight Weight `json:"weight,omitempty"`
	// Oblique is whether the family name says italic or oblique.
	Oblique bool `json:"oblique,omitempty"`
	// MarkedBold and MarkedItalic are poppler's own verdict: it wrapped the
	// run's text in <b> or <i>. That is a different signal from the two above,
	// not a restatement of them — poppler reads the font descriptor, so it
	// catches emphasis no name declares and misses emphasis every name declares.
	MarkedBold   bool `json:"markedBold,omitempty"`
	MarkedItalic bool `json:"markedItalic,omitempty"`
}

// Weight is a font weight as the embedded font's own name declares it.
//
// It is graded rather than a bold/not-bold pair, and that is the measured shape
// of the problem rather than a preference. Poppler's <b> markup is effectively
// boolean and it draws the line above Medium: counted per family across all its
// sizes, it wraps every run of a name that says Bold, Demibold, SemiBold or
// Xbold — FuturaCon-Bol 157 of 159 runs, Function-Xbold 81/81, MiSans-Demibold
// 2861/2861, MiSansLatin-Demibold 607/607, Arimo-SemiBold 89/89,
// Sarabun-SemiBold 105/105, Alibaba-PuHuiTi-B 23/23 — and not one run of a name
// that says Medium: FuturaCon-Med 0 of 1,241, MiSans-Medium 0 of 2,887,
// Arimo-Medium 0/248, Sarabun-Medium 0/157. On the columns manual that discards
// the single most useful distinction in the document, since FuturaCon-Med is
// 17.2% of its characters at the same size as its body text.
//
// The reverse disagreement is just as real, which is why the markup is kept too
// and neither signal is folded into the other. Poppler marks bold where no name
// admits it: FuturaBQ 78 of 78 runs, FuturaStd 11/11, Calibri 12/12, and 498 of
// the 16,426 runs whose family reads plainly "MiSans" — same base name,
// different embedded subsets, different actual weight. Slope disagrees both ways
// as well: <i> appears only on oblique-named families (Futura-BooObl 12/12,
// FuturaCon-MedObl 9/9, FuturaCon-BolObl 5/5), yet FuturaCon-BooObl gets 66 runs
// and not one <i>.
//
// What settles it is that the two manuals rely on opposite signals. On the
// columns manual the names carry the document — 93.4% of its characters are in a
// face whose name states a weight, and poppler marks only 1.5% of them bold. On
// the sequential manual it is the reverse: 73.2% of its characters are in a face
// whose name states nothing at all (MiSans, MiSansLatin, Sarabun, Arimo,
// HarmonyOS_Sans_Naskh_Arabic), and poppler's markup is the only weight there is.
// Either signal alone reads one of these two documents and is close to blind on
// the other, and there are two documents.
//
// So a name heuristic is exactly the sort of thing that misbehaves on the next
// document, and it is used here only because dropping it would discard the
// weight of 93% of one manual. It is reported beside Family and beside poppler's
// verdict, never instead of them, and no rule here decides what a heading is —
// that needs this data first.
type Weight int8

// The scale is the one type designers use, ordered so that comparing two weights
// is meaningful. WeightUnknown is deliberately below every named weight and is
// the zero value: a run whose font did not resolve, or whose name says nothing,
// must not compare as body weight.
const (
	WeightUnknown Weight = iota
	WeightLight
	WeightRegular
	WeightMedium
	WeightSemibold
	WeightBold
	WeightHeavy
)

func (w Weight) String() string {
	switch w {
	case WeightLight:
		return "light"
	case WeightRegular:
		return "regular"
	case WeightMedium:
		return "medium"
	case WeightSemibold:
		return "semibold"
	case WeightBold:
		return "bold"
	case WeightHeavy:
		return "heavy"
	default:
		return "unknown"
	}
}

// obliqueTails are the name fragments that mean a slanted face. Matched as a
// tail rather than a whole token because the two are glued in real names:
// "FuturaCon-BooObl" is Book plus oblique in one token, and stripping the tail
// is what leaves "Boo" behind to be read as a weight.
var obliqueTails = []string{"oblique", "italic", "ital", "obl"}

// weightTails maps a name fragment to a weight, longest and most specific
// first, because these are matched as tails of a token and the shorter ones are
// suffixes of the longer: read in the wrong order "Xbold" is bold and
// "Demibold" is bold. The abbreviations are all measured in the two fixtures —
// "Bol", "Lig", "Med", "Boo" — and "Boo" being one letter from "Bol" while
// meaning the opposite is why nothing here matches a prefix.
var weightTails = []struct {
	tail   string
	weight Weight
}{
	{"ultrabold", WeightHeavy},
	{"extrabold", WeightHeavy},
	{"semibold", WeightSemibold},
	{"demibold", WeightSemibold},
	{"xbold", WeightHeavy},
	{"black", WeightHeavy},
	{"heavy", WeightHeavy},
	{"bold", WeightBold},
	{"regular", WeightRegular},
	{"normal", WeightRegular},
	{"medium", WeightMedium},
	{"light", WeightLight},
	{"roman", WeightRegular},
	{"thin", WeightLight},
	{"book", WeightRegular},
	{"demi", WeightSemibold},
	{"semi", WeightSemibold},
	{"bol", WeightBold},
	{"boo", WeightRegular},
	{"lig", WeightLight},
	{"med", WeightMedium},
}

// weightLetters are the single-letter weight codes, matched only as a whole
// token. "Alibaba-PuHuiTi-B" and "-M" and "-R" are all in the sequential
// manual. They are excluded from tail matching because one letter at the end of
// a word means nothing: "FZYOUH_508R" is not regular.
var weightLetters = map[string]Weight{
	"b":  WeightBold,
	"sb": WeightSemibold,
	"db": WeightSemibold,
	"m":  WeightMedium,
	"r":  WeightRegular,
}

// parseFamily reads what a font's own name admits about its weight and slope.
//
// The subset prefix is dropped for matching only. Tokens are read right to left
// because the weight is the last thing in every name measured here, and the
// first match wins so that "MiSansLatin-Demibold" is semibold rather than
// stopping at the family.
func parseFamily(family string) (w Weight, oblique bool) {
	name := family
	if plus := strings.IndexByte(name, '+'); plus >= 0 {
		name = name[plus+1:]
	}
	tokens := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})

	for i := len(tokens) - 1; i >= 0; i-- {
		tok := strings.ToLower(tokens[i])

		for _, tail := range obliqueTails {
			if strings.HasSuffix(tok, tail) {
				oblique = true
				tok = strings.TrimSuffix(tok, tail)
				break
			}
		}
		if tok == "" {
			continue
		}
		if w != WeightUnknown {
			continue
		}
		if letter, ok := weightLetters[tok]; ok {
			w = letter
			continue
		}
		for _, wt := range weightTails {
			if strings.HasSuffix(tok, wt.tail) {
				w = wt.weight
				break
			}
		}
	}
	return w, oblique
}

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

	// One font table for the whole document, filled as the pages are walked.
	//
	// This is the opposite of what the format's per-page <fontspec> elements
	// suggest, and it is measured rather than assumed. Poppler 26.07.0 allocates
	// ids once per document and declares each one on the page that first uses it:
	// the columns manual declares 83 ids, 0-82 contiguous, spread over 29 of its
	// 68 pages, and the sequential manual 309 ids, 0-308, over 167 of its 560 —
	// with not one id redeclared anywhere in either document. Every later page
	// refers back to ids declared before it, so a table scoped to a single page
	// resolves nothing for most of the document: it leaves 6,201 of the columns
	// manual's 7,493 runs and 32,858 of the sequential manual's 34,413 runs with
	// no font at all, 83% and 95%. Carried forward, both resolve completely.
	//
	// A page that does redeclare an id still wins for its own runs and every page
	// after it, because its declaration overwrites the entry before its text is
	// read — measured, no page in either document does, but that is the ordering
	// the format implies and it costs nothing to honour. Poppler emits a page's
	// fontspecs before any of its text, checked on both documents, so reading them
	// first here is faithful to the stream rather than a reordering of it.
	fonts := make(map[int]Font, 16)

	pages := make([]PageRuns, 0, len(doc.Pages))
	for i := range doc.Pages {
		p := &doc.Pages[i]
		for j := range p.Fonts {
			spec := &p.Fonts[j]
			weight, oblique := parseFamily(spec.Family)
			fonts[spec.ID] = Font{
				Size:    spec.Size,
				Family:  spec.Family,
				Weight:  weight,
				Oblique: oblique,
			}
		}
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
			// An unresolvable id leaves the size and family zero rather than
			// failing the document: a font is an enrichment, and a page of text
			// with no fontspec still has to reach the language map. The markup
			// verdict is attached either way — it is poppler's, not the table's.
			font := fonts[t.Font]
			font.MarkedBold, font.MarkedItalic = t.MarkedBold, t.MarkedItalic
			out.Runs = append(out.Runs, TextRun{
				X: t.Left, Y: t.Top, Width: t.Width, Height: t.Height, Text: t.Text,
				Font: font,
			})
		}
		pages = append(pages, out)
	}
	return pages, nil
}

// pdfXML mirrors the part of poppler's pdf2xml output this code reads. Images
// and the producer are ignored; the geometry, the characters and the font are
// what the pipeline reads.
type pdfXML struct {
	Pages []xmlPage `xml:"page"`
}

type xmlPage struct {
	Number int           `xml:"number,attr"`
	Width  float64       `xml:"width,attr"`
	Height float64       `xml:"height,attr"`
	Fonts  []xmlFontSpec `xml:"fontspec"`
	Texts  []xmlText     `xml:"text"`
}

// xmlFontSpec is one <fontspec> declaration: the table a run's font attribute
// indexes into. See parsePDFXML for the measured scope of the id.
//
// The color attribute is deliberately not read. It would separate white label
// text over a diagram from body copy, which is a real distinction, but nothing
// needs it yet and an unused field invites a caller to trust it untested.
type xmlFontSpec struct {
	ID     int     `xml:"id,attr"`
	Size   float64 `xml:"size,attr"`
	Family string  `xml:"family,attr"`
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
	// Font is the fontspec id from the font attribute, or -1 where the attribute
	// is absent. Absent must not read as 0, because poppler numbers fontspec ids
	// from 0 and that is a real font. Measured: every one of the columns manual's
	// 7,493 runs and the sequential manual's 34,413 carries the attribute.
	Font int
	// MarkedBold and MarkedItalic record that the whole run was wrapped in <b>
	// or <i>. See UnmarshalXML for what "whole" is doing there.
	MarkedBold   bool
	MarkedItalic bool
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
//
// The same walk now also notes which element did the wrapping, since that is
// poppler's own verdict on the run's weight and slope — see [Weight] for why it
// is kept alongside the family name rather than instead of it. Nesting is real:
// 5 runs of the columns manual are <i><b>...</b></i>, so both are recorded, at
// any depth, rather than only the outermost.
//
// A style is recorded only when the wrapper encloses the entire run. That is the
// measured shape — all 355 styled runs of the columns manual and all 4,336 of the
// sequential manual are wrapped whole, none partially — and it keeps the mixed
// case honest: a run reading `plain <i>word</i> plain` is not an italic run, and
// calling it one would label a whole line by one word of it.
func (t *xmlText) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	t.Font = -1
	for _, attr := range start.Attr {
		if attr.Name.Local == "font" {
			if _, err := fmt.Sscanf(attr.Value, "%d", &t.Font); err != nil {
				return fmt.Errorf("text font=%q: %w", attr.Value, err)
			}
			continue
		}
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

	var text, outside strings.Builder
	var bold, italic bool
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
			if depth == 0 {
				// Characters outside every wrapper mean the run is only partly
				// styled, so no wrapper speaks for the whole of it.
				outside.Write(v)
			}
		case xml.StartElement:
			switch v.Name.Local {
			case "b":
				bold = true
			case "i":
				italic = true
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				t.Text = text.String()
				if strings.TrimSpace(outside.String()) == "" {
					t.MarkedBold, t.MarkedItalic = bold, italic
				}
				return nil
			}
			depth--
		}
	}
}
