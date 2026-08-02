# Storing a language that occupies part of a page

Contract for the next change, written before it is built. It is the riskiest step
in the ingest work so far: it alters tables that already shipped, and a
self-hosted install means a bad migration is other people's data.

Prerequisites are done and committed: column geometry (`internal/doc/columns.go`)
and per-column language naming (`internal/doc/columnlang.go`). Neither stores
anything. This is what lets them.

## What is broken today, surveyed rather than remembered

| | |
|---|---|
| `doc_pages` PK `(document_id, page_no)` | one row per page, so a page cannot hold two languages — `00002:145` |
| `doc_langs` PK `(document_id, source, code, pdf_start)` | **two German columns on one page collide.** Same page, same code, same source, nothing to tell them apart — `00002:205` |
| `doc_langs.source` CHECK | omits `repertoire`, which exists in Go as a `Source` — `00002:163` |
| `Reconcile` | resolves and groups per page throughout — `reconcile.go:69,122,223` |
| `SaveProbe` | calls `PageLang(p.No)` once per page — `documents.go:212` |
| `Scope.Chars` | sums whole-page character counts — `doc.go:343` |
| Nothing anywhere | can slice a page's text by rectangle |

## The decisions

**A new `doc_regions` table, and `doc_pages` stays.** They record different
things. A page genuinely has one dominant script, one printed folio, one tag
position — those stay per page. What is not per page is language, and that moves
out. Widening `doc_pages` would make every existing column ambiguous about which
part of the page it describes.

**Key on geometry, not on the label.** `(document_id, source, page, x0)`. That is
what distinguishes the German left column from the German right column, and it
keeps the natural-key upsert that makes re-probing idempotent — the property
`00002`'s comment calls load-bearing and `CONTRIBUTING.md` makes a rule. A
surrogate ULID would break it: a second probe would insert a parallel set rather
than converging.

**A whole-page region has no box.** `x0 = 0, x1 = page width`, so a sectioned
manual stores exactly what it stores today and page-only readers keep working.
That is the compatibility stance: absent box means whole page, never null-checks
scattered through callers.

**Characters replace pages as the unit of size.** Pages stop meaning anything when
a page holds three languages — "48 of 560 pages" was always a proxy. Pages stay a
thing to *show*; characters become the thing to count, which needs the text-slicing
function that does not exist yet. That function is part of this deliverable, not a
follow-on: `Scope.Chars` is wrong the moment regions land without it.

**`repertoire` joins the CHECK lists**, in an append-only `00003`. `00002` is
committed and shipped; editing it now would diverge from any database already
created from it.

## What building it settled

Three things the contract above could not decide in advance. Each was decided by
measurement, and two of the measurements contradicted the first attempt.

**A page divides on language, never on geometry.** The contract assumed the columns
were the regions. They are not: the column manual sets two columns of one language on
pages 6–10 and three on 52–56, and the sectioned manual reads as two or more columns
on 406 of its 560 pages, every one of them a side-by-side table. Dividing on geometry
stored four regions for a single-language page, on hundreds of pages of a manual with
no parallel columns at all. So a page divides only where its columns name more than
one language, which also disposes of "a table cell is not a text column" below.

**The per-page answer outranks a column's, where it exists.** Letting a column's
alphabet reading overturn the reconciled page language split 31 pages of the
sectioned manual and contradicted its printed tab on 46 regions — German read as
Finnish, Spanish, Portuguese, every case a short table cell. That tab is right on all
553 of its content pages. On the column manual the per-page signals name *nothing* on
any of the eight verified pages, which is why the columns are trusted there and not
here. A disagreeing column now records a conflict and changes no answer.

An earlier attempt made this conditional on how much evidence the alphabet had. That
is unsupported: over 685 labelled columns the repertoire signal is 93% accurate and
its errors occur at every amount of evidence, one of them with 118 distinctive
characters. No threshold separates them, so none was added.

**A page-level answer must name a real language to outrank anything.** BCP-47
constrains a subtag's shape, not its meaning, so `FAX` parses as the language `fax`,
`TEL` as `te`, `NDE` as `nd`. The column manual prints FAX on its service-address
page; the index parser reads that page as a contents table and offers FAX as an
entry; reconciliation then labelled two pages `fax`, overriding columns that read
correctly as German and Polish. `doc.KnownLanguage` now gates what may outrank other
evidence, and deliberately does not gate what may be stored — a manual printing an
unrecognised code is information worth keeping.

**Characters are counted with one tool, not two.** A boxed region can only be
measured from positioned runs, so whole-page regions are measured that way too rather
than reusing the existing `pdftotext` count. The two disagree by 3.3% and 2.5% on the
fixtures' totals, 1–2% on a median page, and by up to 51% on a page whose text layer
parks runs outside the page box. One measurement throughout beats two that nearly
agree. Measured payoff: a German-reading household is charged 44,376 characters of the
column manual rather than the 233,849 its pages hold in all five languages — 19%,
where before a single language cost the same as all of them.

## What this deliberately does not solve

Recorded so the next person does not think they are unsolved by accident:

**Regions do not compose across pages.** Nothing says the left column of page 7 is
the same column as page 9. Reading order and stable block IDs will need that, and
it is a separate question about identity rather than storage. Do not invent a
column-identity field here on the guess that it will be right.

**Language-neutral content has no home.** A diagram, a parts table or a spec block
shared by all five languages must currently be assigned to one, duplicated, or
left unlabelled. None of those is correct. Left open because the honest fix is
probably a region kind rather than a language, and that wants a second document to
design against.

**A table cell is not a text column.** Geometry cannot tell them apart, five pages
of the measured manual are troubleshooting tables, and it has already caused the
document's one language error. Above this layer — and now handled there, by dividing
on language rather than on cells, so a same-language table is one region. What is
still unsolved is a table whose cells are in *different* languages: it would divide,
and be wrong to. Neither manual does it.

**A page that both prints a whole-page tab and sets parallel columns of different
languages** would be called one language, with a conflict recorded. Rule 1 gives the
per-page tab precedence and there is no evidence here for doing otherwise: one manual
prints per-page tabs and sets one language per page, the other prints per-column tabs
and names no page at all. The mechanism for the hybrid would be invented rather than
designed. If a third manual is that document, this is the stop condition.

**Interleaved paragraphs down one column** would need one region per paragraph,
at which point a region stops being a layout partition and becomes a paragraph
annotation. Not seen in either manual. If a third manual does it, this design is
the wrong shape rather than an incomplete one — that is the stop condition.

## Acceptance

Not "the migration applies". The pipeline must store and read back the Thomas
manual's five languages across its parallel columns, with the eight
human-verified pages of `testdata/fixtures/thomas-drybox-amfibia.json` matching
column for column — and the Dreame manual's 34 sequential sections must be
unchanged, byte for byte, in what it reports. A change that improves the second
manual by altering the first has broken something.

Both fixtures already carry the ground truth to check this against, and the
column fixture records per-page provenance so nothing is tested against its own
output.
