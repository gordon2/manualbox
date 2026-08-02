package registry

import "testing"

// The rule, exercised on the histograms both real manuals actually produce plus the
// cases the floor exists to refuse. Hermetic: modalFolioOffset takes the histogram,
// so none of this needs a database or a PDF.
func TestModalFolioOffset(t *testing.T) {
	t.Parallel()

	// The sequential manual's real histogram, read from the stored doc_pages of both
	// converted manuals. 558 pages print a folio; six of them are misreads of a
	// short line that is not a folio -- two contents pages, two diagram plates whose
	// callout number was read, and page 509 reading 2.
	sequential := []FolioOffsetCount{
		{Offset: 6, Pages: 552},
		{Offset: 507, Pages: 1},
		{Offset: 3, Pages: 1},
		{Offset: 2, Pages: 1},
		{Offset: -192, Pages: 1},
		{Offset: -400, Pages: 1},
		{Offset: -529, Pages: 1},
	}
	// The columns manual's real histogram. Its true offset is zero, which is exactly
	// the value that must not be confusable with "no answer". Page 12 reads 10 and
	// the back cover reads 2735.
	columns := []FolioOffsetCount{
		{Offset: 0, Pages: 65},
		{Offset: 2, Pages: 1},
		{Offset: -2667, Pages: 1},
	}

	tests := []struct {
		name   string
		counts []FolioOffsetCount
		// want is nil where the document must get no answer.
		want *FolioOffset
	}{
		{
			name:   "the sequential manual, six misreads and all",
			counts: sequential,
			want:   &FolioOffset{Offset: 6, Pages: 552, FolioPages: 558},
		},
		{
			name:   "the columns manual, whose real offset is zero",
			counts: columns,
			want:   &FolioOffset{Offset: 0, Pages: 65, FolioPages: 67},
		},
		{
			name: "one misread does not move the mode",
			// The mean of these is 336, which is not a page of anything.
			counts: []FolioOffsetCount{{Offset: 4, Pages: 20}, {Offset: 2735, Pages: 1}},
			want:   &FolioOffset{Offset: 4, Pages: 20, FolioPages: 21},
		},
		{
			name: "folios restarting in each of 34 sections have no majority",
			// What the sequential manual's histogram would be if every section began
			// again at 1: the biggest section holds 22 of 553 pages, so the best
			// offset has 4.0% support and there is no document-wide answer to give.
			counts: restarting(34, 553),
			want:   nil,
		},
		{
			name: "a document bound in two halves is refused, near-majority and all",
			// The case a bare plurality would get wrong: 55% is the largest share a
			// two-part restart can hand its bigger part while still being a document
			// with two offsets rather than one.
			counts: []FolioOffsetCount{{Offset: 0, Pages: 55}, {Offset: 30, Pages: 45}},
			want:   nil,
		},
		{
			name:   "no page prints a folio at all",
			counts: nil,
			want:   nil,
		},
		{
			name: "too few folios for their agreement to mean anything",
			// 2 of 2 is 100% support and no evidence whatever.
			counts: []FolioOffsetCount{{Offset: 6, Pages: 2}},
			want:   nil,
		},
		{
			name:   "four folios, one of them outvoted, is the smallest real reading",
			counts: []FolioOffsetCount{{Offset: 6, Pages: 3}, {Offset: 100, Pages: 1}},
			want:   &FolioOffset{Offset: 6, Pages: 3, FolioPages: 4},
		},
		{
			name: "the mode is read from the counts, not from the order",
			// The histogram arrives sorted by count, so a rule that took the first row
			// would pass every case above. This one is deliberately out of order.
			counts: []FolioOffsetCount{{Offset: 99, Pages: 2}, {Offset: 6, Pages: 20}},
			want:   &FolioOffset{Offset: 6, Pages: 20, FolioPages: 22},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := modalFolioOffset(tt.counts)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("offset %+v was offered; these folios agree on nothing and the "+
						"document must get no mapping rather than a plausible-looking one", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("no offset was offered, want %+v", *tt.want)
			}
			if got.Offset != tt.want.Offset || got.Pages != tt.want.Pages ||
				got.FolioPages != tt.want.FolioPages {
				t.Fatalf("offset %d on %d of %d pages, want %d on %d of %d",
					got.Offset, got.Pages, got.FolioPages,
					tt.want.Offset, tt.want.Pages, tt.want.FolioPages)
			}
			if want := float64(tt.want.Pages) / float64(tt.want.FolioPages); got.Support != want {
				t.Fatalf("support %v, want %v", got.Support, want)
			}
		})
	}
}

// restarting builds the histogram of a document whose folios begin again at 1 in
// each of sections sections, splitting pages between them as evenly as the
// remainder allows.
func restarting(sections, pages int) []FolioOffsetCount {
	out := make([]FolioOffsetCount, 0, sections)
	for i := range sections {
		n := pages / sections
		if i < pages%sections {
			n++
		}
		// Each section starts where the last ended, so each has its own offset.
		out = append(out, FolioOffsetCount{Offset: i * (pages / sections), Pages: n})
	}
	return out
}
