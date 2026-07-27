package registry_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// newBlobStore is a blob store in a temp directory. Figures are the first thing in
// the pipeline that writes derived bytes there, so every test here needs one.
func newBlobStore(t *testing.T) *store.Store {
	t.Helper()
	blobs, err := store.New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}
	return blobs
}

// pngBytes makes a real PNG in memory, because no image may be committed to this
// repository. A distinct size gives distinct bytes and therefore a distinct
// digest, which is what lets a test tell two figures apart by their content.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.Gray{Y: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// figure builds a rendered figure the way doc.PageFigures returns one: bytes,
// pixel size and the digest of exactly those bytes.
func figure(t *testing.T, page, index, w, h int) doc.Figure {
	t.Helper()
	raw := pngBytes(t, w, h)
	sum := sha256.Sum256(raw)
	return doc.Figure{
		Page: page, Index: index,
		Rect:         doc.CellRect{X0: 43.5, Y0: 200.25, X1: 300.75, Y1: 460.5},
		Ink:          42,
		TextFraction: 0.0234,
		DPI:          216,
		PixelWidth:   w, PixelHeight: h,
		Digest: hex.EncodeToString(sum[:]),
		PNG:    raw,
	}
}

func TestConversionRoundTrip(t *testing.T) {
	// Every field, because a block that survives with the wrong box or the wrong
	// character count is worse than one that fails to save: it reads as
	// authoritative. The fractional coordinates are the point of half of this --
	// region_x0 is rounded on the way in because it is in the primary key, and the
	// block's own box is not rounded at all because nothing keys on it.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "a1")

	blocks := []doc.Block{
		{
			Page: 62, RegionX0: 42.5, Index: 0,
			Kind: doc.BlockHeading, Level: 1,
			Text: "Hinweis zur Entsorgung", Lang: "de",
			X0: 43.2, X1: 300.8, Y0: 100.5, Y1: 118.5,
			Lines: 1, Chars: 22,
			Note: "18pt bold at 22 characters, 0.29 of the measure",
		},
		{
			Page: 62, RegionX0: 42.5, Index: 1,
			Kind: doc.BlockParagraph,
			Text: "Die Verpackung schuetzt das Geraet vor Transportschaeden.", Lang: "de",
			X0: 43.2, X1: 304.9, Y0: 122.0, Y1: 170.75,
			Lines: 3, Chars: 57,
		},
		{
			Page: 62, RegionX0: 42.5, Index: 2,
			Kind: doc.BlockListItem,
			Text: "Typenbezeichnung: 788/M", Lang: "de",
			X0: 43.2, X1: 250.1, Y0: 490.0, Y1: 506.0,
			Lines: 1, Chars: 23,
			Note: "opens with a marker",
		},
		{
			Page: 57, RegionX0: 0, Index: 0,
			Kind: doc.BlockTable,
			Text: "Spannungsversorgung", Lang: "de",
			X0: 29.7, X1: 173.3, Y0: 210.0, Y1: 226.0,
			Lines: 1, Chars: 19,
			Note: "row 1, column 1 of 2",
		},
	}
	figures := []doc.Figure{figure(t, 57, 0, 12, 9)}

	if err := s.SaveConversion(ctx, docID, blocks, figures, blobs, registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}

	got, err := s.Blocks(ctx, docID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	want := []registry.Block{
		// Page 57 sorts before page 62; within page 62 the region at x0 43 holds
		// three blocks in index order.
		{Page: 57, RegionX0: 0, Index: 0, Kind: "table", Text: "Spannungsversorgung",
			Lang: "de", Name: "German", X0: 29.7, X1: 173.3, Y0: 210.0, Y1: 226.0,
			Lines: 1, Chars: 19, Note: "row 1, column 1 of 2"},
		// 42.5 rounds to 43 rather than truncating to 42, which is what makes this
		// value the same one doc_regions stores for the same column.
		{Page: 62, RegionX0: 43, Index: 0, Kind: "heading", Level: 1,
			Text: "Hinweis zur Entsorgung", Lang: "de", Name: "German",
			X0: 43.2, X1: 300.8, Y0: 100.5, Y1: 118.5, Lines: 1, Chars: 22,
			Note: "18pt bold at 22 characters, 0.29 of the measure"},
		{Page: 62, RegionX0: 43, Index: 1, Kind: "paragraph",
			Text: "Die Verpackung schuetzt das Geraet vor Transportschaeden.",
			Lang: "de", Name: "German", X0: 43.2, X1: 304.9, Y0: 122.0, Y1: 170.75,
			Lines: 3, Chars: 57},
		{Page: 62, RegionX0: 43, Index: 2, Kind: "list-item",
			Text: "Typenbezeichnung: 788/M", Lang: "de", Name: "German",
			X0: 43.2, X1: 250.1, Y0: 490.0, Y1: 506.0, Lines: 1, Chars: 23,
			Note: "opens with a marker"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}

	gotFigs, err := s.Figures(ctx, docID)
	if err != nil {
		t.Fatalf("figures: %v", err)
	}
	wantFigs := []registry.Figure{{
		Page: 57, Index: 0,
		X0: 43.5, Y0: 200.25, X1: 300.75, Y1: 460.5,
		Ink: 42, TextFraction: 0.0234,
		DPI: 216, PixelWidth: 12, PixelHeight: 9,
		SHA256: figures[0].Digest,
	}}
	if len(gotFigs) != 1 {
		t.Fatalf("got %d figures, want 1: %+v", len(gotFigs), gotFigs)
	}
	if gotFigs[0] != wantFigs[0] {
		t.Errorf("figure:\n got %+v\nwant %+v", gotFigs[0], wantFigs[0])
	}

	// The state moved in the same transaction as the content that justifies it.
	document, err := s.GetDocument(ctx, docID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if document.State != registry.StateReady {
		t.Errorf("state = %q, want %q", document.State, registry.StateReady)
	}
}

func TestFigureBytesLandInTheBlobStore(t *testing.T) {
	// A figure's row is a digest and nothing else, so the bytes have to be
	// somewhere. If they are not in the store, every reader shows a broken picture
	// and no test that only reads rows back would notice.
	s, database := newServiceWithDB(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "b2")

	// Two different pictures, so a store holding one set of bytes twice cannot pass.
	first, second := figure(t, 11, 0, 12, 9), figure(t, 11, 1, 20, 30)
	if first.Digest == second.Digest {
		t.Fatal("the two figures have the same bytes; this test cannot tell them apart")
	}

	if err := s.SaveConversion(ctx, docID, nil, []doc.Figure{first, second}, blobs,
		registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}

	for _, want := range []doc.Figure{first, second} {
		if !blobs.Exists(want.Digest) {
			t.Errorf("the blob store has no %s; the figure's bytes were not written", want.Digest[:8])
			continue
		}
		// Read them back and digest them again: the row must point at the bytes it
		// describes, not merely at a file of the right name.
		raw, err := blobs.ReadAll(want.Digest)
		if err != nil {
			t.Errorf("read %s: %v", want.Digest[:8], err)
			continue
		}
		if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != want.Digest {
			t.Errorf("the bytes stored as %s digest as something else", want.Digest[:8])
		}
		if !bytes.Equal(raw, want.PNG) {
			t.Errorf("the bytes stored as %s are not the figure's PNG", want.Digest[:8])
		}
	}

	// And the blobs table knows about them, which is the foreign key doc_figures
	// holds. A figure blob is also indexed with its real media type rather than
	// inheriting the document's.
	for _, want := range []doc.Figure{first, second} {
		row, err := gen.New(database.Read()).GetBlob(ctx, want.Digest)
		if err != nil {
			t.Errorf("blobs row for %s: %v", want.Digest[:8], err)
			continue
		}
		if row.MediaType != "image/png" {
			t.Errorf("blob %s media type = %q, want image/png", want.Digest[:8], row.MediaType)
		}
		if row.SizeBytes != int64(len(want.PNG)) {
			t.Errorf("blob %s size = %d, want %d", want.Digest[:8], row.SizeBytes, len(want.PNG))
		}
	}

	// The rows reference the digests, in page reading order.
	got, err := s.Figures(ctx, docID)
	if err != nil {
		t.Fatalf("figures: %v", err)
	}
	if len(got) != 2 || got[0].SHA256 != first.Digest || got[1].SHA256 != second.Digest {
		t.Errorf("figure rows reference %+v; want %s and %s",
			got, first.Digest[:8], second.Digest[:8])
	}
}

func TestAFigureThatWasNeverRenderedIsRejected(t *testing.T) {
	// doc.FindFigures is pure geometry and renders nothing; doc.PageFigures renders.
	// Substituting one for the other is easy and the result would be undetectable
	// afterwards: a complete row pointing at a blob that does not exist.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "c3")

	found := doc.Figure{Page: 11, Index: 0, Ink: 42,
		Rect: doc.CellRect{X0: 43, Y0: 200, X1: 300, Y1: 460}}
	err := s.SaveConversion(ctx, docID, nil, []doc.Figure{found}, blobs, registry.StateReady)
	if !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("saving an unrendered figure returned %v, want ErrInvalid", err)
	}

	// And nothing was written: the rejection happens before the transaction opens.
	if got, err := s.Figures(ctx, docID); err != nil || len(got) != 0 {
		t.Errorf("figures = %+v, %v; want none", got, err)
	}
}

func TestSavingTheSameConversionTwiceLeavesTheSameRows(t *testing.T) {
	// A conversion job can run twice: a worker may die after doing the work but
	// before recording success, and the reclaimed job runs again. The second run
	// must converge on the same rows rather than duplicating them. Values, not
	// counts -- a converging count with a corrupted row would pass a count check.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "d4")

	blocks := []doc.Block{
		{Page: 62, RegionX0: 42.5, Index: 0, Kind: doc.BlockHeading, Level: 1,
			Text: "Garantie", Lang: "de", X0: 43, X1: 200, Y0: 100, Y1: 118,
			Lines: 1, Chars: 8},
		{Page: 62, RegionX0: 42.5, Index: 1, Kind: doc.BlockParagraph,
			Text: "Gemaess nachstehenden Bedingungen.", Lang: "de",
			X0: 43, X1: 305, Y0: 122, Y1: 170, Lines: 2, Chars: 34},
		{Page: 62, RegionX0: 463, Index: 0, Kind: doc.BlockParagraph,
			Text: "Kundendienst.", Lang: "de", X0: 463, X1: 720, Y0: 100, Y1: 118,
			Lines: 1, Chars: 13},
	}
	figures := []doc.Figure{figure(t, 62, 0, 12, 9), figure(t, 62, 1, 20, 30)}

	if err := s.SaveConversion(ctx, docID, blocks, figures, blobs, registry.StateReady); err != nil {
		t.Fatalf("save first conversion: %v", err)
	}
	firstBlocks, err := s.Blocks(ctx, docID)
	if err != nil {
		t.Fatalf("blocks after first conversion: %v", err)
	}
	firstFigures, err := s.Figures(ctx, docID)
	if err != nil {
		t.Fatalf("figures after first conversion: %v", err)
	}

	if err := s.SaveConversion(ctx, docID, blocks, figures, blobs, registry.StateReady); err != nil {
		t.Fatalf("save second conversion: %v", err)
	}
	secondBlocks, err := s.Blocks(ctx, docID)
	if err != nil {
		t.Fatalf("blocks after second conversion: %v", err)
	}
	secondFigures, err := s.Figures(ctx, docID)
	if err != nil {
		t.Fatalf("figures after second conversion: %v", err)
	}

	if len(firstBlocks) != 3 {
		t.Fatalf("first conversion stored %d blocks, want 3", len(firstBlocks))
	}
	if len(secondBlocks) != len(firstBlocks) {
		t.Fatalf("re-converting changed the block count from %d to %d",
			len(firstBlocks), len(secondBlocks))
	}
	for i := range firstBlocks {
		if firstBlocks[i] != secondBlocks[i] {
			t.Errorf("block %d changed on re-convert:\nfirst  %+v\nsecond %+v",
				i, firstBlocks[i], secondBlocks[i])
		}
	}

	if len(firstFigures) != 2 || len(secondFigures) != len(firstFigures) {
		t.Fatalf("re-converting changed the figure count from %d to %d",
			len(firstFigures), len(secondFigures))
	}
	for i := range firstFigures {
		if firstFigures[i] != secondFigures[i] {
			t.Errorf("figure %d changed on re-convert:\nfirst  %+v\nsecond %+v",
				i, firstFigures[i], secondFigures[i])
		}
	}
}

func TestAReconversionThatProducesFewerBlocksLeavesNoStaleRow(t *testing.T) {
	// THIS IS THE CASE THE WHOLESALE REPLACE EXISTS FOR, and the reason
	// SaveConversion deletes before inserting.
	//
	// Indices run consecutively from 0 within a region, so a region that converted
	// to 4 blocks and now converts to 2 leaves rows at idx 2 and 3. An upsert alone
	// cannot remove them, because an upsert only ever touches the keys it is given.
	// What a reader then shows is two paragraphs of the PREVIOUS run's text, in
	// order, at the end of the region, indistinguishable from content -- which is
	// worse than a crash, because nothing reports it.
	//
	// It is not a hypothetical. Every threshold in internal/doc's block builder is
	// one measurement away from moving and most of them merge: the paragraph gap
	// factor folds two paragraphs into one, and the heading share cut turns two
	// headings into one.
	//
	// If this test still passes with the delete removed from saveBlocks, it is not
	// testing what it claims.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "e5")

	four := []doc.Block{
		{Page: 62, RegionX0: 43, Index: 0, Kind: doc.BlockParagraph, Text: "Erstens.",
			Lang: "de", X0: 43, X1: 305, Y0: 100, Y1: 116, Lines: 1, Chars: 8},
		{Page: 62, RegionX0: 43, Index: 1, Kind: doc.BlockParagraph, Text: "Zweitens.",
			Lang: "de", X0: 43, X1: 305, Y0: 120, Y1: 136, Lines: 1, Chars: 9},
		{Page: 62, RegionX0: 43, Index: 2, Kind: doc.BlockParagraph, Text: "Drittens.",
			Lang: "de", X0: 43, X1: 305, Y0: 140, Y1: 156, Lines: 1, Chars: 9},
		{Page: 62, RegionX0: 43, Index: 3, Kind: doc.BlockParagraph, Text: "Viertens.",
			Lang: "de", X0: 43, X1: 305, Y0: 160, Y1: 176, Lines: 1, Chars: 9},
	}
	if err := s.SaveConversion(ctx, docID, four, nil, blobs, registry.StateReady); err != nil {
		t.Fatalf("save first conversion: %v", err)
	}

	// The gap factor moved and the four paragraphs folded into two.
	two := []doc.Block{
		{Page: 62, RegionX0: 43, Index: 0, Kind: doc.BlockParagraph,
			Text: "Erstens. Zweitens.", Lang: "de",
			X0: 43, X1: 305, Y0: 100, Y1: 136, Lines: 2, Chars: 18},
		{Page: 62, RegionX0: 43, Index: 1, Kind: doc.BlockParagraph,
			Text: "Drittens. Viertens.", Lang: "de",
			X0: 43, X1: 305, Y0: 140, Y1: 176, Lines: 2, Chars: 19},
	}
	if err := s.SaveConversion(ctx, docID, two, nil, blobs, registry.StateReady); err != nil {
		t.Fatalf("save re-conversion: %v", err)
	}

	got, err := s.Blocks(ctx, docID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d blocks after a re-conversion that produced 2, want 2 -- the tail of "+
			"the previous run lingered and reads as content: %+v", len(got), got)
	}
	for i := range got {
		if got[i].Text != two[i].Text {
			t.Errorf("block %d text = %q, want %q", i, got[i].Text, two[i].Text)
		}
	}

	// The same, one table down: a figure the trim rule now rejects must disappear.
	if err := s.SaveConversion(ctx, docID, two,
		[]doc.Figure{figure(t, 62, 0, 12, 9), figure(t, 62, 1, 20, 30)},
		blobs, registry.StateReady); err != nil {
		t.Fatalf("save conversion with two figures: %v", err)
	}
	if err := s.SaveConversion(ctx, docID, two, []doc.Figure{figure(t, 62, 0, 12, 9)},
		blobs, registry.StateReady); err != nil {
		t.Fatalf("save conversion with one figure: %v", err)
	}
	gotFigs, err := s.Figures(ctx, docID)
	if err != nil {
		t.Fatalf("figures: %v", err)
	}
	if len(gotFigs) != 1 {
		t.Errorf("got %d figures after a re-conversion that produced 1, want 1 -- a rejected "+
			"figure outlived the run that rejected it: %+v", len(gotFigs), gotFigs)
	}
}

func TestTwoLanguagesOnOnePageAreKeptApartByTheirRegion(t *testing.T) {
	// THIS IS THE doc_regions COLLISION, ONE LEVEL DOWN. A parallel-columns page
	// sets several languages side by side, and their blocks share a page and an
	// index: block 0 of the German column and block 0 of the Polish column are both
	// "page 62, index 0". Only region_x0 tells them apart, which is exactly why it
	// is in the key -- without it one column silently overwrites the other and the
	// funnel returns half a page.
	//
	// The coordinates are the columns manual's real page 2 edges.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "f6")

	blocks := []doc.Block{
		{Page: 2, RegionX0: 42.5, Index: 0, Kind: doc.BlockHeading, Level: 1,
			Text: "Sicherheitshinweise", Lang: "de",
			X0: 43, X1: 305, Y0: 100, Y1: 118, Lines: 1, Chars: 19},
		{Page: 2, RegionX0: 42.5, Index: 1, Kind: doc.BlockParagraph,
			Text: "Lesen Sie diese Anleitung.", Lang: "de",
			X0: 43, X1: 305, Y0: 122, Y1: 170, Lines: 3, Chars: 26},
		{Page: 2, RegionX0: 322.6, Index: 0, Kind: doc.BlockHeading, Level: 1,
			Text: "Wskazowki bezpieczenstwa", Lang: "pl",
			X0: 323, X1: 585, Y0: 100, Y1: 118, Lines: 1, Chars: 24},
		{Page: 2, RegionX0: 322.6, Index: 1, Kind: doc.BlockParagraph,
			Text: "Przeczytaj te instrukcje.", Lang: "pl",
			X0: 323, X1: 585, Y0: 122, Y1: 170, Lines: 3, Chars: 25},
	}
	if err := s.SaveConversion(ctx, docID, blocks, nil, blobs, registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}

	got, err := s.Blocks(ctx, docID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d blocks on page 2, want 4 -- a column was lost to an index "+
			"collision: %+v", len(got), got)
	}
	// Left column first, then right, and index order within each.
	if got[0].RegionX0 != 43 || got[1].RegionX0 != 43 ||
		got[2].RegionX0 != 323 || got[3].RegionX0 != 323 {
		t.Errorf("the columns are not ordered left to right: %+v", got)
	}
	if got[0].Index != 0 || got[1].Index != 1 || got[2].Index != 0 || got[3].Index != 1 {
		t.Errorf("the indices are not in order within each column: %+v", got)
	}
	if got[0].Text != "Sicherheitshinweise" || got[2].Text != "Wskazowki bezpieczenstwa" {
		t.Errorf("index 0 of each column is not distinct: %q and %q", got[0].Text, got[2].Text)
	}

	// And the funnel: asking for German returns the German column and no Polish at
	// all, which conversion.md calls the one failure a reader would notice
	// immediately.
	german, err := s.BlocksByLang(ctx, docID, "de")
	if err != nil {
		t.Fatalf("blocks by lang: %v", err)
	}
	if len(german) != 2 {
		t.Fatalf("German has %d blocks, want 2: %+v", len(german), german)
	}
	for i := range german {
		if german[i].Lang != "de" {
			t.Errorf("a %s block came back in the German conversion: %+v",
				german[i].Lang, german[i])
		}
	}
	if german[0].Text != "Sicherheitshinweise" || german[1].Text != "Lesen Sie diese Anleitung." {
		t.Errorf("the German column is not in reading order: %+v", german)
	}

	// One page's blocks, both columns, which is what a page view asks for.
	onPage, err := s.BlocksForPage(ctx, docID, 2)
	if err != nil {
		t.Fatalf("blocks for page: %v", err)
	}
	if len(onPage) != 4 {
		t.Errorf("page 2 has %d blocks, want 4", len(onPage))
	}
}

func TestDeletingADocumentRemovesItsBlocksAndFigures(t *testing.T) {
	// ON DELETE CASCADE, on both tables. Without it they accumulate rows pointing at
	// documents that no longer exist, and the FK clause is easy to copy without the
	// cascade with nothing else noticing.
	s, database := newServiceWithDB(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "07")

	blocks := []doc.Block{{
		Page: 62, RegionX0: 43, Index: 0, Kind: doc.BlockParagraph,
		Text: "Garantie.", Lang: "de", X0: 43, X1: 305, Y0: 100, Y1: 116,
		Lines: 1, Chars: 9,
	}}
	fig := figure(t, 62, 0, 12, 9)
	if err := s.SaveConversion(ctx, docID, blocks, []doc.Figure{fig}, blobs,
		registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}
	if got, err := s.Blocks(ctx, docID); err != nil || len(got) != 1 {
		t.Fatalf("blocks before delete = %+v, %v; want 1 row", got, err)
	}
	if got, err := s.Figures(ctx, docID); err != nil || len(got) != 1 {
		t.Fatalf("figures before delete = %+v, %v; want 1 row", got, err)
	}

	if err := gen.New(database.Write()).DeleteDocument(ctx, docID); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, err := s.GetDocument(ctx, docID); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("document survived deletion: %v", err)
	}

	if got, err := s.Blocks(ctx, docID); err != nil || len(got) != 0 {
		t.Errorf("%d blocks outlived their document: %+v (%v)", len(got), got, err)
	}
	if got, err := s.Figures(ctx, docID); err != nil || len(got) != 0 {
		t.Errorf("%d figures outlived their document: %+v (%v)", len(got), got, err)
	}

	// The BYTES are deliberately still there. A blob outlives the rows pointing at
	// it and is collected by counting references, because two documents can
	// legitimately render the same picture -- the same diagram in five languages'
	// sections is one set of bytes.
	if !blobs.Exists(fig.Digest) {
		t.Error("deleting a document deleted a figure's bytes from the content-addressed store")
	}
}

func TestAConversionThatFailsToSaveLeavesTheStateAlone(t *testing.T) {
	// The state moves in the same transaction as the content it rests on, so a save
	// that fails part-way through leaves a document that still says what it said.
	// Setting the state on its own handle -- or before the rows -- would leave this
	// document claiming to be readable with no blocks behind it, which is exactly
	// what a reader sees as an empty manual.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "b7")

	// Page 0 violates doc_blocks' CHECK (page >= 1), so this fails inside the
	// transaction and after the point a wrongly-ordered state write would already
	// have committed.
	blocks := []doc.Block{
		{Page: 12, RegionX0: 0, Index: 0, Kind: doc.BlockParagraph, Text: "gut", Lang: "de"},
		{Page: 0, RegionX0: 0, Index: 0, Kind: doc.BlockParagraph, Text: "impossible", Lang: "de"},
	}
	if err := s.SaveConversion(ctx, docID, blocks, nil, blobs, registry.StateReady); err == nil {
		t.Fatal("a block on page 0 was accepted")
	}

	document, err := s.GetDocument(ctx, docID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if document.State == registry.StateReady {
		t.Errorf("the document says %q after a failed save; the state must not outlive "+
			"the content it claims", document.State)
	}

	got, err := s.Blocks(ctx, docID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a rolled-back conversion left %d blocks behind", len(got))
	}
}

func TestOneLanguagesFiguresAreItsOwnPlusTheNeutralOnes(t *testing.T) {
	// The read side of the rule conversion.md settles: a picture belonging to no
	// language belongs to every language. A household reading two languages stores
	// the union of both, so selecting a language's pictures by page would hand a
	// German reader everything out of the Ukrainian column of every page they share.
	s := newService(t)
	blobs := newBlobStore(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "c3")

	// One page, two columns, a picture in each and one spanning both.
	if err := s.SaveProbe(ctx, docID, resultWith(
		doc.Region{Page: 1, X0: 40, X1: 440, Lang: "de", Source: doc.SourceRepertoire, Chars: 900, Runs: 30},
		doc.Region{Page: 1, X0: 460, X1: 860, Lang: "uk", Source: doc.SourceRepertoire, Chars: 900, Runs: 30},
	), registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	german := figure(t, 1, 0, 12, 9)
	german.Rect = doc.CellRect{X0: 60, Y0: 100, X1: 400, Y1: 300}
	ukrainian := figure(t, 1, 1, 13, 9)
	ukrainian.Rect = doc.CellRect{X0: 480, Y0: 100, X1: 840, Y1: 300}
	neutral := figure(t, 1, 2, 14, 9)
	neutral.Rect = doc.CellRect{X0: 40, Y0: 400, X1: 860, Y1: 600}

	if err := s.SaveConversion(ctx, docID, nil,
		[]doc.Figure{german, ukrainian, neutral}, blobs, registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}

	for _, tc := range []struct {
		lang string
		want []string
	}{
		{"de", []string{german.Digest, neutral.Digest}},
		{"uk", []string{ukrainian.Digest, neutral.Digest}},
		// The unnamed question: only the picture belonging to no column. Asked by no
		// other language's call, which is how it stays reachable.
		{"", []string{neutral.Digest}},
		// A language this document does not print still gets the neutral picture.
		// Nothing here filters by scope, because the conversion already did.
		{"fr", []string{neutral.Digest}},
	} {
		got, err := s.FiguresByLang(ctx, docID, tc.lang)
		if err != nil {
			t.Fatalf("figures for %q: %v", tc.lang, err)
		}
		digests := make([]string, 0, len(got))
		for i := range got {
			digests = append(digests, got[i].SHA256)
		}
		if len(digests) != len(tc.want) {
			t.Errorf("%q got %d figures, want %d: %v", tc.lang, len(digests), len(tc.want), digests)
			continue
		}
		for i := range tc.want {
			if digests[i] != tc.want[i] {
				t.Errorf("%q figure %d is %s, want %s", tc.lang, i, digests[i], tc.want[i])
			}
		}
	}
}
