package registry

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/store"
)

// figureMediaType is what a rendered figure is. doc.renderFigure calls pdftoppm
// with -png and verifies the signature and IHDR before returning, so this is
// checked rather than assumed.
const figureMediaType = "image/png"

// SaveConversion records everything a conversion produced, in one transaction.
//
// All of it or none of it, for [Service.SaveProbe]'s reason one stage further on:
// a document whose row says 'ready' but whose blocks are missing looks converted
// and reads as an empty manual. The state moves in the same transaction as the
// content that justifies it.
//
// The write is idempotent, because a worker can die after doing the work and have
// the job run again. Blocks and figures are keyed naturally and upserted, and both
// are replaced wholesale first -- see [saveBlocks] for why the delete is required
// rather than tidy.
//
// # Why the blob store is a parameter
//
// A figure's bytes are content like any other, so they go to the content-addressed
// store on disk and the row holds the digest, exactly as an uploaded document
// does. [Service] does not hold a store because nothing else in the registry needs
// one; internal/ingest, which is the caller, already has it.
//
// # Why the bytes are written before the transaction opens
//
// A store.Put is a filesystem write and cannot be rolled back with the rows. Doing
// it first means a rolled-back conversion can leave PNG bytes on disk with no row
// pointing at them, and that is the harmless direction: the store is content
// addressed, so the retry writes the same digest and reuses them rather than
// duplicating. The other order -- rows first, bytes after -- would leave a row
// pointing at a picture that does not exist, which is a broken reader.
//
// It also has to be this way round. blobs(sha256) is a foreign key and SQLite
// checks it immediately, so the blobs row must exist inside the transaction before
// the doc_figures row that references it.
func (s *Service) SaveConversion(
	ctx context.Context,
	documentID string,
	blocks []doc.Block,
	figures []doc.Figure,
	blobs *store.Store,
	state string,
) error {
	if documentID == "" {
		return fmt.Errorf("%w: a conversion needs a document", ErrInvalid)
	}
	if len(figures) > 0 && blobs == nil {
		return fmt.Errorf("%w: %d figures to store and no blob store to put them in",
			ErrInvalid, len(figures))
	}

	// Outside the transaction, for the reason above. The refs come back in a
	// parallel slice rather than being written into the caller's figures: filling
	// in a field of someone else's struct as a side effect of saving it is the kind
	// of thing a caller discovers by having it happen.
	refs, err := putFigures(ctx, blobs, figures)
	if err != nil {
		return err
	}

	now := db.Millis(s.now())
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		if err := saveBlocks(ctx, q, documentID, blocks, now); err != nil {
			return err
		}
		if err := saveFigures(ctx, q, documentID, figures, refs, now); err != nil {
			return err
		}

		// Last, so that a document only claims to be converted once its content is
		// in the same transaction as the claim.
		if err := q.SetDocumentState(ctx, gen.SetDocumentStateParams{
			State:     state,
			LastError: "",
			UpdatedAt: now,
			ID:        documentID,
		}); err != nil {
			return fmt.Errorf("set state %s: %w", state, err)
		}
		return nil
	})
}

// putFigures writes each figure's PNG to the blob store and returns the refs in
// the same order, checking as it goes that the digest internal/doc computed is the
// name the store gave the bytes.
//
// The digest is verified rather than trusted, and it is a string comparison
// against an expensive mistake: doc.Figure.Digest is the SHA-256 of exactly what
// pdftoppm wrote, and if it ever disagreed with the store's own then the row would
// describe one picture and point at another. store.Put returns the digest it
// actually used, so the check is free.
//
// A figure with no bytes is rejected rather than stored with an empty digest. That
// is what a caller gets from doc.FindFigures, which is pure geometry and renders
// nothing, where it meant doc.PageFigures -- an easy substitution to make and an
// impossible one to notice afterwards, because the row would be complete in every
// respect except the picture.
func putFigures(ctx context.Context, blobs *store.Store, figures []doc.Figure) ([]store.Ref, error) {
	refs := make([]store.Ref, len(figures))
	for i := range figures {
		fig := &figures[i]
		if len(fig.PNG) == 0 {
			return nil, fmt.Errorf("%w: the figure at index %d on page %d has no bytes; it was "+
				"found but never rendered", ErrInvalid, fig.Index, fig.Page)
		}
		ref, err := blobs.Put(ctx, bytes.NewReader(fig.PNG))
		if err != nil {
			return nil, fmt.Errorf("registry: store figure %d on page %d: %w",
				fig.Index, fig.Page, err)
		}
		if fig.Digest != "" && ref.SHA256 != fig.Digest {
			return nil, fmt.Errorf("registry: figure %d on page %d digests as %s but was "+
				"recorded as %s", fig.Index, fig.Page, ref.SHA256, fig.Digest)
		}
		refs[i] = ref
	}
	return refs, nil
}

// saveBlocks replaces a document's blocks inside SaveConversion's transaction.
//
// REPLACE, NOT MERGE, and the delete is load-bearing: a re-conversion can produce
// FEWER blocks than the one before it. Indices run consecutively from 0 within a
// region, so a region that converted to 12 blocks and now converts to 9 leaves
// rows at idx 9, 10 and 11 which a reader renders as three paragraphs of the
// previous run's text, in order, indistinguishable from content. Every threshold
// in internal/doc's block builder is one measurement away from moving and most of
// them merge. See the foot of 00005_doc_blocks.sql.
//
// Unlike saveRegions there is no "the tool was missing, leave what is there"
// case. A probe can run on a host without pdftohtml and legitimately have no
// opinion about regions; a conversion that could not read the document fails and
// never reaches here, because there is no partial conversion worth storing.
func saveBlocks(ctx context.Context, q *gen.Queries, documentID string, blocks []doc.Block, now int64) error {
	if err := q.DeleteDocBlocks(ctx, documentID); err != nil {
		return fmt.Errorf("clear blocks: %w", err)
	}
	for i := range blocks {
		b := &blocks[i]
		if err := q.UpsertDocBlock(ctx, gen.UpsertDocBlockParams{
			DocumentID: documentID,
			Page:       int64(b.Page),
			// The same rounding doc_regions.x0 got, from the same function, so a
			// block joins to the region it was read from rather than landing one
			// rounding away from the only row it can join to.
			RegionX0:  roundCoord(b.RegionX0),
			Idx:       int64(b.Index),
			Kind:      string(b.Kind),
			Level:     int64(b.Level),
			Text:      b.Text,
			Lang:      b.Lang,
			X0:        b.X0,
			X1:        b.X1,
			Y0:        b.Y0,
			Y1:        b.Y1,
			Lines:     int64(b.Lines),
			Chars:     int64(b.Chars),
			Note:      b.Note,
			CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("save block %d of the region at x %.0f on page %d: %w",
				b.Index, b.RegionX0, b.Page, err)
		}
	}
	return nil
}

// saveFigures replaces a document's figures inside SaveConversion's transaction,
// recording each one's bytes as a blob first.
//
// The blobs row is written here rather than through [Service.RecordBlob], and that
// is not a style choice. RecordBlob uses s.db.Write(), whose pool is capped at one
// connection, and this transaction is holding it -- so calling RecordBlob from
// inside the transaction waits for a connection that only the transaction can
// release. Measured: with a 3 s context it returns "context deadline exceeded"
// after 3.00 s, and a background job passing a context with no deadline would hang
// for good. The statement is the same one; only the handle differs.
//
// Deleting a document's figure ROWS never deletes the blobs they point at. Two
// documents can legitimately render the same picture -- the same diagram in five
// languages' sections is one set of bytes -- so a blob is collected by counting
// references, which is what documents.blob_sha256 already does.
func saveFigures(
	ctx context.Context,
	q *gen.Queries,
	documentID string,
	figures []doc.Figure,
	refs []store.Ref,
	now int64,
) error {
	if err := q.DeleteDocFigures(ctx, documentID); err != nil {
		return fmt.Errorf("clear figures: %w", err)
	}
	for i := range figures {
		f := &figures[i]
		if err := q.UpsertBlob(ctx, gen.UpsertBlobParams{
			Sha256:    refs[i].SHA256,
			SizeBytes: refs[i].Size,
			MediaType: figureMediaType,
			CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("record figure %d on page %d as a blob: %w", f.Index, f.Page, err)
		}
		if err := q.UpsertDocFigure(ctx, gen.UpsertDocFigureParams{
			DocumentID:   documentID,
			Page:         int64(f.Page),
			Idx:          int64(f.Index),
			X0:           f.Rect.X0,
			Y0:           f.Rect.Y0,
			X1:           f.Rect.X1,
			Y1:           f.Rect.Y1,
			Ink:          int64(f.Ink),
			TextFraction: f.TextFraction,
			Dpi:          int64(f.DPI),
			PixelWidth:   int64(f.PixelWidth),
			PixelHeight:  int64(f.PixelHeight),
			BlobSha256:   refs[i].SHA256,
			CreatedAt:    now,
		}); err != nil {
			return fmt.Errorf("save figure %d on page %d: %w", f.Index, f.Page, err)
		}
	}
	return nil
}

// Block is one stored piece of readable content.
//
// RegionX0 is an integer here because that is what is stored and what doc_regions
// stores, so it is the value a caller joins on. The block's own box is float64,
// unrounded, because nothing keys on it: a caller drawing the block on a
// pdftoppm -r 108 render wants what internal/doc measured.
type Block struct {
	Page     int    `json:"page"`
	RegionX0 int    `json:"regionX0"`
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	// Level is the heading level, 1 for the most prominent, and 0 for anything
	// that is not a heading.
	Level int    `json:"level,omitempty"`
	Text  string `json:"text"`
	// Lang is the region's language, empty where none was established, and Name is
	// that for a person to read: the UI shows "Ukrainian", not "uk".
	Lang string `json:"lang,omitempty"`
	Name string `json:"name,omitempty"`

	X0 float64 `json:"x0"`
	X1 float64 `json:"x1"`
	Y0 float64 `json:"y0"`
	Y1 float64 `json:"y1"`

	Lines int    `json:"lines"`
	Chars int    `json:"chars"`
	Note  string `json:"note,omitempty"`
}

// Blocks returns a document's readable content in reading order: down the pages,
// then left to right across each, then in order within a region.
//
// Empty is not the same claim as absent, for [Service.Regions]' reason: a document
// that has not been converted has no blocks, and that is not the claim that it has
// no content. [Document.State] is what distinguishes them.
func (s *Service) Blocks(ctx context.Context, documentID string) ([]Block, error) {
	rows, err := gen.New(s.db.Read()).ListDocBlocks(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("registry: list blocks: %w", err)
	}
	return blocksFrom(rows), nil
}

// BlocksByLang returns one language's content, which is the funnel's own query: a
// household that reads German gets the German column of each page rather than the
// page, measured in conversion.md as a fifth of the work.
//
// Passing "" asks for the blocks whose language was never established. Those are
// returned by no other language's call, so this is how they stay reachable rather
// than becoming invisible.
func (s *Service) BlocksByLang(ctx context.Context, documentID, lang string) ([]Block, error) {
	rows, err := gen.New(s.db.Read()).ListDocBlocksByLang(ctx, gen.ListDocBlocksByLangParams{
		DocumentID: documentID,
		Lang:       lang,
	})
	if err != nil {
		return nil, fmt.Errorf("registry: list blocks for %q: %w", lang, err)
	}
	return blocksFrom(rows), nil
}

// BlocksForPage returns one page's blocks, in reading order across its regions.
func (s *Service) BlocksForPage(ctx context.Context, documentID string, page int) ([]Block, error) {
	rows, err := gen.New(s.db.Read()).ListDocBlocksForPage(ctx, gen.ListDocBlocksForPageParams{
		DocumentID: documentID,
		Page:       int64(page),
	})
	if err != nil {
		return nil, fmt.Errorf("registry: list blocks on page %d: %w", page, err)
	}
	return blocksFrom(rows), nil
}

func blocksFrom(rows []gen.DocBlock) []Block {
	out := make([]Block, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, Block{
			Page:     int(r.Page),
			RegionX0: int(r.RegionX0),
			Index:    int(r.Idx),
			Kind:     r.Kind,
			Level:    int(r.Level),
			Text:     r.Text,
			Lang:     r.Lang,
			Name:     doc.DisplayName(r.Lang),
			X0:       r.X0, X1: r.X1, Y0: r.Y0, Y1: r.Y1,
			Lines: int(r.Lines),
			Chars: int(r.Chars),
			Note:  r.Note,
		})
	}
	return out
}

// Figure is one stored illustration.
//
// There is no language field, and that is the contract rather than an omission: a
// picture that belongs to no language belongs to every language, so a reader
// scoped to German selects the figures of the pages German occupies instead of
// selecting figures by language.
type Figure struct {
	Page  int `json:"page"`
	Index int `json:"index"`

	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`

	// Ink is how many drawn shapes the figure holds and TextFraction how much of
	// its area is covered by text: the shape guard's evidence and the text guard's,
	// kept rather than reduced to the verdict.
	Ink          int     `json:"ink"`
	TextFraction float64 `json:"textFraction"`

	DPI         int `json:"dpi"`
	PixelWidth  int `json:"pixelWidth"`
	PixelHeight int `json:"pixelHeight"`

	// SHA256 is the blob store's name for the PNG, which is also the PNG's digest.
	SHA256 string `json:"sha256"`
}

// Figures returns a document's illustrations in page order.
func (s *Service) Figures(ctx context.Context, documentID string) ([]Figure, error) {
	rows, err := gen.New(s.db.Read()).ListDocFigures(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("registry: list figures: %w", err)
	}
	return figuresFrom(rows), nil
}

// FiguresForPage returns one page's illustrations in reading order.
func (s *Service) FiguresForPage(ctx context.Context, documentID string, page int) ([]Figure, error) {
	rows, err := gen.New(s.db.Read()).ListDocFiguresForPage(ctx, gen.ListDocFiguresForPageParams{
		DocumentID: documentID,
		Page:       int64(page),
	})
	if err != nil {
		return nil, fmt.Errorf("registry: list figures on page %d: %w", page, err)
	}
	return figuresFrom(rows), nil
}

func figuresFrom(rows []gen.DocFigure) []Figure {
	out := make([]Figure, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, Figure{
			Page:  int(r.Page),
			Index: int(r.Idx),
			X0:    r.X0, Y0: r.Y0, X1: r.X1, Y1: r.Y1,
			Ink:          int(r.Ink),
			TextFraction: r.TextFraction,
			DPI:          int(r.Dpi),
			PixelWidth:   int(r.PixelWidth),
			PixelHeight:  int(r.PixelHeight),
			SHA256:       r.BlobSha256,
		})
	}
	return out
}
