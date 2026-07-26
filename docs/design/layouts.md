# Manuals are not laid out the same way, and the pipeline has to know it

The ingest pipeline was designed against one document and worked perfectly on it.
The second real manual broke it comprehensively. This page records what differs,
what does not, and where the seam goes.

Two ordinary consumer-appliance manuals:

| | Dreame L40 Ultra | Thomas DryBox Amfibia |
|---|---|---|
| Pages | 560 | 68 |
| Languages | 34 | 5 — German, Polish, Russian, Ukrainian, Kazakh |
| Arrangement | sequential sections, 16 pages each | parallel columns, and not uniformly |
| A page holds | one language | one to three columns |
| Tagged PDF | yes | no |

Neither is exotic. The second is how European multi-language manuals are routinely
printed.

## What the second manual did to a pipeline built for the first

```
found 1 language          (there are 5)
56 of 68 pages unlabelled
content range 49-66       (the content is the whole document)
page-tag runs: 0
scope for de,uk: 11 of 68 pages, 16%
```

Four causes, each a design assumption rather than a bug:

**The page is the wrong unit.** Languages sit side by side, so a language run
defined as a contiguous span of pages cannot express "German on the left, Polish
on the right". You cannot skip a page to skip a language.

**Language changes every page.** So `minTagRunPages = 2` — the guard that stops a
contents page becoming a section — deletes the real signal here.

**Printed codes are one and three letters.** This manual marks its languages `D`,
`PL`, `RUS`, `UA`, `KAZ`. `looksLikeLanguageCode` requires exactly two, so it can
read two of the five.

**Script cannot separate German from Polish**, both Latin and on the same page.

One thing worked: `Unlabelled` reported 56. Under its earlier definition — bounded
by a content range derived from the labelled runs — it would have reported 0 and
the document would have looked fine.

## The column geometry, measured

Text columns per page, from `internal/doc/columns.go`:

| Text columns | Pages |
|---|---|
| 0 | 1 |
| 1 | 3 |
| 2 | 31 |
| 3 | 28 |
| 4 | 5 |

Column *widths* vary within the document — 262px on the three-column spreads,
403px on the wide two-column ones — so nothing may assume a fixed width, count or
pitch.

### The sectioned manual has columns too, and that was assumed away

The table above says a Dreame page holds one language, which is true, and it was
read as also meaning one column, which is false. Measured over all 560 pages once
positioned text could be extracted from it:

| Text columns | Pages |
|---|---|
| 0 | 6 |
| 1 | 148 |
| 2 | 136 |
| 3 | 199 |
| 4 | 71 |

A test asserting this manual was single-column was written and failed. Pages 20 and
100 rendered at `pdftoppm -r 108` settle what the numbers could not: both are two
side-by-side troubleshooting tables, and the regions returned are the tables' cells,
correctly located. On page 20 only the two wide answer cells come back, the narrow
question cells falling below `minColumnRuns` — that guard working, not failing.

So the assumption was wrong and the code was right. **Column count is not language
count, in both directions**: on the Thomas manual one page holds several languages,
and on the Dreame manual one language is set across several table cells. Any rule
keyed on how many columns a page has is wrong on one of these two documents.

An earlier version of this page published 11/16/40/1 for the same document. That
was wrong: it came from an ad-hoc script splitting at gaps wider than 90px, and the
real gutters here are 9 to 17px. Three approaches were needed before the numbers
held, and each failure is a trap worth keeping:

**Whitespace projection is binary, so one run welds two columns for ever.** A
heading set across the measure is enough. The fix is to count how many runs *cross*
each x and tolerate a few: a gutter is a band few runs cross, not none. Page 63 has
exactly one spanning run, page 68 has two.

**Left-alignment peaks over-split**, because alignment is a local statistic. Page
13 gives six peaks for three columns, each column having a hanging indent for its
numbered markers; page 63 gives a spurious peak 162px into the left column from a
nested list. No fixed "merge peaks closer than N" rule separates a 30px hanging
indent from a 162px sub-indent while keeping two real columns 280px apart. Crossing
count is page-wide, which is what a column boundary actually is.

**The text layer contains things that are not on the page**, and both kinds had to
be filtered before any geometry worked:

- *Production artifacts.* An InDesign filename slug and an export timestamp, 261
  occurrences each across 67 of 68 pages, 8% of all runs, several sitting in
  gutters. The obvious filter is wrong in both directions: "repeats across pages"
  also matches the printed `UA` and `PL` tags, while "repeats within a page" misses
  pages 6 and 41, which carry only two copies each, and would delete 742 of page
  68's 769 genuine runs, since that page legitimately prints a company name a dozen
  times. The discriminator is **height** — 522 runs at 2–6px against a body median
  of 17, being leftovers scaled down with placed artwork.
- *Off-page runs.* Page 68 parks 218 runs at negative coordinates, invisible in
  print, lying across two gutters. Filtering to the page box is the only filter
  that changes a column count.

## Figure callouts are not columns

A diagram's numbered callouts cluster like text. What separates them is how much
text a candidate column holds: real columns carry 1,116–3,058 characters, the
callouts on one exploded diagram carry 12 and 24. A parts list with short lines
sits between at 1,716, so a character count separates them where a median line
length would not.

## The seam: a geometry pass, then an assignment rule

An earlier draft proposed a `Layout` interface with an implementation per
arrangement, chosen per document by a scored `Detect`. That was wrong twice over.
A document contains several arrangements, so a per-file choice is confidently wrong
on every page it does not fit — and the interface bundled two things that fail
separately.

What exists instead:

**A geometry pass that knows nothing about language.** `DetectColumns` takes a
page's text runs and returns its columns. It is measured, testable alone, and
correct on all eight pages verified against renders. Whether a document is
"sectioned" or "parallel-column" is not a decision it makes — a sectioned page is
one column, and that falls out rather than being classified.

**An assignment rule above it**, deciding what a column *is*. That is where the
language signals attach, where a table cell must be told from a text column, and
where an honest refusal belongs.

The practical payoff of splitting them: the geometry pass shipped and was verified
before the assignment question was answered, and a failure in one is diagnosable
separately from a failure in the other.

The unit flowing downstream is a region — a page, a box, a language, a source. The
box is what makes the second manual expressible at all.

One thing changed from this sketch when it was built: the box for a sectioned manual
is not *absent* but spans zero to the page width. An absent box would put a null
check in every caller; a full-width one means a reader clipping text to the box gets
the whole page and needs no special case. See regions.md.

## Detecting arrangement from the printed index

The contents table gives it away cheaply. If several entries point at the same
page, the languages must share pages:

| | Index entries | Distinct targets | Claimed more than once |
|---|---|---|---|
| Dreame (sectioned) | 34 | 34 | 0 |
| Thomas (columns) | 87 | 34 | 14 |

**Provenance, because it matters here:** the Thomas numbers come from an ad-hoc
script counting lines that end in a page number, not from the shipping index
parser — which requires a two-letter code and could not produce 87 entries in a
five-language manual. The contrast is stark and the signal is real, but the
measurement has not been reproduced by the code that would rely on it.

Nor is it a general rule. A manual numbering each section from 1, or a
chapter-level contents table in a monolingual document, produces duplicate targets
with no columns at all. Corroboration, not classifier.

## Failing honestly, without regressing

An earlier draft made "unclassified" an implementation that labels nothing. That is
a regression rather than a safe fallback: a sectioned manual with page tags and no
contents table is labelled correctly today, and under that design nothing would
score, so it would produce zero languages. **Layout classification must never veto
a stronger signal that is already present.**

So the fallback is current behaviour — whole-page regions, labelled as they are
now — carrying an unclassified flag. What the flag drives is a *route*, not a
refusal: keep the original untouched, process with the local pipeline, or ask a
model. A document this pipeline finds hard should say so and offer the choice
rather than return a confident wrong answer.

Whether a model is in fact more reliable on the hard cases is untested. It is
plausible, and it is measurable — two manuals now have recorded ground truth — so
it should be measured before it is offered as the better route.

## What is still not understood

**Geometry cannot tell a table cell from a text column.** Pages 57–61 are
troubleshooting tables: two side-by-side tables of two cells each, which is why the
distribution above has five four-column pages. This belongs above the geometry pass,
and it is now answered there — not by learning to recognise a table, but by never
asking. A page divides only where its columns name more than one *language*, so a
table of same-language cells is one region however many cells it has. That disposes
of the case on both manuals at once: Thomas's pages 57–61 and the 406 Dreame pages
that read as two or more columns.

What remains unsolved is a table whose cells are in *different* languages, which
would divide on language and be wrong to. Neither manual does it. It is recorded
here rather than guarded against, because a guard would be written against an
imagined document.

The narrow-cell language error survives: a cell of German read as Finnish, sharing
only ä and ö, which gives the repertoire signal too little to discriminate. An
attempt to fix it by requiring more evidence failed on measurement — over 685
labelled columns the signal is 93% accurate and its mistakes are spread across every
amount of evidence, including one with 118 distinctive characters. There is no
threshold to find. See language-detection.md.

**Everything here generalises from two documents**, one of which took three
attempts to measure correctly. The numbers are real; their generality is not.

## Status

Built and verified: the geometry pass, `internal/doc/columns.go`, correct on all
eight pages checked against renders — and now checked against runs extracted from
the real PDF by `internal/doc/runs.go` rather than typed into a test, which is what
makes that verification non-circular.

Built: the region model and the assignment rule, `internal/doc/regions.go`. The
column manual's five languages read back across its parallel columns; the sectioned
manual produces exactly one whole-page region per page and its language map is
unchanged.

Designed, not built: the routing by complexity.

`testdata/fixtures/thomas-drybox-amfibia.json` records per-page ground truth with
provenance — eight pages verified by eye, the remainder marked as detector output,
so that nothing is ever tested against its own answer.
