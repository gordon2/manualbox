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

M0 and the first slice of M1 are done: registry, upload, and the free probe that
reports what a document contains and then stops at the gate. Conversion,
full-text search, export and the reader are still to come — see the roadmap in
[README.md](README.md).

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
