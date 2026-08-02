# manualbox — working notes for Claude

Self-hosted household manual and maintenance manager. Go 1.25 + React 19, single
binary with the SPA embedded, SQLite, no external services required. Public repo,
MIT. Module `github.com/gordon2/manualbox`.

## Read before changing an area

The design docs carry the reasoning **and the measurement** behind each decision.
Read the relevant one first; do not re-derive it.

| | |
|---|---|
| [CONTRIBUTING.md](CONTRIBUTING.md) | Conventions that have already caused real bugs here |
| [docs/design/ingest.md](docs/design/ingest.md) | The funnel: how a 560-page, 34-language manual is reduced to the pages you actually read, before any model is called |
| [docs/design/layouts.md](docs/design/layouts.md) | How a manual is arranged — sequential sections or parallel columns — and the one seam that varies |
| [docs/design/regions.md](docs/design/regions.md) | Storing a language that is part of a page: the contract, what building it settled, and what it still does not solve |
| [docs/design/conversion.md](docs/design/conversion.md) | Turning a manual into readable blocks — reading order, headings, tables from ruled lines, and the five pages nothing can see |
| [docs/design/search.md](docs/design/search.md) | Finding the sentence you need: why the tokeniser is `trigram`, what that costs and what it cannot do, and why the index is kept by triggers |
| [docs/design/language-detection.md](docs/design/language-detection.md) | The five language signals, what each costs and how accurate each is, and why the detector choice is still open |
| [docs/design/providers.md](docs/design/providers.md) | Why a subscription CLI or local model comes before a metered key, and why a CLI adapter must batch a whole document |
| [docs/design/privacy.md](docs/design/privacy.md) | What manualbox holds, ranked by how it actually leaks |
| [docs/design/keys.md](docs/design/keys.md) | Encryption keys: choosing, storing, recovering |

## Commands

```sh
make web-install && make build   # build the binary with the SPA embedded
make check                       # test + lint + typecheck — everything CI runs
make sqlc                        # regenerate DB code after editing queries or migrations
./bin/manualbox doctor           # what is configured, which optional tools are present
./bin/manualbox serve            # http://localhost:7745
```

`poppler` and `tesseract` are optional at runtime; features needing them report
why they are unavailable instead of failing. Install them to work on the document
pipeline (`brew install poppler tesseract`).

Fixture-backed tests need `MANUALBOX_TEST_FIXTURES=1`; they fetch a real 15 MB
manual on demand and skip without it, so the default suite is hermetic and offline.

## How this project expects to be worked on

**Measure, don't estimate.** Every number in the design docs came from running
something, and several contradicted the first guess. Before asserting a cost, a
size, or a behaviour, run something and quote the result.

**Verify where the user will see it.** A clean clone for anything touching the
build or `go:embed`; a real browser for UI; server or container logs for config.
Read structured results, not scrolled log tails.

**After fixing a bug, revert the fix and confirm the test fails.** This has caught
two worthless tests and one bug that was reported as covered but had no test.

**Say plainly what was deliberately not done, and why.** Leaving it implicit reads
as a claim that it was done.

**Prefer delegating bulk implementation** to subagents once a contract is stable,
and keep the main thread for framing, design decisions, integration and
verification.

## Architecture

Packages under `internal/`: `config` `id` `db` `store` `jobs` `auth` `api`
`frontend` `fixture` `keyring` `extern` `logging` `doc` `registry` `ingest`
`testpdf`, plus `web/` (React 19 + Vite + Tailwind v4, embedded via `go:embed`).

- `store` — content-addressed blob store. Originals are immutable, mode 0400, and
  the filename **is** the SHA-256.
- `jobs` — SQLite-backed queue with leases, so a killed worker's job is reclaimed.
- `doc` — reads a document and reports facts about it. Knows nothing about
  databases and calls nothing remote.
- `registry` — the inventory: locations, devices, documents.
- `ingest` — runs the pipeline as background work and answers the pre-flight gate.
- `testpdf` — generates small valid PDFs in memory for tests, because no PDF may
  be committed.

## Conventions that bite

These are the ones that have already caused bugs. CONTRIBUTING.md has the full
list and the story behind each.

- **`db.Read()` for queries, `db.Write()` for statements that modify.** The writer
  is capped at one connection; that is what prevents intermittent "database is
  locked".
- **Timestamps are integer milliseconds**, converted only through `internal/db`.
- **Wrap SQLite aggregates in `CAST(... AS INTEGER)`** or sqlc emits `interface{}`
  and every caller pays for a type assertion.
- **Job handlers must be idempotent.** A worker can die after doing the work but
  before recording success. Derived tables use composite natural keys and upserts
  so a second run converges instead of duplicating.
- **Count runes, not bytes**, wherever text size matters. Half of a real manual is
  Cyrillic, Greek, Hebrew, Arabic or CJK, where bytes run a third higher.
- **Strip Unicode format characters before matching text.** A right-to-left page
  wraps Latin furniture in bidi controls, so a tab reading `HE` is really
  `RLE LRE H E PDF PDF`. Missing this silently loses whole sections.
- **`gocritic` rejects ranging over large structs by value.** Use
  `for i := range pages` and take a pointer.
- **Anchor `.gitignore` patterns** — an unanchored `manualbox` once matched
  `cmd/manualbox/`.
- **Look at UI changes in a browser.** Three real bugs shipped past a green
  typecheck.

## Never commit

- **Real documents or photos.** Manuals are copyrighted and manualbox's own
  principle is not to redistribute them. Fixtures are manifests describing where
  to fetch a document; tests that need a PDF generate one with `internal/testpdf`.
  CI rejects any committed `.pdf`, `.jpg`, `.jpeg` or `.heic`.
- **Anyone's personal data.** Tests use `example.com` (RFC 2606) and documentation
  IP ranges (RFC 5737) — never a real address, which is how a shared project
  starts reading as one person's private inventory.
- **Absolute paths from a developer's machine** — they carry an OS username.
- Databases, `data/`, or `.env`.

CI enforces these with a `hygiene` job. It is a grep, not a guarantee.

## Current state

M0 and most of M1's pipeline are done: registry, upload, the free probe that reports
what a document contains and stops at the gate, conversion behind that gate, and
full-text search over what it produced. The reader and export are still to come —
see the roadmap in [README.md](README.md).

**Regions are computed and stored.** A page can hold several languages, so the unit
of the language map is a region rather than a page: `internal/doc/runs.go` reads
where text sits with `pdftohtml`, `regions.go` divides a page on language, and
`doc_regions` persists it. Verified on both real manuals — the parallel-columns one
stores all five of its languages across its columns, the sequential one stores
exactly one whole-page region per page and its 34-section map is unchanged.

**The gate answers from regions, and characters lead.** `ingest.Gate` reads
`registry.Regions` where a document has them and falls back to the per-page runs
where it does not, so the parallel-columns manual now reports its five languages
with 47,641 characters of German — 20% of the document's text, on 26 of 68 pages it
shares with four other languages — instead of "68 pages, but no language could be
identified". The sequential manual reports exactly what it did, plus the new fields.
`Gate.UnlabelledPages` and `CostEstimate.Chars` were declared and never assigned;
both are now derived from stored rows.

**The gate screen leads with characters too.** `web/src/screens/DeviceDetail.tsx`
reports each language as its character count and its share of the document's text,
with the pages underneath as a locator, and words those pages by `sharesPages` —
"appears on 26 pages, sharing each with other languages" against "pages 23–38, all
its own". The stat row and the import button count characters rather than pages, for
the same reason. A language under 1% of the text keeps a decimal and loses its
emphasis rather than being filtered, which is what the 289 characters of Finnish in
the columns manual need.

**The gate has a second door, and conversion runs behind it.**
`POST /documents/{id}/approve` is `decline`'s opposite: it moves the document to
`converting` and queues `doc.convert`, which re-runs `doc.Analyze`, calls
`doc.Convert` for the household's configured languages, and stores the result with
`registry.SaveConversion` — the target state travelling as a parameter so `ready`
lands in the same transaction as the blocks that justify it. There is no language
argument anywhere on that path: the gate showed a specific scope, and approving must
mean that scope. `GET /documents/{id}/conversion?lang=de` serves the blocks and
figures, `GET /documents/{id}/figures/{sha256}` the PNG bytes; `/content` still serves
the original, unchanged. Measured through the API: the column manual's German is 431
content blocks and 53 figures, the sequential manual's Russian 431 content blocks and
65 figures over pages 517-538.

**A section title is served once, where the section starts.** The furniture pass has a
third clause: the page's first printed line is a running head when the page before it
in the same language section printed the identical line, so the first page of each
consecutive run keeps its title and every page after it loses one. That dissolves a
blocker the design doc had recorded as measured-and-refused — separating a running head
from a repeated heading by the occupancy of its height is 0.77 against 0.63 with one
document on each side — because both of its options were wrong: remove them all and the
sequential manual loses its titles, keep them all and every page reprints one. 61 claims
on the column manual over 20 titles, 184 on the sequential over 77, and **0 of the 245
has no surviving content copy**, which is the invariant that replaces a list of 97
strings in 39 languages. Two recorded claims turned out to be wrong and are corrected in
[conversion.md](docs/design/conversion.md): the column manual **does** have a running
head (its grey banner's chapter name), and sequential page 24's pinned "one heading and
12 list items" was itself the defect — page 23 starts that section and page 24 only
reprints its title.

**A contents page reads as a list of entries.** The columns manual's `Оглавление` was
one run-together paragraph of dot leaders; each printed line is now its own block,
drawn as a title, a leader rule and the page the paper prints. The signal is a dot
leader of 8+ plus a page reference, and it has a rare thing under it — a real gap:
over both manuals every dot run is 3, 3, 3, 4 then 34 to 91. It is **not** a sixth
`BlockKind`, because that reaches a CHECK on `doc_blocks` and widening it costs a
rebuild of the table the FTS index is external-content over; the note carries the fact
instead.

**And the page number is a link.** The printed folio maps onto a PDF page by one
constant per document, derived from `doc_pages.printed_folio` rather than stored — no
migration, one grouped scan per conversion response. It is the **mode** of
`page_no - printed_folio`, the same estimator and the same reason as `columnPitch`'s
line gaps: the sequential manual is **6 on 552 of its 558** folio-bearing pages and the
columns manual **0 on 65 of 67**, with the runner-up covering exactly one page in each,
and one back cover misread as folio 2735 would put a mean 40 pages out. The mode must
hold **0.6** of those pages or no offset is served at all — had the sequential manual's
34 sections each restarted at 1, the best offset would have held 22 of 553 pages, 4.0%.
`Conversion.folioOffset` is **absent**, never 0, when there is no answer: the columns
manual's real offset IS 0 and the two must not be confusable. An entry stays plain text
when no offset was served, when the line has no number, or when the target is not a
page this language's conversion holds; a range links to its first page. Verified in
Chrome: 17 of 17 German entries link, `Fehlerbehebung 57` scrolls to page 57, and
withholding the offset returns all 17 to plain text. The brief's expectation that a
German entry could point at a Russian page is **refuted** — each language's contents
page prints its own folios.

**A page can have two columns and no second column, and reading order now says so.**
On 8 of the sequential manual's 16 two-column Russian pages a block crossed the gutter:
page 530 read `"Мешок для сбора пыли Основная щетка"`, two section banners spliced. The
cause is not the projection but the two gates a **`Column`** has to pass, which are
right for a published fact language attribution reads and wrong for reading order —
page 530's right column holds **6 runs** against `minColumnRuns = 8`, and at 17 usable
runs no x of that page is crossed by more than the **4** `maxGutterCrossings` allows, so
the whole right-hand half reads as one gutter. `readingStrips` is the same projection
with both bounds at their limit, used only where `DetectColumns` has already declined;
`DetectColumns` itself does not move, so no region and no language attribution does.

A **threshold on the gap was measured first and refused**: over the pages the detector
does split, within-column gaps reach 22.1 times the line's font size and across-gutter
gaps start at 0.0, and there is no gap to put a number in. Two things came out of this
sideways — a right-to-left region's columns were being read left to right on 16 Hebrew
and Arabic pages, and a table whose drawn box overhangs its own words fell to the banner
band and was read before the page's title. Measured with every language converted:
`reading-order` findings **38 → 24** on the sequential manual and its glued-word count
6 → 5, blocks 16,097 → 16,132 over 28 pages and 2,345 → 2,407 over 7, **no word gained
or lost on any page**. The column manual's one new finding is the check's shape, not a
defect, and is explained where it is pinned. See
[conversion.md](docs/design/conversion.md).

**The blocks are indexed, and `GET /api/v1/search?q=` answers which manual says X.**
FTS5 over `doc_blocks` with `content='doc_blocks'`, kept correct by three triggers
because the third path that changes that table — `documents ON DELETE CASCADE` —
runs no Go at all. A hit names the document, the device, the page and the language,
and carries the block's natural key so it can cite the paragraph.

The tokeniser was the one open question and it is measured, not argued:
**`unicode61` finds nothing in Japanese and nothing in Thai**, because a whole CJK or
Thai run is one token, so the index is `trigram remove_diacritics 1` — 880 KB against
270 KB over the 3,122-block corpus of both manuals. Its named limitation is that no
query under three characters is in the index at all, which is a real hole in Chinese
and Japanese, so those are answered by an `instr` scan instead and the response's
`mode` says which path ran. Verified through the API on both real manuals: German,
Russian, Japanese and Thai all find a real word, and **so does Hebrew, typed
forwards** — `מדריך` finds 5 blocks forwards and 0 backwards, the exact inverse of
what it did, because `internal/doc/bidi.go` stores right-to-left text in logical
order and the **region's language** decides direction. A fixture test pins both
numbers; the claim had lived in prose with nothing under it.
The whole measurement is [docs/design/search.md](docs/design/search.md).

**Right-to-left text is no longer stored reversed, and that is asserted rather than
believed.** `internal/verify`'s `right-to-left-reversed` check reports **0 pages**
where it reported 32, and `TestNoTextIsStoredReversed` fails on a single word that is
absent from `pdftotext` and present in it backwards. Getting there needed the check
sharpened first: it fired on any right-to-left page with any absent word, which was
the same question as "is this page Hebrew" only while every Hebrew page was broken.
It now needs evidence of a reversal — see `verify.minReversibleWords`, which records
why that is a count and not a share.

**A zero there means no word is spelled backwards. It does not mean the words are in
the right order.** Four defects were found in `bidi.go`, all by measurement, and the
check whose name describes them caught **exactly the one that was a reversal** — the
direction rule that left six lines unrepaired, which it named to the page and the word
once it was sharpened. It was structurally blind to the other three, which were
reorderings: the word check compares set membership per page, so word order is outside it
by construction. Page 204's URL arrived with its seventeen runs reversed; page 211's
Arabic list marker `1.` arrived as `. 1`, merging six printed list items into one
paragraph on each of six pages; page 204's laser standard arrived as `EN1:2014/ 60825-`.
Two surfaced sideways through the joins check reacting to a side effect, and **one was not
reported at all** — caught only because a pinned block count moved by 43. All four are
fixed. `internal/verify` asks the order question of blocks and nothing asks it of words;
the design of the check that would, and the reason it is not built, is in
[docs/design/conversion.md](docs/design/conversion.md).

**Pin the counts you cannot yet explain.** That block-count pin caught the regression
nothing else saw, and then confirmed the repair page for page. Its history also shows why
a total needs its sequence beside it: 16,055 appears twice and means opposite things.

Deliberately not built yet, each for a stated reason:

- **The printed-index parser cannot read a contents page laid out in columns.** It
  returns junk for the Thomas manual, which costs 26 columns of printed-tag
  attribution and once labelled two pages `fax`. See language-detection.md; a test
  pins the current reading so the gap stays visible.
- AI provider adapters (the `Kind` values are accepted and fail at first use with a
  clear message), the statistical language detector (see language-detection.md),
  serial numbers and purchase prices in the schema (they need the keyring first),
  and login throttling (`TODO(M1)` in `internal/auth/auth.go`).

**One trap worth knowing before you touch `internal/db/queries/`:** those files must
stay pure ASCII. sqlc v1.31.1 corrupts generated statements when a query file
contains a non-ASCII character, sometimes silently — valid Go, invalid SQL, failing
at PREPARE time in a background job. Two tests guard it; the header of
`queries/docregions.sql` has the measurement.
