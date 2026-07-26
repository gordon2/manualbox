package doc

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A block is one piece of readable content: a heading, a paragraph, a list item.
// See docs/design/conversion.md for the contract this implements.
//
// Blocks are built one region at a time, and that is the whole funnel: a
// household that reads German gets the German column of a page, not the page.
// [Region] is the unit because it is the unit the language map already stores.
//
// Two things about reading order had to be settled here rather than taken from
// the contract, and the second contradicts the obvious reading of it.
//
// **Down then across, not across then down.** `pdftotext -layout` reflows two
// side-by-side tables on the sequential manual's page 20 into one interleaved
// block, and the contract names that as the mistake to avoid.
//
// **A region is not narrow enough to sort inside.** The contract says reading
// order comes from the region, and for a parallel-columns page it does — a boxed
// region IS one text column. But regions.md rule 3 deliberately stores a page of
// several same-language columns as ONE whole-page region, so on those pages
// sorting inside the region is exactly the interleaving mistake, committed under
// another name. Measured on the column manual's page 62: a whole-page German
// region holding two text columns of prose, whose baselines do not even line up
// across the gutter (y=102 against y=102, then y=118 against y=120). Sorting its
// runs by y then x yields "Die Verpackung schützt..." followed by "Gerät
// Garantie gemäß nachstehenden Bedingungen:" — the left column's second line
// followed by the right column's. The sequential manual has the same shape on the
// 199 pages that read as three columns.
//
// So a region is subdivided by [DetectColumns] before anything is sorted, and
// reading order is column by column, left to right, down then across within each.
// The runs that span a gutter — a banner heading set across the measure — are read
// first, since that is where a banner is.
//
// # What this deliberately does not do
//
// Recorded so the next person does not think these are unsolved by accident. All
// were seen while checking the output against 108 dpi renders of the column
// manual's pages 62 and 14 and the sequential manual's pages 23 and 24.
//
// **Page furniture is not identified.** A printed language tab, a folio and a
// running head are text on the page, so they become blocks like anything else: the
// sequential manual's "DE" badge is 11pt medium beside a 17pt body and comes back
// as a level-2 heading on all 110 pages that print one, its folio comes back as a
// one-character paragraph, and the column manual's running head "D | Hinweis zur
// Entsorgung | Kundendienst | Garantie" comes back as a paragraph because it fills
// its measure. None of these can be told from content by anything on one page —
// the sequential manual genuinely heads sections "A", "B" and "C", so a
// two-letter line is not evidence of a tab. What identifies furniture is that it
// repeats in the same place on every page of a section, and that is a comparison
// across pages rather than a property of one, so it belongs to a later pass with
// the whole document in hand.
//
// **Hyphenation is not undone.** See [joinRuns] for the German counter-example
// that makes a trailing hyphen ambiguous.
//
// **A two-line heading set with generous leading becomes two headings.** Measured
// on the column manual's page 14: "Trockensaugen mit der DryBOX" and
// "(Zyklon-Filtertechnologie)" sit 24 units apart on a 16-unit pitch, so the same
// gap rule that separates paragraphs separates them. Merging headings across a
// wider gap than prose would need a second threshold with nothing to measure it
// against, since the only case in either document is this one.
//
// **A table is not a table.** Tables come from the ruled lines, which conversion.md
// records as a different input. The sequential manual's side-by-side troubleshooting
// tables therefore read down each cell column rather than across each row — not
// interleaved, which is the failure that matters, but not a table either.

// BlockKind is what a block is.
//
// A string for the same reason [Source] is one: it reaches a database column and
// a JSON payload, where "heading" survives a schema change and 1 does not.
type BlockKind string

const (
	// BlockHeading is a line, or a few, that titles what follows. See
	// [regionBody] for how it is told from body copy, which is the one decision
	// in this file with a document-wide consequence.
	BlockHeading BlockKind = "heading"
	// BlockParagraph is running prose, its printed line breaks removed.
	BlockParagraph BlockKind = "paragraph"
	// BlockListItem is one item of a list: a line that opens with a marker,
	// plus the lines indented under it.
	BlockListItem BlockKind = "list-item"

	// BlockTable and BlockFigure are declared and never produced here. Tables come
	// from the ruled lines, which are a different input — pdftocairo's strokes, not
	// the text geometry — and a figure needs the image list this pipeline does not
	// read. They are named now so that the kind vocabulary a reader and a database
	// column see does not have to change when that work lands, and so that nothing
	// downstream assumes three kinds is all there will ever be.
	BlockTable  BlockKind = "table"
	BlockFigure BlockKind = "figure"
)

// Block is one piece of readable content, in one language, on one page.
//
// The key is natural, not a surrogate: Page, RegionX0 and Index. Same reasoning
// as doc_regions, and the same reason — a job handler can run twice, and a
// surrogate ID would make the second conversion insert a parallel set instead of
// converging on the first. It is also what gives extraction the stable block IDs
// ingest.md asks for: "paragraph 4 of the German region of page 62" is a citation
// that survives a re-convert.
//
// RegionX0 rather than a region ID for the same reason [Region] has no ID: the
// left edge is what the page itself determines, so two runs of the pipeline over
// the same bytes agree on it without consulting anything stored.
type Block struct {
	// Page is the 1-based page number in the original PDF — the number printed on
	// the paper's own page furniture is a different thing and is not this.
	Page int
	// RegionX0 is the left edge of the region this came from, in the coordinate
	// space [ExtractRuns] reports. 0 for a whole-page region.
	RegionX0 float64
	// Index is the block's position within its region, from 0, in reading order.
	Index int

	// Kind is what the block is.
	Kind BlockKind
	// Level is the heading level, 1 for the most prominent, and 0 for anything
	// that is not a heading.
	//
	// It is derived from the region's own body face and reaches only two values:
	// 1 for a heading set larger than the body, 2 for one set at the body size and
	// merely heavier. That is as far as this evidence honestly goes. A document-wide
	// outline — this manual has four heading sizes, so level 3 exists — needs the
	// sizes of the whole document ranked together, which is a pass over every
	// region and not a property of one. Inventing more levels from one region's two
	// facts would put a number on the page that the next page contradicts.
	Level int

	// Text is the block's content, with the printed line breaks removed: a break
	// at the original measure is a property of the paper's column width, not of the
	// content, and a reader renders at a different width. Runs on one line are
	// joined the same way. See [joinRuns] for the one thing this loses.
	Text string
	// Lang is the region's language, empty where none was established. Carried on
	// the block so that a block is self-describing once it leaves the page it came
	// from — which is the state extraction and search will see it in.
	Lang string

	// X0, X1, Y0 and Y1 are the block's own bounding box, not the region's. A
	// caller wanting to draw the block on a `pdftoppm -r 108` render, which is what
	// checking this work by eye needs, uses these.
	X0, X1, Y0, Y1 float64
	// Lines is how many printed lines the block was folded from, and Chars its
	// rune count. Runes, not bytes, for the reason [Region.Chars] gives.
	Lines int
	Chars int
	// Note says in checkable terms why this block is the kind it is. The same
	// stance as [Region.Note] and [ColumnLayout.Note]: the evidence is countable and
	// a reader can hold it against the page.
	Note string
}

// Bounds on what a line, a paragraph break and a heading are.
//
// Every one is measured against both fixtures — the 68-page parallel-columns
// manual (testdata/fixtures/thomas-drybox-amfibia.json) and the 560-page
// sequential one (dreame-l40-ultra.json) — read through `pdftohtml -xml`, whose
// space matches a `pdftoppm -r 108` raster 1:1 so a block can be drawn on the
// page and looked at. Where a measurement did not support a clean threshold that
// is recorded here too, rather than a tuned number being left to look measured.
const (
	// paragraphGapFactor is how much bigger than the column's own line pitch a
	// vertical gap must be to end a paragraph.
	//
	// The pitch is measured per column, never assumed, because the two manuals set
	// different bodies at different leading: 14pt on a 16-unit pitch in the column
	// manual's prose columns, 17pt on 22.5 in the sequential manual's safety pages,
	// and 15 in the column manual's parts lists. A fixed pitch reads one document
	// and shreds the other.
	//
	// 1.2 comes from the tightest real break in either document. On the column
	// manual's page 62, whose German region is the acceptance target, the left
	// column runs body lines at a 16-unit pitch and separates its paragraphs and
	// its specification rows by 20 to 21 — "Typenbezeichnung: 788/M" at y=490,
	// "Spannungsversorgung: 230 V, 50 Hz" at 511, "Leistungsaufnahme:" at 531,
	// "Länge Stromzuleitung:" at 552. 1.2*16 = 19.2 splits all four and keeps every
	// 16-, 17- and 18-unit gap inside its paragraph. That matters more than it
	// looks: those four lines are the unruled specification table the contract
	// records as undetectable, and the contract's claim that such a page "still
	// reads correctly, as lines of text" is true only if each row is its own block.
	//
	// What this does NOT resolve is honest to state: two paragraphs 18 units apart
	// on the same page (y=357 and y=375 of that column, two separate sentences
	// about the service department) are 1.125 of the pitch and are folded into one
	// paragraph. No factor separates them from the 17- and 18-unit gaps that occur
	// inside paragraphs on the same page, so the choice is between losing that
	// break and inventing breaks inside prose, and losing the break is the smaller
	// error.
	paragraphGapFactor = 1.2

	// minPitchLines is how many line gaps a column needs before its own pitch is
	// believed. Below that the pitch falls back to the median line height, which is
	// the leading of a single-spaced setting to within a few per cent and is the
	// only evidence a two-line column offers.
	minPitchLines = 3

	// headingMaxMeasureFraction is how much of its column's measure a line may
	// occupy and still be a heading.
	//
	// Length is measured as a fraction of the measure rather than in characters,
	// which the contract states in characters per run. Both readings agree on the
	// documents — the column manual's headings are 17.8 characters a run against
	// 43.5 for its emphasis, the sequential manual's 15.6 against 65.2 — but a rune
	// count is not scale-free and not script-free: a CJK heading of six runes is
	// wide and a Thai one of forty is narrow, and this manual has sections in both.
	// The fraction says the same thing about the two measured manuals and keeps
	// saying it about a third.
	//
	// Measured, as a fraction of the column's own measure: the column manual's
	// page 62 sets "Hinweis zur Entsorgung" at 0.30 and "Technische Daten" at 0.22
	// against body lines at 0.97; the sequential manual's page 24 sets
	// "Sicherheitshinweise" at 0.25 against body lines at 0.98.
	//
	// **This is a soft cut and there is no gap to put it in.** That was measured
	// rather than hoped for, and it came out the wrong way. Taking every line that
	// passes the other two tests and histogramming its share of the measure gives a
	// smooth continuum from 5% to 100% on both documents — the column manual's 632
	// candidates run 33 at 60-64%, 25 at 65-69%, 17 at 70-74%, 116 at 95-99% — with
	// no trough anywhere. Counting runes instead, which is what the contract states,
	// is no better: 135 candidates at 50-59 runes and 104 at 60-69 against 133 at
	// 20-29, again with no valley. The reason is real and not an artifact. A manual
	// sets one-line paragraphs ("Saugen Sie im Trockensaugbetrieb keine Flüssigkeiten
	// auf.") and two-line headings ("Trockensaugen mit der AQUA-Box
	// (Wasserfilter-Technologie)"), so the two populations genuinely overlap.
	//
	// 0.6 is therefore chosen for precision rather than found in a gap, and the
	// asymmetry is deliberate: a false heading is visible wrong furniture in the
	// reader, while a missed heading degrades to a paragraph and still reads. What it
	// costs, measured: a genuine heading that fills a narrow column comes back as a
	// paragraph, because the measure is taken from the column and a column holding
	// nothing but its heading has the heading's own width. The sequential manual's
	// "Fehlersuche", "Feilsøking" and "Depanare" sit at 100% for exactly that reason
	// and are lost. The widest German heading that survives is "Trockensaugen mit der
	// AQUA-Box (Wasserfilter-Technologie)" at 60% — on the cut, which is what
	// TestBlocksHeadingLengthIsASoftCut pins so that the next person sees the cost
	// rather than rediscovering it.
	headingMaxMeasureFraction = 0.6

	// markerGapFactor is how wide the space after a list marker must be, against
	// the line's own text height, before the marker is read as a marker rather than
	// as the first word of a sentence.
	//
	// This is what makes a bare number a marker. The column manual's parts lists
	// print " 1 " at x=30 and its text at x=60 with no punctuation between them —
	// 20 units of gap against 17 of height, 1.2 — and without a gap test those
	// numbers are prose: measured, page 13's nine parts fold into one paragraph
	// reading "1 Крышка корпуса 2 Ручка для переноски 3 ...". A word space in these
	// faces is much narrower: the intra-line gap between runs that already carry a
	// space is 1.06 of the height at the 25th percentile on that manual and 0.0 on
	// the sequential one, so 0.5 is above a word space in both and below every
	// measured tab.
	markerGapFactor = 0.5

	// maxMarkerRunes is how long a leading token may be and still be a list
	// marker. Three digits covers "1." to "999.", and the measured manuals never
	// number past 21.
	maxMarkerRunes = 3

	// minHangingIndentFactor is how far right of a list item's marker a following
	// line must sit to be that item's own text. See [blocksOfColumn] for the three
	// measured indents it has to catch and the document shape that would defeat it.
	minHangingIndentFactor = 0.5
)

// bulletRunes are the characters that open a list item without needing a gap
// test, because none of them starts a word. Every one is in the two fixtures
// except the ASCII asterisk and hyphen, which are here because a manual written
// in a word processor uses them and excluding them would be a guess the other
// way.
const bulletRunes = "•·▪◦‣∙*-–—>»✓"

// RegionBlocks turns one region of one page into ordered readable blocks.
//
// p is the page's positioned text and r the region to read, which must be a
// region of that page. The result is in reading order with Index assigned from 0,
// and is empty for a region holding no usable text — a region of a diagram's
// callouts is a normal outcome, not a failure.
//
// Only the text inside the region's box is read, through the same two filters the
// region's own character count came from: [usableRuns] drops the sub-legible
// production slugs and the runs parked off the page, and [runsInBox] decides
// membership. Sharing those is not tidiness — a block built from runs the region
// did not count would put text in the reader that the gate never charged for, and
// on a parallel-columns page it would be text in a language nobody asked for.
func RegionBlocks(p *PageRuns, r *Region) []Block {
	var dropped DroppedRuns
	kept := usableRuns(p.Runs, p.Width, p.Height, &dropped)
	inside := runsInBox(kept, r.X0, r.X1)
	if len(inside) == 0 {
		return nil
	}

	tol := baselineToleranceFraction * medianHeight(inside)
	body := regionBody(inside)

	var out []Block
	for _, group := range readingGroups(inside, p) {
		lines := groupLines(group.runs, tol)
		if len(lines) == 0 {
			continue
		}
		pitch := columnPitch(lines)
		// Indexed rather than ranged by value: gocritic rejects copying a struct this
		// size per iteration, and CONTRIBUTING.md records why.
		blocks := blocksOfColumn(lines, pitch, group.measure, body)
		for i := range blocks {
			b := &blocks[i]
			b.Page = r.Page
			b.RegionX0 = r.X0
			b.Lang = r.Lang
			b.Index = len(out)
			out = append(out, *b)
		}
	}
	return out
}

// RegionsBlocks reads every region of a document that is in scope, in page order.
//
// inScope is keyed on base language, the same key [RegionChars] and ScopeFor use,
// for the same measured reason: a document printing CN, JA and ZH-HK counts as
// three languages under its labels and one under its base tags. A nil map reads
// every region, which is what a caller inspecting a whole document wants.
func RegionsBlocks(pages []PageRuns, regions []Region, inScope map[string]bool) []Block {
	byPage := make(map[int]*PageRuns, len(pages))
	for i := range pages {
		byPage[pages[i].No] = &pages[i]
	}

	// Regions arrive in page then left-edge order from PageRegions, but nothing
	// downstream promises to keep them that way once they have been through a
	// database, and the natural key is only stable if the order is.
	idx := make([]int, 0, len(regions))
	for i := range regions {
		if inScope != nil && !inScope[BaseLanguage(regions[i].Lang)] {
			continue
		}
		idx = append(idx, i)
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ra, rb := &regions[idx[a]], &regions[idx[b]]
		if ra.Page != rb.Page {
			return ra.Page < rb.Page
		}
		return ra.X0 < rb.X0
	})

	var out []Block
	for _, i := range idx {
		p := byPage[regions[i].Page]
		if p == nil {
			continue
		}
		out = append(out, RegionBlocks(p, &regions[i])...)
	}
	return out
}

// bodyFace is the size and weight most of a region's text is set in.
type bodyFace struct {
	size   float64
	weight Weight
	chars  int
}

// regionBody derives the body face from the region's own text.
//
// It is derived and not configured, and that is the decision the heading rule
// stands on. The whole-document body of the sequential manual is 11pt, 54.1% of
// its characters — but its safety pages carry no 11pt text at all. Page 24 is
// twenty-two lines of 17pt prose under one 21pt heading, and against a
// hard-coded 11pt every line of it is "larger than body". Against its own page
// the body is 17pt and only the heading stands above it. The column manual needs
// the same treatment from the other side: 84.0% of its characters are one size,
// so on most of its pages size discriminates nothing and only weight is left.
//
// Weight is taken as the character-weighted mode too, not the minimum or the
// mean, because the column manual's body face is FuturaCon-Lig — light — and
// 17.2% of the document is the same size in FuturaCon-Med. Reading the body as
// "light" is what leaves medium available as an emphasis signal; reading it as
// the lightest thing present would too, but reading it as the mean would not.
func regionBody(runs []TextRun) bodyFace {
	type face struct {
		size   float64
		weight Weight
	}
	chars := make(map[face]int, 8)
	for i := range runs {
		n := utf8.RuneCountInString(strings.TrimSpace(runs[i].Text))
		if n == 0 {
			continue
		}
		chars[face{runs[i].Font.Size, effectiveWeight(&runs[i].Font)}] += n
	}

	var best bodyFace
	for f, n := range chars {
		// Ties broken towards the smaller size, then the lighter weight, so that a
		// region split evenly between a heading face and a body face does not name
		// the heading face as the body and lose every heading on the page.
		switch {
		case n > best.chars,
			n == best.chars && f.size < best.size,
			n == best.chars && f.size == best.size && f.weight < best.weight:
			best = bodyFace{size: f.size, weight: f.weight, chars: n}
		}
	}
	return best
}

// effectiveWeight folds poppler's own markup into the weight the family name
// declares, taking whichever is heavier.
//
// Both signals are needed and neither can be dropped, which is measured and not a
// hedge: 93.4% of the column manual's characters are in a face whose name states a
// weight and poppler marks only 1.5% of them bold, while 73.2% of the sequential
// manual's are in a face called plainly "MiSans" that states nothing at all and
// poppler's markup is the only weight there is. See [Weight] for the counts.
//
// A marked run is read as semibold and not as bold because that is where poppler
// draws its line, measured over both documents: it wraps every run of every name
// saying Bold, Demibold, SemiBold or Xbold, and not one run of any name saying
// Medium. So <b> means "at least semibold" exactly, and promoting it to bold would
// make a semibold heading outrank a bold one.
func effectiveWeight(f *Font) Weight {
	w := f.Weight
	if f.MarkedBold && w < WeightSemibold {
		w = WeightSemibold
	}
	return w
}

// readingGroup is one strip of a region that can be sorted internally: a text
// column, or the band of runs that span across all of them.
type readingGroup struct {
	runs []TextRun
	// measure is the width the group's lines are set to — the column's own
	// measure, which is what a heading's length is judged against. Taken from the
	// column the detector reported rather than from the runs in hand, so that a
	// column holding nothing but two short headings is not judged to have a short
	// measure and both promoted.
	measure float64
}

// readingGroups subdivides a region into the strips reading order runs down.
//
// See the file comment for why a region is not itself such a strip. The spanning
// runs come first as one group of their own: a heading set across two columns is
// above both of them on the page, and putting it after either would read wrongly
// on the one page of the column manual that does it.
func readingGroups(inside []TextRun, p *PageRuns) []readingGroup {
	layout := DetectColumns(inside, p.Width, p.Height)
	if len(layout.Columns) < 2 {
		// One column, or too little text to call one. Either way the region is a
		// single strip and its measure is what its own text reaches.
		lo, hi := extent(inside)
		return []readingGroup{{runs: inside, measure: hi - lo}}
	}

	cols := layout.Columns
	groups := make([]readingGroup, len(cols)+1)
	groups[0].measure = func() float64 { lo, hi := extent(inside); return hi - lo }()
	for i := range cols {
		groups[i+1].measure = cols[i].Width()
	}

	for i := range inside {
		r := &inside[i]
		k := columnOf(r, cols)
		if k < 0 {
			// Spanning, or in a gap the detector did not report as a column: read it
			// with the banner band rather than dropping it. Losing text is never the
			// right answer here — the region's own character count included it.
			groups[0].runs = append(groups[0].runs, *r)
			continue
		}
		groups[k+1].runs = append(groups[k+1].runs, *r)
	}

	out := make([]readingGroup, 0, len(groups))
	for i := range groups {
		if len(groups[i].runs) > 0 {
			out = append(out, groups[i])
		}
	}
	return out
}

// columnOf places a run in the column that contains it, or -1 when it reaches
// past one. Membership is by containment and not by left edge, because a run
// straddling two columns belongs to neither and must be read with the banner band
// — the same distinction [crossesAny] draws, expressed against columns rather
// than gutters because that is what is in hand here.
func columnOf(r *TextRun, cols []Column) int {
	for i := range cols {
		if r.X >= cols[i].Min-1 && r.right() <= cols[i].Max+1 {
			return i
		}
	}
	return -1
}

// textLine is the runs of one baseline, in the order they are read.
type textLine struct {
	runs           []TextRun
	y, bottom      float64
	x0, x1         float64
	text           string
	chars          int
	size           float64
	weight         Weight
	marker         string
	markerRuneOnly bool
}

// groupLines folds runs onto shared baselines, then orders the baselines down the
// page and the runs across each one.
//
// The baseline rule is [sameBaseline], which is the rule columns.go already uses
// to fold a list marker into the text it labels — shared rather than restated,
// because two definitions of "these runs are one line" would drift and the second
// one would be the wrong one. The tolerance is small on purpose: runs of one line
// carry the same top to the unit in both documents, so it has rounding to absorb
// and nothing more.
func groupLines(runs []TextRun, tol float64) []textLine {
	ordered := make([]TextRun, len(runs))
	copy(ordered, runs)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Y != ordered[j].Y {
			return ordered[i].Y < ordered[j].Y
		}
		return ordered[i].X < ordered[j].X
	})

	var lines []textLine
	for i := range ordered {
		r := &ordered[i]
		// Compared against the line's first run, not its last, so that a line of many
		// runs cannot drift a tolerance at a time into the line below it.
		if n := len(lines); n > 0 && sameBaseline(lines[n-1].runs[0].Y, r.Y, tol) {
			lines[n-1].runs = append(lines[n-1].runs, *r)
			continue
		}
		lines = append(lines, textLine{runs: []TextRun{*r}})
	}

	for i := range lines {
		lines[i].finish()
	}
	return lines
}

// finish computes everything a line's runs imply, once they are all in.
func (l *textLine) finish() {
	sort.SliceStable(l.runs, func(i, j int) bool { return l.runs[i].X < l.runs[j].X })

	l.y, l.bottom = math.Inf(1), math.Inf(-1)
	l.x0, l.x1 = math.Inf(1), math.Inf(-1)
	sizes := make(map[float64]int, 4)
	weights := make(map[Weight]int, 4)
	for i := range l.runs {
		r := &l.runs[i]
		l.y = math.Min(l.y, r.Y)
		l.bottom = math.Max(l.bottom, r.bottom())
		l.x0 = math.Min(l.x0, r.X)
		l.x1 = math.Max(l.x1, r.right())
		n := utf8.RuneCountInString(strings.TrimSpace(r.Text))
		sizes[r.Font.Size] += n
		weights[effectiveWeight(&r.Font)] += n
	}

	// The line's face is its dominant one by characters, which is what makes a
	// bold lead-in inside a line of body copy not turn the line into a heading:
	// "Achtung: das Gerät nicht ..." is two runs and the light one is longer.
	l.size = dominantSize(sizes)
	l.weight = dominantWeight(weights)

	l.text = joinRuns(l.runs)
	l.chars = utf8.RuneCountInString(l.text)
	l.marker, l.markerRuneOnly = leadingMarker(l)
}

func dominantSize(sizes map[float64]int) float64 {
	var best float64
	bestN := -1
	for size, n := range sizes {
		if n > bestN || (n == bestN && size < best) {
			best, bestN = size, n
		}
	}
	return best
}

func dominantWeight(weights map[Weight]int) Weight {
	best, bestN := WeightUnknown, -1
	for w, n := range weights {
		if n > bestN || (n == bestN && w < best) {
			best, bestN = w, n
		}
	}
	return best
}

// joinRuns renders a line's runs as text, inserting a space only where the page
// shows one and neither run already carries it.
//
// Poppler splits a run at every font change, so a line reading "Sollte Ihr
// THOMAS DryBox einmal ausgedient haben" arrives as three runs, and gluing them
// blind produces "THOMASDryBoxeinmal". Measured over both documents, the gap
// between two runs with no whitespace on either side is above zero for the great
// majority — a quarter of them are already more than 1.8 heights apart on the
// column manual and 2.9 on the sequential one, because they are separate cells
// and labels rather than a split word. Where the gap is zero or negative the runs
// touch or overlap, and joining them directly is what the page shows.
//
// Zero is the threshold and it is not tuned: touching glyphs are one word and
// separated ones are two. That is the boundary the page draws.
//
// What this deliberately does not do is undo hyphenation. The column manual
// breaks "brud-/nej wody" and "Verpackungsmate-/rial" across lines, and joining
// leaves the hyphen in. Removing a trailing hyphen would be wrong at least as
// often: German prose in the same document ends a line with a hyphen that must
// stay — "Ein- und Ausschalten", "Elektro-Fachkräfte" — and telling a broken word
// from a compound needs a dictionary of the language, which is a later stage's
// evidence and not this one's.
func joinRuns(runs []TextRun) string {
	var b strings.Builder
	for i := range runs {
		if i > 0 {
			prev := &runs[i-1]
			gap := runs[i].X - prev.right()
			if gap > 0 && !endsWithSpace(prev.Text) && !startsWithSpace(runs[i].Text) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(runs[i].Text)
	}
	return collapseSpaces(b.String())
}

// hasLetter reports whether a string carries any letter at all. Format
// characters are stripped first for the reason [stripFormatting] gives: a
// right-to-left line wraps its Latin furniture in bidi controls, and those are
// neither letters nor digits.
func hasLetter(s string) bool {
	for _, r := range stripFormatting(s) {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func endsWithSpace(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}

func startsWithSpace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}

// collapseSpaces reduces every run of whitespace to one space and trims the ends.
// A tab stop is many spaces on the page and one on a screen, and a block that
// carries the page's own spacing cannot be rendered at another measure.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// leadingMarker reports the list marker a line opens with, if any.
//
// Two shapes, both measured. A bullet character needs nothing else: nothing that
// opens a sentence looks like one. A number or a letter needs either punctuation
// after it — "1." or "2)" — or, when the document prints neither, a gap to the
// text wide enough to be a tab rather than a word space. runeOnly reports which
// it was, because a bare number that was accepted on the strength of a gap is
// weaker evidence and is not allowed to break a paragraph on its own.
func leadingMarker(l *textLine) (marker string, runeOnly bool) {
	text := strings.TrimSpace(stripFormatting(l.text))
	if text == "" {
		return "", false
	}

	first, size := utf8.DecodeRuneInString(text)
	if strings.ContainsRune(bulletRunes, first) {
		return string(first), false
	}

	// A leading token of digits, or one letter, then optional punctuation.
	rest := text
	token := ""
	for rest != "" {
		r, n := utf8.DecodeRuneInString(rest)
		if !unicode.IsDigit(r) {
			break
		}
		token += string(r)
		rest = rest[n:]
		if utf8.RuneCountInString(token) > maxMarkerRunes {
			return "", false
		}
	}
	if token == "" {
		if !unicode.IsLetter(first) || utf8.RuneCountInString(text) < 2 {
			return "", false
		}
		// A single letter followed by punctuation: "a)" and "b)" lists.
		token, rest = string(first), text[size:]
		next, _ := utf8.DecodeRuneInString(rest)
		if next != ')' && next != '.' {
			return "", false
		}
	}

	if next, n := utf8.DecodeRuneInString(rest); next == '.' || next == ')' {
		return token + string(next), false
	} else if n > 0 && !unicode.IsSpace(next) {
		// "230 V" is not item 230; "2026" is not item 202.
		return "", false
	}

	// No punctuation, so the page must show a tab. Measured on the runs rather
	// than on the text, since the space in the text is one character however wide
	// the page sets it.
	if len(l.runs) < 2 {
		return "", false
	}
	gap := l.runs[1].X - l.runs[0].right()
	if gap < markerGapFactor*l.runs[0].Height {
		return "", false
	}
	// The marker has to be the whole of the first run, or the run is a sentence
	// that happens to start with a number.
	if strings.TrimSpace(stripFormatting(l.runs[0].Text)) != token {
		return "", false
	}
	return token, true
}

// columnPitch is the normal distance between one line and the next, measured from
// the column's own lines.
//
// The mode of the gaps, rounded to the unit, rather than the median. That is the
// one estimator the measurement forced: a column of prose broken into short
// paragraphs has nearly as many break gaps as body gaps, and the median of the
// column manual's page 62 left column comes out at 18 against a body pitch of 16,
// which is high enough that the 20- and 21-unit breaks it has to find fall inside
// 1.2 of it and are lost. The mode is 16, which is what the page is set on.
//
// Below [minPitchLines] gaps the mode of two numbers means nothing, so the pitch
// falls back to the median line height.
func columnPitch(lines []textLine) float64 {
	counts := make(map[int]int, len(lines))
	best, bestN := 0, 0
	gaps := 0
	for i := 1; i < len(lines); i++ {
		gap := lines[i].y - lines[i-1].y
		if gap <= 0 {
			continue
		}
		gaps++
		k := int(math.Round(gap))
		counts[k]++
		if counts[k] > bestN || (counts[k] == bestN && k < best) {
			best, bestN = k, counts[k]
		}
	}
	if gaps >= minPitchLines && best > 0 {
		return float64(best)
	}

	hs := make([]float64, 0, len(lines))
	for i := range lines {
		hs = append(hs, lines[i].bottom-lines[i].y)
	}
	sort.Float64s(hs)
	if len(hs) == 0 {
		return 1
	}
	return hs[len(hs)/2]
}

// blocksOfColumn folds one column's lines into blocks.
func blocksOfColumn(lines []textLine, pitch, measure float64, body bodyFace) []Block {
	var out []Block
	var cur *Block
	var curLines []textLine

	flush := func() {
		if cur == nil {
			return
		}
		texts := make([]string, 0, len(curLines))
		for i := range curLines {
			texts = append(texts, curLines[i].text)
		}
		cur.Text = collapseSpaces(strings.Join(texts, " "))
		cur.Chars = utf8.RuneCountInString(cur.Text)
		cur.Lines = len(curLines)
		out = append(out, *cur)
		cur, curLines = nil, nil
	}

	for i := range lines {
		l := &lines[i]
		if l.chars == 0 {
			continue
		}
		kind, level, note := classify(l, measure, body)

		// A line with no marker of its own, indented under the item above it and no
		// further down the page than one line, is that item's own text rather than a
		// paragraph after it. This is the hanging indent, and without it every
		// numbered clause of the column manual's guarantee comes back as a one-line
		// item followed by an orphaned paragraph: "1. Die Garantiezeit beträgt 24
		// Monate - gerechnet vom Liefertag an den ersten Endabnehmer. Sie reduziert",
		// then "sich bei gewerblicher Benutzung ..." as a block of its own.
		//
		// Measured, as a fraction of the line's own height, the indents that have to
		// be caught are 1.29 (the column manual's numbered clauses, marker at x=463
		// and text at 485 on a 17-unit line), 0.65 (its bulleted items, 43 and 54) and
		// 1.00 (the sequential manual's safety bullets, 55 and 77 on a 22-unit line).
		// 0.5 is below all three. Nothing has to be excluded above it, because neither
		// document indents the first line of a paragraph — checked over both, every
		// paragraph's first line is flush with its column — so an indent in these
		// manuals means a hanging one. A document that indents first lines instead
		// would fold each new paragraph into the item above it, and that is the stop
		// condition rather than a threshold to retune.
		if cur != nil && cur.Kind == BlockListItem && kind == BlockParagraph {
			prev := &curLines[len(curLines)-1]
			if l.x0 >= curLines[0].x0+minHangingIndentFactor*(l.bottom-l.y) &&
				l.y-prev.y <= paragraphGapFactor*pitch {
				kind, level, note = cur.Kind, cur.Level, cur.Note
			}
		}

		// A heading has to start a block. This is structural and not a threshold, and
		// it is the largest correction the measurement forced: the last line of a
		// paragraph is short by definition, so any paragraph set in a face heavier
		// than the region's body hands its final line over as a heading. That is not
		// a corner case on these documents — the column manual sets 17.2% of its
		// characters in FuturaCon-Med at the body size, and reading its lines
		// independently produced 280 headings reading "Umgebungen benutzt werden.",
		// "во взрывоопасных помещениях.", "gung durchgeführt werden." Requiring the
		// space above a heading that the typesetter put there removes all 280 and
		// costs nothing: measured over both manuals, every heading that survives is
		// separated from what precedes it by more than one line pitch, because that is
		// what a heading looks like on paper.
		if cur != nil && kind == BlockHeading && cur.Kind != BlockHeading &&
			l.y-curLines[len(curLines)-1].y <= paragraphGapFactor*pitch {
			kind, level, note = cur.Kind, cur.Level, cur.Note
		}

		start := cur == nil || kind != cur.Kind || level != cur.Level
		if !start {
			prev := &curLines[len(curLines)-1]
			switch {
			case l.y-prev.y > paragraphGapFactor*pitch:
				start = true
			case kind == BlockListItem && l.marker != "" && !l.markerRuneOnly:
				// A second marker is a second item. A bare number accepted on a gap
				// alone does not get this power: the column manual's specification rows
				// would each become an item of their own list.
				start = true
			case kind == BlockListItem && l.marker != "" && l.markerRuneOnly &&
				math.Abs(l.x0-curLines[0].x0) < 1:
				// ...unless it sits at exactly the indent the current item's marker does,
				// which is what a parts list numbered "1", "2", "3" with no punctuation
				// looks like and is the only thing that separates its entries.
				start = true
			}
		}
		if start {
			flush()
			cur = &Block{Kind: kind, Level: level, Note: note,
				X0: l.x0, X1: l.x1, Y0: l.y, Y1: l.bottom}
		}
		curLines = append(curLines, *l)
		cur.X0 = math.Min(cur.X0, l.x0)
		cur.X1 = math.Max(cur.X1, l.x1)
		cur.Y1 = math.Max(cur.Y1, l.bottom)
	}
	flush()
	return out
}

// classify decides what one line is.
//
// The order is deliberate and the first rule is the one that surprises: a line
// opening with a list marker is a list item even when it is set heavier than the
// body and short enough to be a heading. The column manual's pages 62 to 66 are
// full of exactly that — "• Entsorgung Reinigungsmittel" in FuturaCon-Med at 14pt,
// four to a page, each followed by its own paragraph. The marker is a fact about
// the content and the weight is a fact about the typesetting, so the marker wins.
// Checked against a 108 dpi render of page 62: they are bulleted items with a
// bold lead-in, which is what this calls them.
func classify(l *textLine, measure float64, body bodyFace) (kind BlockKind, level int, note string) {
	if l.marker != "" {
		return BlockListItem, 0, fmt.Sprintf("opens with the list marker %q", l.marker)
	}
	if level, note, ok := headingLevel(l, measure, body); ok {
		return BlockHeading, level, note
	}
	return BlockParagraph, 0, ""
}

// headingLevel decides whether a line is a heading, and how prominent.
//
// Weight and length, never size alone, and the counter-example is measured rather
// than feared: 14.1% of the sequential manual's characters are 17pt in a face
// whose name says nothing, at 65 characters a run, and they are safety prose.
// "Larger than the body means a heading" promotes every line of them. Its real
// headings are 15pt and 21pt semibold at 15.6 and 17.2 characters a run.
//
// So the three tests, in the order they eliminate:
//
//  1. It contains a letter. Not a threshold but a category, and it is the single
//     biggest source of false headings measured: the column manual's page 11 is an
//     exploded diagram whose 26 numeric callouts are set in FuturaCon-Med at 17pt
//     — larger AND heavier than that page's body, and two characters long, so they
//     pass every typographic test there is. They were 26 of the 134 headings this
//     found in the manual's German regions before this test existed. A figure
//     callout, a folio and a chapter number are numbers; a heading is words.
//  2. Heavier than the region's body face. This is the test that does the work,
//     and it is the whole reason [effectiveWeight] reads two signals: on the
//     column manual the family name carries it and on the sequential one only
//     poppler's markup does.
//  3. Short against its column's measure. This is what separates a heading from
//     emphasis set as a whole paragraph, which the column manual has 17.2% of.
//
// There is deliberately no floor on the size, and that was measured the other way
// round after being written in. The sequential manual's safety pages are set
// entirely in 17pt, so 17pt is their body, and their real subheadings —
// "Nutzungsbeschränkungen" on page 23 — are 15pt MiSans-Demibold: SMALLER than the
// body they head. Requiring a heading to be at least the body size loses them, and
// it protects against nothing, which is the part that had to be checked rather than
// assumed. The small bold text it looked like it was guarding against is 9pt
// Demibold at 8.0 characters a run on 215 pages, and every one of those is the word
// "Note:" or "Hinweis:" opening a paragraph — a lead-in run, not a line. The
// dominant-face rule in [textLine.finish] already excludes it, because the rest of
// its line is longer and lighter.
//
// Nothing else here reads the text. A heading is otherwise a typographic fact in
// these documents, and requiring, say, no terminal full stop would fail on
// "Wskazóki dotyczące utylizacji | Obsługa serwisowa | Gwarancja", which is a
// running head with two pipes in it.
func headingLevel(l *textLine, measure float64, body bodyFace) (level int, note string, ok bool) {
	if !hasLetter(l.text) {
		return 0, "", false
	}
	if l.weight <= body.weight {
		return 0, "", false
	}
	if measure > 0 && l.x1-l.x0 > headingMaxMeasureFraction*measure {
		return 0, "", false
	}

	level = 2
	if l.size > body.size {
		level = 1
	}
	return level, fmt.Sprintf("%gpt %s against a %gpt %s body, %.0f%% of the measure",
		l.size, l.weight, body.size, body.weight,
		100*(l.x1-l.x0)/math.Max(measure, 1)), true
}

// BlockSummary describes a document's blocks in one line, for logs and for a test
// that wants the shape rather than every row.
func BlockSummary(blocks []Block) string {
	if len(blocks) == 0 {
		return "no blocks"
	}
	byKind := make(map[BlockKind]int, 4)
	pages := make(map[int]bool, 64)
	chars, headings := 0, 0
	for i := range blocks {
		b := &blocks[i]
		byKind[b.Kind]++
		pages[b.Page] = true
		chars += b.Chars
		if b.Kind == BlockHeading {
			headings++
		}
	}
	parts := make([]string, 0, len(byKind))
	for _, k := range []BlockKind{BlockHeading, BlockParagraph, BlockListItem, BlockTable, BlockFigure} {
		if byKind[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", byKind[k], k))
		}
	}
	return fmt.Sprintf("%d blocks over %d pages, %d chars: %s",
		len(blocks), len(pages), chars, strings.Join(parts, ", "))
}
