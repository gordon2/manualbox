package doc

// SCRATCH: deleted before commit. Lets the measurement tool sweep the merge
// threshold over a whole document without paying for a Go test harness.

// FindFiguresMerge is [FindFigures] with the merge threshold overridden.
func FindFiguresMerge(ink []Ink, page *PageRuns, overlap float64) []Figure {
	g := defaultGuards
	g.mergeOverlap = overlap
	return findFigures(ink, page, g)
}
