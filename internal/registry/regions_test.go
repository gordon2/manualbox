package registry_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// newServiceWithDB is newService plus the handle, for the two assertions that must
// be made against the database rather than through the service: the region summary,
// whose arithmetic is in SQL with nothing in Go protecting it, and the cascade,
// which needs to delete a document row and the service has no method for that.
func newServiceWithDB(t *testing.T) (*registry.Service, *db.DB) {
	t.Helper()
	database, err := db.Open(context.Background(), db.Options{
		Path: filepath.Join(t.TempDir(), "registry.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return registry.New(database, registry.Options{}), database
}

// newProbedDocument makes a device, a blob and a document to hang regions off,
// because every test here needs one and the ceremony is not what any of them is
// about. The digest is a parameter so two documents in one test cannot collide on
// the unique (device_id, blob_sha256) index.
func newProbedDocument(t *testing.T, s *registry.Service, digest string) string {
	t.Helper()
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Dry box"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat(digest, 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, "application/pdf"); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256, Filename: "manual.pdf",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	return document.ID
}

// resultWith wraps regions in the minimum viable probe result. RegionNote is empty,
// which is what says "the probe did read the positioned text".
func resultWith(regions ...doc.Region) *doc.Result {
	return &doc.Result{
		Info:         doc.Info{Pages: 4},
		Pages:        []doc.Page{{No: 1, Chars: 100}, {No: 2, Chars: 100}, {No: 3, Chars: 100}, {No: 4, Chars: 100}},
		Runs:         []doc.Run{},
		HasTextLayer: true, ContentStart: 1, ContentEnd: 4,
		Regions: regions,
	}
}

func TestRegionsRoundTrip(t *testing.T) {
	// Every field, because a region that survives with the wrong character count or
	// the wrong box is worse than one that fails to save: it reads as authoritative.
	// The coordinates are the column manual's real page 2 edges from
	// testdata/fixtures/thomas-drybox-amfibia.json, with fractions added to check
	// that they are rounded rather than truncated on the way in.
	s := newService(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "a1")

	res := resultWith(
		// A whole page: x0 = 0, x1 = the page width. No box means the whole page.
		doc.Region{
			Page: 1, X0: 0, X1: 892.0,
			Code: "UA", Lang: "uk", Source: doc.SourcePageTag,
			Chars: 1700, Runs: 96,
			Note: "the whole page is Ukrainian",
		},
		// Three boxed columns on one page, .5 and .49 chosen so rounding and
		// truncation give different answers.
		doc.Region{
			Page: 2, X0: 42.5, X1: 305.4,
			Code: "D", Lang: "de", Source: doc.SourceRepertoire,
			Chars: 900, Runs: 50,
			Note: "read from the characters the column uses",
		},
		doc.Region{
			Page: 2, X0: 322.6, X1: 585.49,
			Code: "PL", Lang: "pl", Source: doc.SourceRepertoire,
			Chars: 880, Runs: 47,
		},
		doc.Region{
			Page: 2, X0: 603.5, X1: 866.5,
			Code: "RUS", Lang: "ru", Source: doc.SourceRepertoire,
			Chars: 870, Runs: 46,
			Conflict: true,
			Note:     "the printed tag and the alphabet disagreed",
		},
	)
	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	got, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions: %v", err)
	}

	want := []registry.Region{
		{Page: 1, X0: 0, X1: 892, Source: "page-tag", Code: "UA", Lang: "uk", Name: "Ukrainian",
			Chars: 1700, Runs: 96, Note: "the whole page is Ukrainian"},
		{Page: 2, X0: 43, X1: 305, Source: "repertoire", Code: "D", Lang: "de", Name: "German",
			Chars: 900, Runs: 50, Note: "read from the characters the column uses"},
		{Page: 2, X0: 323, X1: 585, Source: "repertoire", Code: "PL", Lang: "pl", Name: "Polish",
			Chars: 880, Runs: 47},
		{Page: 2, X0: 604, X1: 867, Source: "repertoire", Code: "RUS", Lang: "ru", Name: "Russian",
			Chars: 870, Runs: 46, Conflict: true, Note: "the printed tag and the alphabet disagreed"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d regions, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("region %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestTwoColumnsOfOneLanguageOnAPageBothSurvive(t *testing.T) {
	// THIS IS THE BLOCKER doc_regions EXISTS TO FIX. Under doc_langs' key
	// (document_id, source, code, pdf_start) two German columns on one page are the
	// same row: same page, same code, same source, nothing to tell them apart, so one
	// silently overwrites the other. Keying on geometry is what separates them, and
	// the column manual really does set two columns of one language -- its manifest
	// calls that out precisely because column count is not language count.
	s := newService(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "b2")

	res := resultWith(
		doc.Region{Page: 6, X0: 43, X1: 438, Code: "D", Lang: "de",
			Source: doc.SourceRepertoire, Chars: 1200, Runs: 60, Note: "left column"},
		doc.Region{Page: 6, X0: 469, X1: 857, Code: "D", Lang: "de",
			Source: doc.SourceRepertoire, Chars: 1150, Runs: 58, Note: "right column"},
	)
	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	got, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d regions on page 6, want 2 -- a same-language column was lost: %+v", len(got), got)
	}
	if got[0].X0 != 43 || got[1].X0 != 469 {
		t.Errorf("columns are at x0 %d and %d, want 43 and 469", got[0].X0, got[1].X0)
	}
	if got[0].Note != "left column" || got[1].Note != "right column" {
		t.Errorf("the two columns are not distinct: %q and %q", got[0].Note, got[1].Note)
	}
	// The characters must be summed across both, not taken from whichever was
	// written last, or the page's size is half what it is.
	if total := got[0].Chars + got[1].Chars; total != 2350 {
		t.Errorf("page 6 holds %d characters, want 2350", total)
	}
}

func TestSavingTheSameProbeTwiceLeavesTheSameRegions(t *testing.T) {
	// A probe job can run twice: a worker may die after doing the work but before
	// recording success, and the reclaimed job runs again. The second run must
	// converge on the same rows rather than duplicating them. Values, not just
	// counts -- a converging count with a corrupted row would pass a count check.
	s := newService(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "c3")

	res := resultWith(
		doc.Region{Page: 2, X0: 43, X1: 305, Code: "D", Lang: "de",
			Source: doc.SourceRepertoire, Chars: 900, Runs: 50},
		doc.Region{Page: 2, X0: 323, X1: 585, Code: "PL", Lang: "pl",
			Source: doc.SourceRepertoire, Chars: 880, Runs: 47},
		doc.Region{Page: 3, X0: 0, X1: 892, Code: "", Lang: "", Source: "",
			Chars: 120, Runs: 12, Note: "no language established for this page"},
	)

	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save first probe: %v", err)
	}
	first, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions after first probe: %v", err)
	}

	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save second probe: %v", err)
	}
	second, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions after second probe: %v", err)
	}

	if len(first) != 3 {
		t.Fatalf("first probe stored %d regions, want 3", len(first))
	}
	if len(second) != len(first) {
		t.Fatalf("re-probing changed the row count from %d to %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("region %d changed on re-probe:\nfirst  %+v\nsecond %+v", i, first[i], second[i])
		}
	}
}

func TestAChangedRegionAttributionLeavesNoStaleRow(t *testing.T) {
	// THIS IS THE CASE source-IN-THE-KEY WOULD OTHERWISE BREAK, and the reason
	// SaveProbe deletes before inserting.
	//
	// internal/doc produces ONE resolved set of regions in which source merely
	// records which signal named each one, and that attribution changes between
	// probes: a column named by its alphabet on one run can be named by its printed
	// tag on the next, because the tag reader's vocabulary comes from the document's
	// own contents table and that parse can improve. Same document, same page, same
	// x0, same column -- but a different primary key, so an upsert alone leaves the
	// superseded row behind and the page reports itself twice.
	//
	// If this test still passes with the delete removed from SaveProbe, it is not
	// testing what it claims.
	s := newService(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "d4")

	named := doc.Region{
		Page: 7, X0: 43, X1: 305, Code: "D", Lang: "de",
		Source: doc.SourceRepertoire, Chars: 900, Runs: 50,
		Note: "read from the characters the column uses",
	}
	if err := s.SaveProbe(ctx, docID, resultWith(named), registry.StateAwaitingScope); err != nil {
		t.Fatalf("save first probe: %v", err)
	}

	// The same column, at the same x0 on the same page, now named by its printed tag.
	reattributed := named
	reattributed.Source = doc.SourcePageTag
	reattributed.Note = "read from the tag printed on the column"
	if err := s.SaveProbe(ctx, docID, resultWith(reattributed), registry.StateAwaitingScope); err != nil {
		t.Fatalf("save re-probe: %v", err)
	}

	got, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d regions after re-attribution, want 1 -- the superseded row lingered "+
			"and page 7 now reports itself twice: %+v", len(got), got)
	}
	if got[0].Source != "page-tag" {
		t.Errorf("source = %q, want page-tag: the newer attribution did not win", got[0].Source)
	}
	if got[0].Note != "read from the tag printed on the column" {
		t.Errorf("note = %q, want the re-probe's", got[0].Note)
	}
}

func TestAProbeThatCouldNotReadRegionsLeavesThemIntact(t *testing.T) {
	// RegionNote is set when positioned text could not be read at all: pdftohtml is
	// absent or failed. poppler is optional at runtime here, so the same document can
	// be probed with regions available and then without -- and deleting a good region
	// map because a tool went missing from the host would be destructive.
	//
	// The distinction is RegionNote, not len(Regions): an empty note with no regions
	// means the probe read the document and found none, which must replace.
	s := newService(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "e5")

	stored := resultWith(
		doc.Region{Page: 2, X0: 43, X1: 305, Code: "D", Lang: "de",
			Source: doc.SourceRepertoire, Chars: 900, Runs: 50},
		doc.Region{Page: 2, X0: 323, X1: 585, Code: "PL", Lang: "pl",
			Source: doc.SourceRepertoire, Chars: 880, Runs: 47},
	)
	if err := s.SaveProbe(ctx, docID, stored, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe with regions: %v", err)
	}

	// A re-probe on a host without pdftohtml. The per-page language map is still
	// complete; only the column resolution is missing.
	blind := resultWith()
	blind.RegionNote = "per-column languages are unavailable: pdftohtml not found"
	if err := s.SaveProbe(ctx, docID, blind, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe without regions: %v", err)
	}

	got, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d regions, want 2 -- a missing optional tool deleted good rows: %+v", len(got), got)
	}

	// And the other half of the distinction: a probe that DID read the document and
	// found nothing replaces, even though it also carries zero regions.
	silent := resultWith()
	if err := s.SaveProbe(ctx, docID, silent, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe that read nothing: %v", err)
	}
	got, err = s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d regions, want 0 -- a probe that read the document and found none "+
			"must replace a stale map, not hide behind it: %+v", len(got), got)
	}
}

func TestDeletingADocumentRemovesItsRegions(t *testing.T) {
	// ON DELETE CASCADE. Without it doc_regions accumulates rows pointing at
	// documents that no longer exist, and the FK clause is easy to copy without the
	// cascade with nothing else noticing.
	s, database := newServiceWithDB(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "f6")

	res := resultWith(doc.Region{
		Page: 2, X0: 43, X1: 305, Code: "D", Lang: "de",
		Source: doc.SourceRepertoire, Chars: 900, Runs: 50,
	})
	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}
	if got, err := s.Regions(ctx, docID); err != nil || len(got) != 1 {
		t.Fatalf("regions before delete = %+v, %v; want 1 row", got, err)
	}

	if err := gen.New(database.Write()).DeleteDocument(ctx, docID); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, err := s.GetDocument(ctx, docID); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("document survived deletion: %v", err)
	}

	got, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("regions after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("%d regions outlived their document: %+v", len(got), got)
	}
}

func TestRegionSummaryCountsCharactersNotPages(t *testing.T) {
	// The summary is asserted against the database rather than through the service,
	// because the arithmetic is in SQL and nothing in Go protects it. Two German
	// columns on one page must sum to one language on one page, not two pages.
	s, database := newServiceWithDB(t)
	ctx := context.Background()
	docID := newProbedDocument(t, s, "07")

	res := resultWith(
		doc.Region{Page: 6, X0: 43, X1: 438, Code: "D", Lang: "de",
			Source: doc.SourceRepertoire, Chars: 1200, Runs: 60},
		doc.Region{Page: 6, X0: 469, X1: 857, Code: "D", Lang: "de",
			Source: doc.SourceRepertoire, Chars: 1150, Runs: 58},
		doc.Region{Page: 7, X0: 0, X1: 892, Code: "PL", Lang: "pl",
			Source: doc.SourcePageTag, Chars: 2000, Runs: 100, Conflict: true},
	)
	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	rows, err := gen.New(database.Read()).SummarizeDocRegions(ctx, docID)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d summary rows, want 2 (de and pl): %+v", len(rows), rows)
	}
	for i := range rows {
		r := &rows[i]
		switch r.Lang {
		case "de":
			if r.Chars != 2350 || r.Pages != 1 || r.Runs != 118 || r.Disputed != 0 {
				t.Errorf("de summary = %+v; want chars 2350, pages 1, runs 118, disputed 0", r)
			}
		case "pl":
			if r.Chars != 2000 || r.Pages != 1 || r.Disputed != 1 {
				t.Errorf("pl summary = %+v; want chars 2000, pages 1, disputed 1", r)
			}
		default:
			t.Errorf("unexpected summary row %+v", r)
		}
	}
}
