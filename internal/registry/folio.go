package registry

import (
	"context"
	"fmt"

	"github.com/gordon2/manualbox/internal/db/gen"
)

// The rule for turning a document's folio histogram into one offset.
//
// A contents entry names a page printed on the paper, and the reader has to open a
// page of the PDF. The two differ by the front matter the printer bound in front of
// page 1, so `pdf = printed + offset` -- and the whole question is whether one
// offset is true for the whole document.
//
// Measured against both real manuals' stored doc_pages, which is the pipeline's own
// answer rather than a re-reading of the PDFs:
//
//	sequential (560pp): 558 pages print a folio, 552 of them at offset 6
//	columns    ( 68pp):  67 pages print a folio,  65 of them at offset 0
//
// The runner-up covers exactly one page in each, so the margin is 552-to-1 and
// 65-to-1. Every deviation is a misread of a short line that is not a folio: the
// sequential manual's contents pages read their own body numbers (194, 403, 533),
// its diagram plates read a callout number, and the columns manual's back cover
// reads 2735.
const (
	// minFolioSupport is the share of the folio-bearing pages the modal offset must
	// hold before it is offered at all.
	//
	// The mode, not the mean -- for the same reason internal/doc's columnPitch takes
	// the mode of its line gaps rather than their median, recorded there: a handful
	// of readings that are not measurements of the thing at all drag an average off
	// the value every real member sits exactly on. Here it is worse than a drag. One
	// page misread as folio 2735 puts the mean 40 pages out, and 6 of the sequential
	// manual's outliers are negative offsets in the hundreds.
	//
	// 0.6 is chosen from the two measurements above and from what the failure looks
	// like. A document whose folios genuinely restart per section has no majority:
	// if the sequential manual's 34 sections each began again at 1, its biggest
	// section would hold 22 of 553 pages and the best offset would have 4.0%
	// support. The case that could still fool a bare plurality is a document bound
	// in two halves, where the larger half is near 50%. So the floor has to be
	// above a half, and being above a half buys a second property for nothing: at
	// most one offset can hold more than half the pages, so the mode is unique by
	// construction and no tie-break policy is needed.
	//
	// What it costs, said plainly: a document that really does have one offset, but
	// whose folios are read so badly that fewer than three pages in five agree, is
	// refused and its contents entries stay plain text. That is the right side to
	// fail on -- a link to the wrong page is worse than no link -- and it is a long
	// way from anything observed: the two real manuals disagree on 6 of 558 and 2 of
	// 67. Refusing to answer is also the correct outcome for a genuinely restarting
	// document, which is the case this floor exists to catch.
	minFolioSupport = 0.6

	// minFolioPages is how many pages must print a folio before their agreement
	// means anything.
	//
	// Below four, minFolioSupport is satisfied by 1 of 1, 2 of 2 or 2 of 3, none of
	// which is evidence of a constant that holds across a document. 3 of 4 is the
	// smallest reading in which an outlier has actually been outvoted.
	minFolioPages = 4
)

// FolioOffset is how far a document's PDF pages run ahead of its printed folios:
// the PDF page for a printed page number is printed + Offset.
//
// Pages and Support are the evidence, carried so a caller can say why rather than
// only what. Offset is very often 0 -- the columns manual's really is -- so the
// answer is a pointer at every layer above this one, and "no confident answer" must
// never be flattened into "offset zero".
type FolioOffset struct {
	Offset int
	// Pages is how many pages agree on Offset, of the FolioPages that print one.
	Pages      int
	FolioPages int
	// Support is Pages over FolioPages, between 0 and 1.
	Support float64
}

// FolioOffset reports the one offset that maps this document's printed page numbers
// onto its PDF pages, or nil where the stored folios do not agree on one.
//
// Derived from doc_pages on every call rather than stored: see the query's own
// header for why, and note that it is asked once per conversion response, not per
// entry.
func (s *Service) FolioOffset(ctx context.Context, documentID string) (*FolioOffset, error) {
	rows, err := gen.New(s.db.Read()).DocPageFolioOffsets(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("registry: folio offsets: %w", err)
	}
	counts := make([]FolioOffsetCount, 0, len(rows))
	for i := range rows {
		counts = append(counts, FolioOffsetCount{
			Offset: int(rows[i].FolioOffset),
			Pages:  int(rows[i].Pages),
		})
	}
	return modalFolioOffset(counts), nil
}

// FolioOffsetCount is one bar of the histogram: an offset and how many of the
// document's pages read that way.
type FolioOffsetCount struct {
	Offset int
	Pages  int
}

// modalFolioOffset applies the rule above to a histogram.
//
// Separated from the query so the rule can be exercised on the real outliers
// without a database. The input need not be sorted.
func modalFolioOffset(counts []FolioOffsetCount) *FolioOffset {
	total := 0
	best := -1
	for i := range counts {
		total += counts[i].Pages
		if best < 0 || counts[i].Pages > counts[best].Pages {
			best = i
		}
	}
	if best < 0 || total < minFolioPages {
		return nil
	}
	support := float64(counts[best].Pages) / float64(total)
	if support < minFolioSupport {
		return nil
	}
	return &FolioOffset{
		Offset:     counts[best].Offset,
		Pages:      counts[best].Pages,
		FolioPages: total,
		Support:    support,
	}
}
