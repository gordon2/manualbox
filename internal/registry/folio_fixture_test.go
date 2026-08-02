package registry_test

import (
	"context"
	"os"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
	"github.com/gordon2/manualbox/internal/registry"
)

// TestFolioOffsetOnRealManuals pins the offset both real manuals produce, and the
// margin it wins by.
//
// These four numbers are the ones a change to internal/doc's pageFolio would move,
// which is exactly why they are here rather than only in a design doc. The offset
// itself is the user-visible one -- it is what a contents entry jumps by -- and the
// support is what says the offset was believed for a reason. A change that leaves
// the offset alone but halves its support has broken the folio reader without
// breaking any link yet, and that is worth being told about before it gets worse.
//
// Both documents are asserted in one test because their two answers are the
// contrast the whole feature rests on: the columns manual's real offset is zero, so
// "no mapping" and "offset zero" are two different answers that must not converge.
func TestFolioOffsetOnRealManuals(t *testing.T) {
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixtures and run the real-document tests", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText, extern.PDFToHTML} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}

	tests := []struct {
		name     string
		fixture  string
		digest   string
		shape    string
		offset   int
		pages    int
		folioPgs int
	}{
		{
			// 558 of its 560 pages print a folio. The six that disagree are all
			// misreads of a short line that is not a folio: pages 2-4 are contents
			// pages whose own body numbers were read (194, 403, 533), pages 5-6 are
			// diagram plates where a callout number was, and page 509 reads 2.
			name: "the sequential manual, offset 6", fixture: "dreame-l40-ultra",
			digest: "a1", shape: "560 pages of sections one after another",
			offset: 6, pages: 552, folioPgs: 558,
		},
		{
			// 67 of its 68 pages print a folio, and the offset really is zero: this
			// manual's page 1 is its cover. Page 12 reads 10 and the back cover reads
			// 2735.
			name: "the columns manual, offset 0", fixture: "thomas-drybox-amfibia",
			digest: "b2", shape: "68 pages of five languages in parallel columns",
			offset: 0, pages: 65, folioPgs: 67,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, err := fixture.Load(fixturesDir, tt.fixture)
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			path, err := manifest.Fetch(ctx)
			if err != nil {
				t.Fatalf("fetch fixture: %v", err)
			}
			res, err := doc.Analyze(ctx, path)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}

			s := newService(t)
			docID := newProbedDocument(t, s, tt.digest)
			if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
				t.Fatalf("save probe: %v", err)
			}

			got, err := s.FolioOffset(ctx, docID)
			if err != nil {
				t.Fatalf("folio offset: %v", err)
			}
			if got == nil {
				t.Fatalf("no offset was offered for %s.\n"+
					"This document's folios used to agree on %d for %d of the %d pages that "+
					"print one. Losing the answer entirely means pageFolio now reads folios "+
					"that disagree, and every contents entry in this manual has stopped being "+
					"a link.", tt.shape, tt.offset, tt.pages, tt.folioPgs)
			}
			if got.Offset != tt.offset {
				t.Errorf("offset %d, want %d.\n"+
					"This is what a contents entry jumps by, so every link in %s now lands "+
					"%d pages from where it should. Either pageFolio reads a different line "+
					"than it did, or this manual's front matter changed.",
					got.Offset, tt.offset, tt.shape, got.Offset-tt.offset)
			}
			if got.FolioPages != tt.folioPgs {
				t.Errorf("%d pages print a folio, want %d.\n"+
					"pageFolio is finding folios on a different set of pages than it did; "+
					"the offset above may still be right by luck.",
					got.FolioPages, tt.folioPgs)
			}
			if got.Pages != tt.pages {
				t.Errorf("%d of %d pages agree on offset %d, want %d.\n"+
					"The answer has not changed but the evidence for it has. Fewer agreeing "+
					"pages means pageFolio is misreading more lines as folios; more means it "+
					"has started reading folios it used to miss. Both are real changes and "+
					"neither is visible in the offset alone.",
					got.Pages, got.FolioPages, got.Offset, tt.pages)
			}
		})
	}
}
