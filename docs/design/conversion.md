# Turning a manual into something you can read

Contract for the next change, written before it is built, in the pattern
[regions.md](regions.md) set. The probe answers *what is in this document*.
Conversion answers *let me read it* — and it is the first stage that produces
something a user looks at rather than decides on.

Prerequisites are done and committed: the language map, regions (which part of a
page is which language), and the font each run is set in. Conversion is what those
were for.

## What it produces

**Blocks, not a page image and not a PDF viewer.** A block is one piece of
readable content: a heading, a paragraph, a list item, a table, a picture, a
caption. In document order, with the original's page number kept so a reader can
say "page 47" and mean the same thing the paper does.

That choice is already implied by what comes after. [ingest.md](ingest.md) says
extraction must be able to cite *"a paragraph rather than a document"*, and
full-text search wants the same units. A rendered page image satisfies neither, and
a single blob of text per page satisfies neither.

## The decisions

**Only the regions in scope are converted.** This is the whole funnel. A household
that reads German gets the German column of each page — not the page, and not the
other four languages sharing it. The measurement that justifies the funnel is
already recorded: 47,641 characters of the column manual against 240,622 in all its
languages, so converting one language is a fifth of the work.

**Reading order comes from the columns inside a region, not from the region's box.**
The first version of this contract said "within a region's box" and that was wrong —
corrected here because it was caught in implementation rather than in review.

A region is not always one column. [regions.md](regions.md) rule 3 deliberately
stores a page whose columns are all the *same* language as one whole-page region:
the column manual's page 62 is two columns of German at x=43-443 and x=463-863, and
it is stored as a single region spanning 0-892. Sorting runs down-then-across inside
*that* box interleaves the two columns line by line — which is precisely the
`pdftotext -layout` failure this section exists to avoid, committed under another
name. Measured on that page: its two columns run body lines at a 16-unit pitch whose
baselines drift apart across the gutter (102/102, then 118/120), and interleaving
produces `"rial bitte umweltgerecht. sich bei gewerblicher Benutzung oder
gleichzusetzender Beanspruchung…"`. The sequential manual has the same shape on the
199 pages that read as three columns.

So a region is subdivided by [DetectColumns] first, and reading order runs down each
column in turn. Lines within a column are grouped by shared baseline, using the same
rule `columns.go` already uses to fold a list marker into its text.

That rule 3 is still right — a page of same-language columns is one *language*
territory — is what makes this a seam rather than a contradiction: the region says
which language and how much text, the columns inside it say in what order to read it.

**A heading is found by weight and by length, not by size — and there is no size
floor either.** Size alone is known to be wrong here, and the counter-example is
measured: on the sequential manual, 17pt text is 11.4% of the document at 70
characters per run — safety body copy, which "larger than body means heading" would
promote to a heading. (Its weight reads as *unknown* rather than regular: the face is
plainly `MiSans` and states nothing, and unknown sorts below light, which matters to
any rule comparing weights.) Its real headings are 15pt semibold, 1,268 runs at
**15.5 characters per run**.

The tempting corollary — that a heading is at least as large as the body — is also
wrong, and costs 80 real headings. The sequential manual's safety pages are set
entirely in 17pt, so on those pages 17pt *is* the body, and their real subheadings
(`Nutzungsbeschränkungen`) are 15pt semibold: **smaller than the text they head.** Characters per run is what
separates a heading from same-size emphasis, and on the column manual the same holds:
18pt bold at 17.8 characters per run are headings, while 14pt medium at 43.8 is
emphasis and table labels.

**Both weight signals are needed, because the two manuals disagree about which one
exists.** The column manual names its faces honestly — `FuturaCon-Bol`,
`FuturaCon-Med` — and 93.4% of its characters are in a face that states a weight.
The sequential manual does not: 73.2% of its characters are in a face called plainly
`MiSans`, and poppler's own `<b>` marking is the only weight there is. Either signal
alone fails on one of the two documents.

**Tables come from the ruled lines, which are a different input rather than a better
use of the old one.** [layouts.md](layouts.md) and [regions.md](regions.md) both
record that *geometry* cannot tell a table cell from a text column, and that remains
true. Vector rules are not geometry of the text; they are the lines the document
draws. `pdftohtml -xml` reports none of them; `pdftocairo` reports them exactly, in
PDF points, which is this space divided by 1.5. Measured against renders:

| page | printed cells | recovered |
|---|---|---|
| column manual 57 | 29 | 25 — the misses are two header rows whose top border is not drawn |
| sequential 20 | 12 | 12 |
| sequential 100 | 16 | 16 |
| sequential 21 | 32 | 32 |
| sequential 15 | 47 | 37 — the misses are exactly the vertically merged cells |

**A table needs a text guard as well as a shape guard, and this is not optional.**
"Has ruled lines" fires on 68 of the column manual's 68 pages. Requiring a table
shape — at least two columns, two rows, four cells of a legible size — leaves 13,
and three of those are false in an instructive way: pages 22, 38 and 44 are **grids
of framed illustrations**, ruled by exactly the evidence a table gives. What
separates them is whether the cells hold words: 14 of their 15 cells contain zero
characters, while all 12 cells of page 57's table contain text. With both guards:
10 pages of the column manual, 170 of the sequential one — which is 34 languages
times 5 table pages, exactly.

**Blocks are keyed naturally, so re-converting converges.** Same reasoning as
`doc_regions`, and the same reason: a job handler can run twice. The key is the
document, the page, the region's left edge and the block's index within it. A
surrogate ID would make a second conversion insert a parallel set. This is also
what gives extraction the stable block IDs ingest.md asks for.

**Conversion runs after the gate, never before it.** It is the first thing in this
pipeline that is not free, and the gate exists precisely so a user authorises it.
The document states `converting` and `ready` have been in the schema since `00002`
with nothing setting them; this is what sets them.

**Cost — and the free probe now pays some of it, which this section originally
denied.** Reading the ruled lines costs 6.1 s over all 68 pages of the column manual
and 36 s over all 560 of the sequential one, against a probe of about 4 s for either.

The first version of this said conversion runs only over the pages in scope, so the
pre-flight is never slowed. That became false the moment tables were needed to derive
regions — which they are, because a table's cell dividers would otherwise be read as
language boundaries, and regions are computed by the probe. Reading every page's rules
there would take the sequential manual's probe from 3.6 s to about 46 s: a tenfold
regression of the one thing the design insists is free.

So the rules are read **lazily, only for a page that just divided into more than one
column** — the only place a table can change a stored answer. Measured: 44 of the
column manual's 68 pages, and **0 of the sequential manual's 560**, since none of its
pages divide by language. Its probe is untouched at 3.60 s. The column manual's goes
from 4.09 s to **8.02 s**, and that is the honest price of reading a
parallel-columns manual correctly.

Converting the pages in scope is still separate, still after the gate, and still only
over the pages asked for.

A 6.5x saving exists for that later pass and is not yet verified: one
`pdftocairo -ps` call renders all 560 pages in 5.15 s where per-page SVG spawns take
33.5 s, and the strokes survive, but no PostScript parser has been written.

## What this deliberately does not solve

Recorded so the next person does not think these are unsolved by accident.

**A table with no ruled lines is invisible.** The column manual prints
`Technische Daten` as label/value pairs — `Spannungsversorgung: | 230 V, 50 Hz` —
with no rule anywhere, and nothing detects them. Not five separate spec pages, as an
earlier draft of this said: it is one spec table repeated per language, a block within
the disposal-and-warranty page — 62 German, 63 Polish, 65 Ukrainian. This is accepted rather than solved, and the softening is real: those
pages still *read* correctly, as lines of text, which is how they read on paper. What
is lost is answering "what is the tank capacity" from a cell later.

A text-only signal was looked for and not found. Row alignment points the wrong way:
the table page scores 29-40% mutual band alignment while three parallel translated
columns score 67% and 100%, because a translated paragraph corresponds to its
neighbour and a two-line question cell does not correspond to a ten-line answer. A
per-column tab-stop streak does find all five specification tables, but also fires on
three pages of numbered lists — it separates a spec table from body text, not from a
list.

**Vertically merged cells are dropped.** 10 of 47 on one measured page. An omission
in the row walk rather than a limit of the data; the fix is a column-direction twin
of the same walk.

**A header row with no top border loses its cells.** Four of 29 on the measured page.
The shaded cell backgrounds are filled rectangles present in the same output and
would recover them.

**Framed illustrations are geometrically identical to tables.** Only the text guard
separates them, and a figure with a caption inside its frame would defeat it.

**Language-neutral content still has no home, and measuring the pictures showed how
much that costs.** regions.md left this open and conversion inherits it: a diagram or
a specification table shared by all five languages belongs to a region only if it
happens to sit inside one, and a picture spanning the full measure belongs to none.

The size of it, measured: the sequential manual has **229 figures on 23 of its 560
pages, and every one is in front or back matter.** Its 34 language sections carry
prose and ruled tables and not a single illustration. So a language-scoped conversion
of that document shows a reader **no pictures at all** — which is not a bug in any of
the code, it is the funnel doing exactly what it was told on a document whose pictures
are shared by every language. The column manual is the opposite: 45 figures on 27 of
68 pages, sitting inside the language columns where they belong.

That is a product decision rather than a technical one, and it is not taken here.

**The pictures in a manual are not the images in the file.** `pdfimages` — the
obvious tool, registered in `extern` since before any of this — yields **zero
illustrations across all 628 pages of both manuals.** What it does yield is 1,358
gradient-mesh slivers of 12x4 pixels on two pages, a 97x73 corner logo, some CE marks
and recycling symbols. Page 42 of the column manual prints four framed line drawings
and reports zero embedded images. Every illustration in both documents is **vector**,
so a figure is found the same way a table is — from what the page draws — and its
bytes come from rendering the crop.

Two consequences worth stating. `pdfimages` is not useless, but its role is the raster
path for a photographed or scanned manual, which neither fixture is. And a caller that
wants both tables and figures pays `pdftocairo` twice for the same page; that is
accepted for now and recorded rather than optimised.

**Clip paths are not read, and that is the real cost of the vector route.** A figure's
box is a path's *unclipped* extent, so neighbouring drawings merge — page 42 returns 3
figures for 4 printed, page 16 returns 1 for 3 — and a figure can reach over adjacent
text. Trimming to the drawn content takes that overlap from 19 of 46 figures to 8. The
proper fix is for the SVG reader to keep the `clip-path` attribute it currently drops.

**No translation, no search, no OCR.** Translation is M3. Search needs an FTS5 table
that does not exist yet — SQLite has the extension compiled in and nothing uses it.
A scanned manual with no text layer needs OCR before any of this applies, and the
tesseract binary is registered but called from nowhere.

**Nothing here renders right to left.** The reader is a separate slice, and the
frontend has no direction handling at all — no `dir` attribute, no logical
properties, every margin physical. That matters because this project's own pitch
includes Hebrew and Arabic manuals, and retrofitting direction into a screen built
with one-sided margins is worse than building it in.

## Two corrections to what was already recorded

Found while measuring, and both concern the column fixture:

- Its tables are on **pages 52-61, not 57-61**. Pages 52-56 carry genuine small
  tables (`Anwendungsfall | Düse/Zubehör`) that the manifest never recorded.
- It prints an **unruled specification table** that nothing had recorded at all, as a
  block within its disposal-and-warranty page, once per language: 62 German, 63
  Polish, 65 Ukrainian.

## What building the first half settled

**Page furniture is not identified, and one page cannot identify it.** The printed
`DE` badge comes back as a level-2 heading on 110 pages, the folio as a one-character
paragraph, the running head as a paragraph. Nothing *on a page* separates those from
content — the sequential manual genuinely titles sections `A`, `B` and `C` — and what
does identify furniture is repetition in the same position *across* pages, which is a
different input than a single region's runs. Left for the pass that has the whole
document in view.

**A paragraph break cannot always be found.** The gap factor is 1.2 of the measured
line pitch, and on the column manual's page 62 that resolves paragraphs separated by
20-21 units against a 16-unit pitch — but 17-unit gaps occur both inside and between
paragraphs on that same page, so **no factor separates those**, and two paragraphs 18
units apart stay joined. Chosen as the smaller error: a missed break reads as a long
paragraph, an invented one splits a sentence.

**The pitch must be the mode, not the median.** Page 62's left column has nearly as
many paragraph breaks as body lines, so its median gap is 18 against a real pitch of
16 — high enough to swallow the 20-21 unit breaks it needs to find. The mode is 16.

**A heading's share of the measure is a soft cut with no gap to put it in.** Every
candidate's width as a fraction of its column is a smooth continuum from 5% to 100%
on both manuals — 33 candidates at 60-64%, 25 at 65-69%, 116 at 95-99% — and rune
counts are no better. 0.6 is chosen for precision, and its cost is named: a heading
that fills a narrow column reads as a paragraph, which loses `Fehlersuche`,
`Feilsøking` and `Depanare`.

**Hyphenation is not undone.** `brud-` / `nej` survives as written, because German
legitimately ends a line with a hyphen and rejoining would corrupt those.

## The integration this leaves, and the thing it explains

Blocks and cells were built separately and deliberately do not know about each
other. Joining them is the next step, and measuring both halves against page 57 of
the column manual settled what that join has to be — and incidentally explained a
misreading this project has been carrying since the language work.

**A table's cell dividers are being read as language columns.** Page 57's four
stored language regions and its two tables' cell columns are the same boundaries:

| stored region | table cell column |
|---|---|
| 36-178, read as Finnish | 29.7-173.3 — table 1's question cells |
| 179-424, German | 173.3-428.1 — table 1's answer cells |
| 457-589, German | 450.2-593.9 — table 2's question cells |
| 601-846, German | 593.9-848.7 — table 2's answer cells |

Within about five units on all four. And 173.3 is not incidental: it is the only
interior vertical the left table draws, arriving as six segments broken at each row
and recovered only by merging them.

So that page has **no language columns**. It has two tables, and the column detector
found their cell dividers. That is the root of the one language error both
`layouts.md` and the fixture record — a German cell read as Finnish — one level
below the explanation already written down. Both causes are real and they compound:
the printed `D` in the page's corner is rejected for want of an index vocabulary,
*and* the thing whose language is being asked about is a column of short table
labels rather than a column of prose.

**Therefore a table's cell BOUNDARIES are excluded from region derivation, not
reconciled with it afterwards.** Joining tables to regions afterwards would be joining
a table to boundaries the table itself created. This touches `regions.go` and is the
one place the two halves genuinely meet.

*Boundaries, not area* — an earlier draft said area, and that is not implementable
without a migration: a region is one x-range and cannot have a table-sized hole in it.
What is excluded is the table's interior dividers from the set of candidates a page
may divide on, before any region exists. Two guards stop that welding two genuine
language columns a table happens to cross: only dividers inside the table's own box
are candidates, and a gutter is merged away only if a cell divider sits within 1% of
the page width of it — 8.9 units, against the 5.2 by which page 57's widest
coincidence misses.

Subtracting the area would also have been actively wrong, and by a lot. Pages 58-61 of
the column manual are tables covering nearly the whole measure, and it is their *cell
columns* — all one language — that name the page under rule 3. Remove the table's text
and what is left is a running head, which falls below `minColumnRuns` and loses 3,502,
3,691, 3,668 and 3,339 characters of Polish, Russian, Ukrainian and Kazakh. Four pages
would have lost their language to tidy one.

**Result, measured end to end on a clean database.** Page 57 goes from four regions —
`fi` at 36-178 and German at 179-424, 457-589, 601-846 — to **one whole-page German
region of 3,618 characters**. With the page's German finally together the alphabet has
`ü×17` and `ß` to read, so it names German confidently. The document goes from 6
languages to 5; `fi` is gone and nothing replaced it. The gate reports German at
47,932 characters instead of 47,641. The sequential manual's dump is identical: 560
regions, 0 boxed, 34 languages, every section's pages and spans unchanged.

Three constraints on that join, all measured rather than reasoned:

- **It must be geometric, not by key.** Blocks key on `(page, region left edge,
  index)` and a table area has no region left edge, so there is no key to join on.
- **Both halves already draw from the same filtered run set** — each calls
  `usableRuns` — so cells and blocks can never see text the other cannot. That is
  also what keeps both inside what the gate charged for, since a region's character
  count comes through the same filter.
- **A heading printed across a table must not be assigned to a cell.** It lands in
  the banner group that is read first and is dropped from every cell, so it appears
  exactly once and in the right place. An integration that tries to place it in a
  cell, or that suppresses banner blocks wherever a table covers the region, makes
  it vanish.

One trap for whoever builds it: page 57 draws a **real vertical at x=440.2 spanning
the full page height**, between the two tables. The cell grid correctly ignores it —
no horizontal rule spans 428.1 to 450.2, so it bounds no cell — but any page-wide
column projection will find it and read the page as split at 440. A convincing
divider that is neither a language boundary nor a table one.

## Acceptance

Not "it produces blocks". The column manual's German must come back as readable
content in reading order — headings as headings, its troubleshooting tables as tables
with the right cells — from the German column alone, with no Polish, Russian,
Ukrainian or Kazakh text in it. The sequential manual's German section must come back
the same way from a page it owns outright. Both checked against renders of the pages,
not only against counts.

And the negative: no page of the column manual may contribute text from a language
that was not asked for. That is the funnel's whole promise, and it is the one failure
a reader would notice immediately.
