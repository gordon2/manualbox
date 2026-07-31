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

**A figure is not a block, and `BlockFigure` stays unproduced.** A block's natural key
is the page, the region's left edge and the index within that region — and a
language-neutral figure has no region, so giving it one would either invent a key or
collide with a real block's. Figures come back as their own list, and a reader merges
the two by page and vertical position. `blocks.go` says `BlockFigure` is declared and
never produced; that is still true and is now deliberate rather than pending.

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
over the pages asked for — with one cost this section also omitted: `Convert` pays
**one `pdftohtml` pass over the whole document**, because the probe's `Result`
deliberately does not carry the runs. That is why converting the sequential manual's
German is 3.3 s for 16 pages while its Russian is 13.1 s for 22: the difference is 81
figure renders, not pages.

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

**A picture that belongs to no language belongs to every language — of the pages that
language was already going to read.** Decided by the user, and it settles what
regions.md left open: language-neutral content is included in **every** language's
conversion rather than assigned to one or dropped. A reader must not lose a diagram
because the diagram has no language of its own.

**The scope of "every" is a page, not the document, and that is a deliberate limit
with a measured cost.** The column manual sets pages 14 and 15 as a spread: page 14 is
German and Polish *plus two photographs of the machine*, and page 15 is Russian,
Ukrainian and Kazakh with **no pictures at all**. The same instructions in five
languages, illustrated once. So the pictures serving all five sit physically on the
German page, and a page-scoped rule cannot reach them from Russian:

| household | figures from the column manual |
|---|---|
| German | 53, of which 51 neutral |
| Polish | 54, of which 51 neutral |
| **Russian** | **1** |
| **Ukrainian** | **1** |

Those first two numbers were 40 and 41 when this was written and the shape of the
finding is unchanged; reading the clip split merged drawings apart and took that
document from 46 figures to 59, so every per-household count here rose with it.

Closing that automatically costs either every page's ink for every household — 68
`pdftocairo` spawns where 52 were charged here, and 1,120 on the sequential manual to
find its 3 pages of neutral figures — or a facing-page association, which would be a
rule invented from one manual's binding.

**Neither is being built, and the intended answer is different.** The user's direction:
let a reader skim the original and choose pages to convert by hand — having found those
photographs on page 14, ask for exactly them. That handles this case and every case
like it, without a heuristic guessing at what a spread is, and it is the right shape
for a feature that has to be honest about a document it has never seen. Unbuilt, and
recorded here so nobody builds the expensive guess instead.

**An earlier version of this section claimed the opposite of the truth and is
corrected here.** It said the sequential manual's 229 figures were "every one in front
or back matter", so a language-scoped conversion of it would show no pictures at all.
That was read off page numbers without checking which pages the language sections
actually occupy, and it is wrong. Measured properly, over all 560 pages:

| | |
|---|---|
| figures | 195 |
| figure pages **inside** a language section | **20** |
| figure pages outside one | 3 |
| Russian | **65 figures** |
| Japanese | **69 figures** |
| the other 32 languages | none |

That total read 229 when this was measured, and the two per-section counts 81 and 82.
All three fell when candidate boxes that overlap were merged — a drawing that had
clustered in pieces is one figure now — and the page counts did not move at all,
which is what says these are the same pictures counted correctly rather than
pictures lost.

So a Russian or Japanese reader of that manual gets a heavily illustrated section, and
the other 32 get none — because those two sections genuinely carry illustrations and
the rest genuinely do not. The lesson is narrower than the claim it replaces: figures
outside a section are the exception here, not the rule.

**Some languages have more content than others, and it must not be lost.** Russian
occupies 22 pages of that manual and Japanese 21, where the other 32 languages get 16
— the extra pages are an illustrated maintenance section that exists only in those
two. PDF page 533 is an example: Russian prose with eight line drawings of the robot,
the waste tank and the vents. Verified: those pages fall inside the stored Russian
region span of 517-538, and their figures are found.

No attempt is made to audit every language for such extras. The requirement is
weaker and achievable: **whatever a household's own language contains must be read and
processed, however unlike the other languages' sections it is.** Nothing may assume
the sections are alike, and the 16-page assumption is exactly what would have hidden
this.

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

**Clip paths ARE read now, and it was the largest visible defect in the output.** This
section previously recorded the opposite as an accepted cost. A figure's box was a
path's *unclipped* extent, so drawings merged and were cropped through their own
artwork. The verifier put a number on it — 22 of 46 figures and 74 of 163 cut off — and
`clip.go` now resolves each shape's effective clip and intersects the path's extent
with it. Measured end to end with `manualbox verify`:

| | columns manual | sequential manual |
|---|---|---|
| figures | 46 → **59** | 163 → **168** |
| pages carrying figures | 27 → 27 | 20 → 20 |
| cut off by their own crop | 22 → **15** | 74 → **71** |
| carrying a blank band | 4 → **0** | 6 → **2** |

The count rises while the page count does not, which is what tells a split from newly
admitted furniture: page 42 returns its four printed drawings where it returned three,
page 22 three for three, and page 16 **four for four** — that page prints four framed
panels, not the three an earlier version of this document twice claimed.

It also fixed a table: page 38 draws a frame edge to y=268.7 while the paint stops at
y=239.06, and those 30 units of phantom rule were closing a cell. Verified against a
432 dpi render. All five ground-truth cell counts are unchanged.

Two simplifications, stated: a clip is reduced to its **bounding box**, which can only
ever make a figure's box smaller than the unclipped extent and never wrongly larger; and
an unresolvable reference or an `objectBoundingBox` clip means *no clip*, which is the
old recorded wrongness rather than a guess that could erase a picture.

**The residual cut figures were not the clip either — they were `trimToPicture`**, the
patch written for the cause the clip removed, and it is now fixed rather than removed.
It cut into drawings to exclude labels printed inside them: page 16's third panel lost
its right third, arrow and hose tip, to exclude the label `»click«`.

**A trim now only pulls in an edge that a text line actually reaches PAST**, and that
rule follows from where the box comes from rather than being tuned. The box *is* the
bounding box of the drawn ink, so a line the box merely reached over must stick out of
it, while a label set inside the artwork cannot. Result: cut figures **15 → 3** on the
columns manual and 71 → 70 on the sequential, with figures, pages and blocks all
unmoved. Seven of the columns manual's thirteen bad trims go away and all six good ones
stay — page 52 still loses the German prose line above its diagram, and now keeps the
nozzle top and three labels the old rule amputated.

**The obvious rule — a label inside a drawing has ink on more than one side — was tried
and is wrong.** `»click«` on page 16 has ink on all four sides, but the same label on
pages 24, 26 and 36 sits flush at a drawing's right edge with ink on only two, while
page 1's `GEBRAUCHSANLEITUNG`, which is genuinely prose, also has ink on two. Those need
opposite answers and that signal gives them the same one.

**Two measurements here were misleading and are corrected.** "Figures overlapping prose"
cannot judge this: it counts any run of five runes or more, so a picture keeping its own
seven-rune `»click«` scores exactly like one swallowing a paragraph — it rises 9 → 14
*because* the fix works, while the prose genuinely excluded stays at 6. And the fixture
pin recording the smallest figure's short side as 128 units was measuring page 52's
diagram **amputated by the trim**; the document's real smallest drawing is page 48's at
130.4, which no trim ever touched.

The three residual cut figures on the columns manual are pages 11 and 12, where a
page-sized path cannot be attributed to one figure, and page 1, whose cover art genuinely
runs behind the title block.

**A drawing was also being served in pieces, which a user reported before any test
caught it.** Page 524 of the sequential manual returned six boxes for four printed
drawings, and one of them was *a hand* — part of the robot's underside, cut out and
served as its own picture while still inside the parent.

The cause was not a missing merge step. `clusterInk` joins *shapes* whose boxes meet, but
a group's box is the union of its shapes and is far larger than any one of them, so two
groups can share most of a rectangle while no shape of one touches a shape of the other.
The same rule run again over the clustering's own output, to a fixpoint, closes it.

| sequential manual | before | after |
|---|---|---|
| figures | 168 | **134** |
| overlapping pairs | 53 | **0** |
| wholly inside another | 7 | **0** |
| cut off by their crop | 70 | **25** |

The columns manual is identical in every number; it never had an overlapping pair at any
threshold. Clipped falling to 25 is not a detector agreeing with itself: a piece of a
drawing is genuinely crossed by the shapes of the piece beside it, so removing the split
removes the crossing.

**Any positive overlap merges; merely touching does not.** No fraction was chosen,
because the measurement offers nothing for one to separate: the 53 pairs run from 1.00
down to 0.01 with no gap, and every one, rendered and looked at, is a single printed
drawing that clustered in pieces — at 0.91 the hand, at 0.57 a base station split at its
waist, at 0.01 a water tank and the magnified detail its leader lines run to. The cases
needing the opposite answer are untouched because their boxes do not overlap at all:
page 524's two robot views are 23 units apart, page 522's two mop pads 46.

**Containment alone would not have fixed the reported fault.** The hand is 90.8% inside
its parent, not 100% — its pin pokes 4 units past the edge. A containment-only rule leaves
that page at six boxes with the hand still served as a picture.

**And the merge exposed a determinism bug.** The groups came out of a map, which is
harmless while merging only grows a box, and is not once a threshold is involved: the
same page returned between 194 and 200 figures across runs. Clustering now sorts into
reading order before merging. That matters beyond a flaky test — these bytes go into a
content-addressed store, so a box that moves means the same page yields different files.

Two eye counts already in the repo were counting boxes rather than drawings and are
corrected: a page recorded as 8 drawings prints 4, and one recorded as 8 prints 9.

**A CALLOUT NUMBER WAS BEING CROPPED AWAY, and it made a labelled diagram
unreadable.** Reported by the user against the sequential manual's RU product
overview: the crop keeps the leader lines and loses every label, so the leaders end
in nothing and the drawing cannot be read against its parts.

The cause is not a bad box, and that is the useful part. A figure's box is the
bounding box of the drawn **ink**; a label is a **text run**. On PDF page 521 the
lidar drawing's box ends at x=263.0, its leader terminators are the marks at
259.6–263.0 that *set* that edge, and all eleven of its labels begin at **266.0**.
Three units, every one. The box does not need to find the leader's end — it is
already sitting on it. It needs to cross the gap, and nothing in `findFigures` ever
grew a box: `trimToPicture` only ever pulls edges in.

**What says a run is a label is the terminator, not the distance.** Both documents
draw a small open circle where a leader stops — 3.3 to 3.4 units square, measured —
and one sitting in the corridor between the box's edge and a run, on that run's
midline, is what claims the run. The case that rules the distance out is a document:
page 11 of the columns manual prints its **parts list**, 39 numbers and 39 German
names, 22.3 units to the right of the exploded view — *inside* the range page 521's
underside diagram holds its own labels at, 20.3–35.3. It is not that the legend sits
further away; it is that no distance separates the two, so any "grow onto text within
N units" rule swallows the whole list. The terminator test refuses all 78 of its
runs, because a legend is not pointed at.

**A label wraps, and its later lines are the obstacle.** Page 521's lidar drawing
claims nine of its eleven labels by terminator; the two continuation lines
(`на основе ИИ`, `3D-датчики`) carry no mark of their own and, left unclaimed, block
the edge from moving at all. So a run flush with a claimed label, on the adjacent
line, **alone on its baseline**, is part of it. Alone is what separates a label from
a bulleted description: a bullet has its text beside it 1 unit away, a continuation
line does not. Comparing bands rather than baselines gets this wrong in a way worth
recording — two consecutive lines of one label overlap vertically, because a run is
taller than the pitch it is set at, so a band test reports a label's own third line
as something sharing the second's line and blocks every growth on the page.

**The conservative half is that prose stops an edge dead.** An edge moves only if
everything the growth region touches is a claimed label; one line of prose in the way
and the edge stays. That is why page 521's lid-open drawing keeps its three
right-hand labels cropped — the corridor holds `Кнопка сброса` and then the five
bullet lines explaining it — while its left edge takes nine labels.

**A claimed label may be cut short; prose may not.** On a page whose two label
columns interleave in x this is the difference between the fix working and not
working: the lidar drawing reaches x=397 where its own longest label ends at 469,
because the neighbouring drawing's labels start at 400. Refusing to cut a label at
all was measured, and it costs the whole page — that drawing does not grow, and
neither does its neighbour's left edge. A leader ending in a word cut short is a
large improvement on a leader ending in nothing; a picture with a paragraph in it is
not.

What it is worth, over both whole documents:

| | columns manual | sequential manual |
|---|---|---|
| figures | 59 | 195 |
| figures with a label outside them | 2 | 79 |
| figures grown | **0** | **55** |
| labels taken in | 0 | **229** |

Those 229 are labels the crop **reaches**, and the gap between reaching and
containing is exactly the clipping this accepts: page 521's three drawings reach 9, 11
and 14 labels and hold 3, 8 and 12 whole, so 23 of its 34 arrive uncut. It is the
number that would become 34 if a label were carried as text instead.

**The columns manual does not move at any setting**, which is the same shape of
evidence the merge rests on, so this is the other document's change entirely. Both of
its claims are FALSE and both are blocked, and that is the clearest statement of what
the conservative rule is for: page 1's cover figure claims the title
`РУКОВОДСТВО ПО ЭКСПЛУАТАЦИИ` and page 22's claims eight lines of German prose about
emptying the DryBOX. The terminator signal is not precise on its own — a small shape
near a line of text will do — and what makes it safe is that an edge does not move
unless the region it would add holds nothing but claims. Of the sequential
manual's 55, **22 are on pages 5 and 6** — the front-matter diagram plates, which
fall outside every language region and are never converted — leaving 33 on pages a
reader is served. Page 521's three drawings take 9, 11 and 14 labels.

**The cost is overlapping crops, and it is confined to those plates.** Eleven pairs
of grown boxes overlap — nine on page 5 and two on page 6, where 31 and 28 figures
share one sheet with labels between them, and one of page 5's is a crop wholly inside
another crop; none is on any page a conversion serves. The **drawn** boxes are
untouched: measured over both documents on every page they still overlap in 0 pairs
and nest in 0, so the merge pass's property holds of the rect it is about.
Widening the corridor is what would change that: at 80 units a pair appears on page
524, which a reader is served, and that is the upper bound's evidence.
Not fixed, because arbitrating which of two drawings a shared corridor belongs to
would be a rule invented for one plate.

**The rendered rectangle and the drawn one are now different things, and one caller
must not confuse them.** `Figure.Rect` is what was rendered and is what the stored
pixel size describes; `Figure.InkRect` is the drawn extent the two guards judged.
Attribution reads the drawn one, through `Figure.DrawnExtent`: a box grown sideways
onto a label could otherwise reach out of its own language column and be handed to
every household, which is the one failure the funnel may not have. A picture's
language is a property of the picture, not of how much of the page around it was
rendered. The ink box is not stored — nothing reading a conversion back asks the
language question again — so there is no migration.

**Growth runs after both guards, deliberately.** A diagram's own labels are text, so
a grown box is legitimately over `maxFigureTextFraction`: page 521's lidar drawing
reaches 0.162 with its eleven labels. Re-testing the grown box would reject the very
pictures this pass exists to complete, so the guards judge the drawing and growth is
what happens to a drawing that has already passed.

**A perfectly horizontal leader line is invisible to all of this, and fixing that was
measured and rejected.** `onPageInk` drops a shape whose box has no extent on one
axis, and an axis-aligned hairline is exactly that: page 521 carries 52 such shapes 8
units or longer, its leaders among them, and that is why the underside drawing's
terminators sit 28 units *outside* its box while the lidar drawing's sit on the edge.
Keeping them was tried. It costs the sequential manual **16 figures, 195 → 179**, and
the reason is that the restored shapes include the page's own furniture: page 5 draws
a zero-width column separator 402 units tall, and it bridges that page's middle
column into one 244×402 box where ten drawings were found before. The columns manual
does not move (59 → 59, identical per page). Not taken, because growth reaches the
labels without it — a terminator survives the filter on its own, being a circle
rather than a line.

**What is still cropped is counted, so it can only go down.** 41 figures of the
sequential manual hold 88 labels their leaders point at and their crop does not
reach — 16 of those figures and 29 of those labels on the two plates — and the columns
manual's 2 are its two false claims. Pinned in
`TestALabelOutsideTheFinalCropIsTheResidual`.

**What this still does not do is carry a label as text.** The complete answer is not
a wider crop: it is to keep each claimed label as a string with a position, let the
reader draw it beside the picture, and take it out of the block flow — which would
also make it translatable, searchable, correct in right-to-left, and free of every
rectangle conflict above. That is a schema, an API and a reader change, and it is
what should replace this pass rather than sit beside it.

**A CONTENTS PAGE IS READ AS THE LIST OF ENTRIES IT IS**, which it was not: the
columns manual's `Оглавление` arrived as one run-together paragraph of dot leaders,
`Мы поздравляем Вас ...........2 Использование по назначению ......4`, because its
seventeen entries sit at exactly the line pitch and the paragraph rule has nothing
else to separate them by.

**The signal is a dot leader plus a page reference, and it has a real gap under it** —
which almost nothing else in this package does. Measured over both whole documents,
every run of two or more dots: **3, 3, 3, 4, then 34 to 91, with nothing between.**
The four short ones are ellipses in prose and none carries a page number; the 85 long
ones are the contents entries, 17 per language across five languages. Both halves are
required anyway, because "a leader" and "a page at the end of it" are what an entry
is, and the next document gets no say in which of the two it breaks.

**The sequential manual cannot trigger it at all**, and that is a property rather
than luck: its longest dot run anywhere is two. Its own contents page sets the page
number in a separate column at x=851 against a title at x=89, with no leader between,
so it needs the tab-stop signal this does not attempt — and it is front matter that
falls outside every language region, so no conversion serves it.

**It is not a sixth `BlockKind`, and the reason is a cost worth knowing.** A contents
entry IS a list item, the paper prints a list, and the note says which sort — exactly
as it already says `opens with the list marker "•"`. A kind of its own would reach a
database column whose CHECK lists the five by name, and widening a closed set there
costs a table rebuild. For `doc_blocks` that means dropping and recreating `00006`'s
three FTS triggers and reindexing a search table that is external-content over this
table's rowids. Migration `00003` is the precedent and records the procedure. Nothing
here needs it; a later change that wants the kind knows the price.

Measured: **+16 content blocks per language, +80 over the columns manual's five**, and
coverage does not move — the dots are still in the block's text, only grouped
differently. The reader drops them from the DOM and draws the leader with a rule,
because a row of literal periods is noise to a screen reader.

**The page number is not a link, and that is the honest half.** It is the number
printed on the paper; jumping needs the printed page mapped onto a PDF page, which is
what `Reconcile` already does for the language map and is not wired through to here.
Shown as the paper says it, so a reader can find the page by hand. That mapping is the
next step and it is also what the printed-index parser needs — see
language-detection.md, where the same page defeats it for a different consumer.

**No translation, no search, no OCR.** Translation is M3. Search needs an FTS5 table
that does not exist yet — SQLite has the extension compiled in and nothing uses it.
A scanned manual with no text layer needs OCR before any of this applies, and the
tesseract binary is registered but called from nowhere.

**RIGHT-TO-LEFT TEXT IS EXTRACTED BACKWARDS, and no amount of care in the view can
fix it.** Found by building the reader, and it is a defect in the pipeline rather than
a limitation of it.

`pdftohtml -xml` — the tool every block, column and region reads from — returns a
right-to-left line in **visual** order. Page 185 of the sequential manual, its Hebrew
section, arrives as `שומיש תולבגה`; reversed rune for rune that is `הגבלות שימוש`,
"usage restrictions", which is what the page prints. `pdftotext` on the same page
returns the logical string correctly, wrapped in the bidi controls U+202B and U+202C.
Arabic is worse: it arrives both reversed and unshaped, in isolated rather than
presentation forms.

No `dir` value repairs it. The bidi algorithm reorders a strong RTL run under either
base direction, so `dir="rtl"` displays the mirrored letters and `dir="ltr"` displays
the same mirrored letters flush left.

And reversing in the view would be wrong twice over: it mangles the Latin words and
digits these manuals mix into RTL prose, and it double-reverses the day the extraction
is fixed. **The fix belongs in `internal/doc`** — either reverse an RTL run's runes at
extraction, or take the order from `pdftotext`'s bidi-controlled output.

**It is now built, at the one place a line's order is decided, and it is measured to
zero.** `internal/doc/bidi.go` reverses a right-to-left line and puts its
left-to-right islands back, so `8` stays `8` and `MopExtend` stays `MopExtend`; that
file's header carries the measurements. Over the whole sequential manual, in the two
steps it took:

| | before | line's own majority | region's language |
|---|---|---|---|
| pages `verify` reports reversed | 32 | 6 | **0** |
| words absent from `pdftotext` on them | 8,120 | 80 | — |
| ...of those, present when reversed | 7,938 | 18 | **0** |
| `מדריך` found typed forwards | 0 blocks | 4 | **5** |
| `מדריך` found typed backwards | 5 blocks | 1 | **0** |

The middle column is worth keeping because it is a lesson about authority. Deciding a
line's direction by the majority of its own strong characters looks safe and is not: a
Hebrew line carrying a URL has more Latin than Hebrew, so it was read left to right
and never repaired. Those six lines — the support URL under a Hebrew sentence on page
188 and its Arabic twin on 204, `Dreamehome תייצקלפא` on 191,
`Dreamehome App قيبطت ليزنت` on 207, a Wi-Fi label on 189 and 205 — were the entire
residual. The **region's language** decides now, with the majority as fallback, which
is the right authority: a document-wide answer to a question one line cannot settle,
and establishing it is what the probe is for.

Two independent checks hold it there, off comparisons sharing no code:
`verify.TestNoTextIsStoredReversed` fails on one word absent forwards and present
backwards, and `registry.TestHebrewIsFoundTypedForwards` fails if the word for
"manual" is findable backwards.

**One reversal survives, at run granularity, and nothing can see it.** Page 204 stores
the support URL as
`faqs-and- manuals-user/pages/com.dreametech.global://https`. Poppler paints that URL
as **seventeen runs**, splitting at every `:`, `/`, `.` and `-` because the punctuation
is set in a different font, and `joinRunsRightToLeft` reverses the order of a line's
runs — right for the Arabic, wrong for a left-to-right island spread across runs, whose
visual order is already its logical order. Page 188's Hebrew twin is unaffected because
there the same URL is one run.

The word check cannot see it: every one of those tokens is present in `pdftotext`, and
that comparison is set membership, so a reordering that preserves the word set is
invisible to it by construction. `checkOrder` asks that question of blocks and nothing
asks it of words. The report catches the block only sideways, as a `join-hyphen-space`
on `إىل faqs-and- manuals-us`, which is the 73rd of those findings and the reason that
count moved.

Worth knowing what this does *not* break: the language signals are unaffected. The
character-repertoire and script signals count characters, so order is irrelevant to
them, and the printed page tag already strips bidi controls for the reason
`stripFormatting` documents. It is the readable text, and therefore search and
translation later, that is wrong.

**Right-to-left is postponed for the app and built into the reader.** The frontend has
no direction handling at all — no `dir` attribute, no logical properties, every margin
physical — and converting the five existing screens is deliberately not being done.

The reader is the exception, because for a *new* screen the cost is nil: Tailwind's
logical utilities (`ms-`, `me-`, `ps-`, `pe-`, `text-start`, `text-end`) are the same
length to type as the physical ones, and the block model already carries each block's
language, so setting `dir` from it is one attribute. Writing it that way costs nothing
today and saves rewriting the one screen where direction actually matters — this
document's manuals include Hebrew and Arabic sections.

## Two corrections to what was already recorded

Found while measuring, and both concern the column fixture:

- Its tables are on **pages 52-61, not 57-61**. Pages 52-56 carry genuine small
  tables (`Anwendungsfall | Düse/Zubehör`) that the manifest never recorded.
- It prints an **unruled specification table** that nothing had recorded at all, as a
  block within its disposal-and-warranty page, once per language: 62 German, 63
  Polish, 65 Ukrainian.

## What building the first half settled

**Page furniture IS identified now, per language and across pages.** The printed tab and
the folio are found by `internal/doc/furniture.go` and no longer served as content: 111
blocks on the column manual (70 tabs, 41 folios) and 1,105 on the sequential one (553
tabs, 552 folios). On the sequential manual 471 of those tabs were arriving as level-2
headings and 533 of the folios as paragraphs.

**The denominator was the whole problem, and the obvious choice fails.** Counted over the
pages a *household* converted, the German tab is 16 of 59 — 0.27, below any usable cut.
Counted over the pages of *its own language*, 1.00. The rule is per base language.

The threshold has a real gap under it, which is rare in this codebase: measured over all
39 language sections of both manuals, a tab is on **0.81–1.00** of its language's pages,
and the widest share of anything that is *not* furniture is **0.29** — `Плановое
обслуживание` at 0.27, `Sicherheitshinweise` at 0.25. 0.5 sits 1.7x above the ceiling and
1.6x below the lowest tab with nothing in between. A four-page floor is also needed: on a
two-page section one page out of two is a half, which made ~400 buckets furniture.

**A folio is confirmed by a second opinion rather than by a share.** The column manual
prints its folio in the outer margin, so German only carries it on 7 of 26 pages. What
replaces the share is `Page.Folio`, which `pdftotext` read through none of this code.

**Removing the tab makes the heading rule work better.** Level-1 headings on the column
manual *rise* by 29, because an 11pt tab glued onto a heading line was diluting the body
face the rule measures against. Page 14 read `D Trockensaugen` and now reads
`Trockensaugen`; page 57 `D Fehlerbehebung` now `Fehlerbehebung`. Sequential page 24 is
now exactly one heading and 12 list items, matching its render comparison.

**Marked in the model, filtered at the save boundary** — `Block.Furniture` plus
`ContentBlocks()`, and `internal/ingest` stores only content. No migration, no change to
`00006`'s search triggers, no filter in the reader or the index. The verifier deliberately
excludes furniture from its coverage sum so that a rule wrongly claiming a *paragraph*
shows up as a drop rather than being invisible; coverage moved 0.974 → 0.973 and 1.000 →
0.997 against a threshold of 0.75.

**The tab is on 556 of 560 pages of the sequential manual, not 110.** That 110 is quoted
in four shipped files and is wrong in two ways: it undercounts by a factor of five, and it
attributes the repetition to the *column* manual, which has 68 pages.

**What is still not identified is the RUNNING HEAD**, and the measurement says why rather
than leaving it open. Separating it from a genuinely repeated heading needs the occupancy
of its height, not the text at it — 0.77 on the column manual against 0.63 for a real body
line. But that cut removes the sequential manual's section titles, because there the
running head **is** the section title, printed identically where the section starts and on
every page after, with nothing distinguishing the first from the repeats. Twelve points
apart with one document on each side. So the column manual's page 14 still opens with
`Trockensaugen`, which a reader sees and did not ask for.

**One page cannot identify furniture, which is why this pass is where it is.** The printed
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

## What a free verifier found

`internal/verify` and `manualbox verify` check a conversion against **`pdftotext`, a
completely independent second extraction of the same bytes** — the block pipeline reads
`pdftohtml`, so every page already has a free second opinion produced by different code.
Five checks, every threshold measured against both manuals and quoted at its constant.
No model, no tokens, runs in CI.

What it reports today:

| | column manual | sequential manual |
|---|---|---|
| coverage — did we drop content | **0 findings**, median 0.974 | **0 findings**, median 1.000 |
| reading order | **0** | 37 over 26 pages |
| figures clipped | **22 of 46** | **74 of 163** |
| figures with a blank band | 4 | 6 |
| hyphen-space joins | 276 blocks | 73 blocks |
| words absent from the reference | 4 | 160 |
| right-to-left reversed | none, no such script | **none** |

The last two rows moved together when `bidi.go` landed, and in opposite directions for
one reason. Reversed pages fell from 32 to 6 and then to 0 because the text is no
longer reversed; absent words rose from 153 to 160 because the 25 pages that are
Hebrew or Arabic but *not* reversed stopped being named as pages and are now judged
block by block like every other page. What is left on them is Arabic shaping and
combining-mark disagreement, which is neither tool's to fix. See
`verify.minReversibleWords`, which also records why a zero here is a weaker statement
than it looks — the word comparison cannot see page 204's run-reversed URL.

The 73rd hyphen-space join is that URL.

**Coverage is clean on both, which is the reassuring one:** nothing is being silently
dropped. The least-covered page of either manual is 0.801, and that is the
artifact-heavy front matter `usableRuns` deliberately filters.

**Clipping is far more widespread than it looked.** 22 of 46 figures and 74 of 163 are
cut off by their own crop — the clip-path limitation above, which had been recorded as a
tidiness problem and is in fact the largest visible defect in the output. Cross-checked
against a second, independent signal, whether the render's own paint reaches the crop
edge: 22 of 22 agree on the column manual, 73 of 74 on the sequential one.

**37 pages are read in columns rather than rows**, all one class: the routine-maintenance
page of each language section lays its intervals out as an **unruled** grid, which the
table detector cannot see. This is the unruled-table gap above, biting for real rather
than hypothetically.

**Thai is broken in the document, and neither tool is a reference for it.** The check
flagged 142 absent words across pages 473-488. Investigating rather than trusting the
label: `pdftohtml` returns U+FFFD for some Thai vowels — `ข้อมูลด้�นคว�มปลอดภัย` where the
page prints `ข้อมูลด้านความปลอดภัย` — but `pdftotext` breaks *different* characters and
breaks more of them, 34 against 21 on page 480 and 14 against 6 on page 484, and it
mangles `สำาหรับ` into `สำ�หรับ` where `pdftohtml` gets it right. The PDF's Thai font has an
incomplete character mapping and the two tools recover different partial subsets. So
those findings are mostly the two tools disagreeing, not content we lost, and Thai
cannot be repaired by preferring the other tool. It is a property of the document.

**Two corrections to what is written above, from the same measurements.** The blank band
on page 14 does not reproduce — its two photographs render with 0.0 and 2.5 units of
margin. The real ones are page 46 figure 1, 64 units blank at the foot, and page 40
figure 0, 36 at the left. And the sequential manual's conversion yields **163** figures
rather than the 229 counted over the whole document, because 7 figure pages fall outside
every region.

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

**Both halves are met, and both were checked against renders rather than against
counts.** Column manual German: 427 content blocks and 53 figures over 26 of 68 pages, with
page 57's two troubleshooting tables arriving as 25 distinct cells and page 14's two
photographs coming back neutral with the same digest in the German and the Polish
conversion. Sequential manual German: 453 content blocks over pages 23-38, and page 24
compared against its render matches bullet for bullet — one heading and **12 list
items against 12 printed**. Sequential Russian: 445 content blocks and its 65 figures over
pages 517-538, page 533's eight line drawings among them.

The two blemishes on that page 24 comparison are the documented page-furniture
limitation, not new: the printed `DE` badge arrives as a level-2 heading and the folio
`18` as a paragraph. Nothing on one page separates those from content.

## What wiring it to the pipeline settled

Conversion now runs as a job behind an approval, and three things only became
measurable once it did.

**The job pays for the probe a second time, and the cost section above understates
it.** That section prices a conversion as "one `pdftohtml` pass over the whole
document" because `Result` does not carry the runs. True, but incomplete: the
handler does not have a `Result` at all. The probe stored its findings as rows, and
`Convert` needs the object, so the job re-runs `Analyze`. Measured end to end
through the API, on a clean database:

| | analyze | convert | job, start to finish |
|---|---|---|---|
| sequential manual, Russian | 3.78 s | 14.58 s | **18.6 s** |
| column manual, German + Ukrainian | 8.11 s | 17.07 s | **25.4 s** |

So re-reading is 20% of one job and 32% of the other. It is bought deliberately
rather than cached: `Analyze` is a pure function of the bytes, which is what makes
the probe idempotent, and rebuilding a `Result` from `doc_pages` and `doc_regions`
would be a second implementation of the same object, free to drift from the real one
in ways nothing compares. The alternative worth having later is storing the `Result`
whole, not reconstructing it.

**A figure's language is derived on read, not stored.** `doc_figures` has no language
column, which is the contract — a picture belonging to no language belongs to every
language — and that is exactly why a household reading two languages cannot be served
by page. The de+uk conversion of the column manual stores 54 figures; page-scoped
filtering would hand a German reader the Ukrainian column's picture off every shared
page. Applying the same geometric test `Convert` used, against the same stored
regions, gives German **53** and Ukrainian **52**, overlapping in the 51 neutral ones
— including page 14's two photographs, which arrive with identical digests in both.

**The state has to be the transaction's, and reverting it proves so.** Setting the
document to `ready` on its own handle before `SaveConversion`'s transaction leaves a
document claiming to be readable after a save that rolled back — checked by making a
block violate `page >= 1` and watching the row say `ready` with no blocks behind it.
Calling `SetDocumentState` from *inside* the transaction is the deadlock the
`saveFigures` header already measured; the state therefore travels as a parameter and
is written on the transaction's own handle, last.

Both fixtures came back at the numbers above through the real API: 432 German blocks
with page 57's two tables as 25 cells, and 445 Russian blocks with 65 figures over
pages 517-538, page 533's eight among them. Re-approving a `ready` document produced
byte-identical JSON.
