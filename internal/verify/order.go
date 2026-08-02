package verify

import (
	"fmt"
	"math"
	"sort"

	"github.com/gordon2/manualbox/internal/doc"
)

// orderSlack is how far up the page the next block may sit and still count as
// advancing down it, in the 1.5-scaled space.
//
// Two blocks of one column legitimately share a top to within rounding — a list
// marker folded into its text, a heading and the run beside it — and a strict
// comparison would report those. 2.0 is the baseline tolerance columns.go measures
// at 15% of a median run height, which is about 2.5 units against either manual's
// line pitch, rounded down so that this never accepts a real backwards jump: the
// smallest one on the column manual's page 62, the interleaving case conversion.md
// describes, is 16 units.
const (
	orderSlack = 2.0

	// minOrderGap is how far apart two blocks' x-ranges must be before they are
	// believed to be in different columns, in the same units.
	//
	// Disjointness alone is not enough, and the measurement says why: a folio at
	// x=43-47 and the paragraph above it at x=49-293 are disjoint by two units and
	// are plainly the same column. A real gutter is an order wider — the narrowest
	// on the column manual is 13 units, which columns.go measures on its page 68 —
	// so the guard sits just under that.
	minOrderGap = 12.0

	// minOrderChars is how much text each block must hold before a switch between
	// them is read as interleaving.
	//
	// This is the page-furniture guard, and without it the check reports furniture
	// and little else. The sequential manual prints a two-letter language badge at
	// x=27-41 below the running head on 110 pages, and that badge is a block: it is
	// disjoint from the heading above it and lower down the page, which is the
	// violation's exact shape. The column manual's parts pages do the same with
	// numbered callouts scattered around a diagram.
	//
	// Swept over both manuals, as findings (column / sequential):
	//
	//	              chars>=0    chars>=8   chars>=16   chars>=24
	//	gap>=0        67 / 687    0 / 71      0 / 37      0 / 11
	//	gap>=12       61 / 662    0 / 70      0 / 37      0 / 11
	//	gap>=20       58 / 326    0 / 69      0 / 36      0 / 11
	//
	// A floor of 8 runes already removes every one of the column manual's 67, all of
	// which are a callout number or a folio. 16 is chosen over 8 because the 34 the
	// sequential manual loses between them are the short interval labels of the same
	// grid its 37 remaining findings name, so nothing new is lost, and because a
	// block of interleaved prose is a printed line or more — the page-62 case
	// conversion.md describes runs 40 to 80 runes.
	//
	// What survives at the defaults is one real class, and its concentration is what
	// makes it believable: 37 findings on 26 pages, the routine-maintenance page of
	// one language section after another, where an unruled grid of intervals —
	// invisible to the table detector by conversion.md's own account — is read in
	// columns.
	minOrderChars = 16
)

// orderGuards are the three bounds, taken as a value so a test can sweep them
// over both whole documents. That is how every threshold in this project is set.
type orderGuards struct {
	slack, minGap float64
	minChars      int
}

var defaultOrderGuards = orderGuards{
	slack: orderSlack, minGap: minOrderGap, minChars: minOrderChars,
}

// checkOrder answers "would a person read this in the order it is stored", and it
// is the check that catches the failure conversion.md spends the most words on.
//
// # What a violation is, and why it is not simply "y increases"
//
// A region of several columns is read column by column, so y going backwards is
// CORRECT at every column boundary — from the foot of one column to the head of the
// next. The wrong thing is the opposite: switching column while continuing DOWN the
// page, which is what sorting a whole-page region's runs by y then x produces and
// what conversion.md records on the column manual's page 62 ("rial bitte
// umweltgerecht. sich bei gewerblicher Benutzung…").
//
// So a finding is two consecutive blocks of one page and region whose x-ranges do
// not overlap at all — they are in different columns — where the second does not
// start above the first. Horizontal disjointness rather than a column id because
// [doc.Block] carries no column, only its own box; that is the second thing this
// package wanted from `internal/doc` and worked around.
//
// # Table cells are excluded, and must be
//
// [doc.BlockTable] cells are emitted row-major, deliberately: conversion.md records
// that reading down every question and then down every answer was the limitation
// row-major reading fixed. Row-major is exactly this check's violation shape — cell
// (r,c) to (r,c+1) is disjoint and level — so a table page would report one finding
// per cell. Measured: including table cells takes the column manual from 0 findings
// to 207 and the sequential one from 686 to 3,158, and every added one is correct
// row-major reading.
//
// Excluded from the comparison, though, is not the same as invisible, and reading them
// as invisible is a blind spot this check had. Two prose blocks with a table between
// them are not consecutive in reading order at all, so "the second does not start above
// the first" says nothing about either. The measured case is the column manual's four
// troubleshooting pages: each prints two side-by-side tables whose header rows sit above
// a top border that is not drawn, so those headers are the page's only prose, and the
// left table's header is followed — across twenty-six cells the check cannot see — by
// the right table's header at the same y. That is the two columns read in the right
// order, and it has the exact shape of interleaving once the cells are dropped. So a
// table between two blocks now breaks the chain rather than closing over it.
func checkOrder(blocks []doc.Block) []Finding {
	return checkOrderWith(blocks, defaultOrderGuards)
}

func checkOrderWith(blocks []doc.Block, g orderGuards) []Finding {
	type key struct {
		page int
		x0   float64
	}
	groups := make(map[key][]int, 16)
	for i := range blocks {
		k := key{blocks[i].Page, blocks[i].RegionX0}
		groups[k] = append(groups[k], i)
	}

	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].page != keys[b].page {
			return keys[a].page < keys[b].page
		}
		return keys[a].x0 < keys[b].x0
	})

	var out []Finding
	for _, k := range keys {
		idx := groups[k]
		sort.Slice(idx, func(a, b int) bool { return blocks[idx[a]].Index < blocks[idx[b]].Index })
		judged := 0
		for j := range idx {
			if blocks[idx[j]].Kind != doc.BlockTable {
				judged++
			}
		}
		var prev *doc.Block
		for j := range idx {
			cur := &blocks[idx[j]]
			if cur.Kind == doc.BlockTable {
				prev = nil
				continue
			}
			last := prev
			prev = cur
			if last == nil {
				continue
			}
			if gapX(last, cur) < g.minGap || cur.Y0 < last.Y0-g.slack {
				continue
			}
			if last.Chars < g.minChars || cur.Chars < g.minChars {
				continue
			}
			out = append(out, Finding{
				Kind: KindReadingOrder, Page: cur.Page, RegionX0: cur.RegionX0, Index: cur.Index,
				Got: cur.Y0, Want: last.Y0, Count: 1, Total: judged,
				Sample: excerpt(last.Text + " → " + cur.Text),
				Detail: fmt.Sprintf("page %d region x=%.0f: block %d at x=%.0f-%.0f y=%.0f is "+
					"read after block %d at x=%.0f-%.0f y=%.0f — a different column, no "+
					"further up the page, which is what interleaving looks like",
					cur.Page, cur.RegionX0, cur.Index, cur.X0, cur.X1, cur.Y0,
					last.Index, last.X0, last.X1, last.Y0),
			})
		}
	}
	return out
}

// gapX is the horizontal distance between two blocks, negative when they overlap.
// A banner set across the measure overlaps every column, so it is never a switch.
func gapX(a, b *doc.Block) float64 {
	return math.Max(b.X0-a.X1, a.X0-b.X1)
}
