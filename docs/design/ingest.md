# Ingesting a manual without doing anything stupid

A real appliance manual: the Dreame L40 Ultra, **560 pages, 15 MB, 34 languages in
one PDF**. Sixteen pages of it are English. The other 544 are languages this
household does not read.

This is not an edge case. Multi-language booklets are how appliance manuals ship,
so it is the *normal* input and the pipeline is designed around it rather than
guarded against it.

Every number below was measured on that file.

## The mistake to avoid

Feed the whole thing to a model:

| | Tokens | Sonnet 5 | Opus 5 |
|---|---|---|---|
| All 560 pages, as text | ~395,000 | $1.18 | $1.97 |
| English only, 16 pages | ~8,800 | $0.027 | $0.044 |

**98% of that spend buys nothing.** And if the PDF had no text layer, conversion
would go page-image → vision model: 560 × ~4,784 image tokens = **2.7M tokens,
2.7× over a 1M context window**. Not merely expensive — impossible in one pass,
at roughly $13.40 per attempt. Sixteen pages instead: $0.38.

So the pipeline's first job is not to convert anything. It is to find out what it
is holding, for free.

## The funnel

Four stages before a model is touched. Each is cheaper than the next and each may
refuse to continue.

### Stage 0 — metadata. Free, instant.

`pdfinfo` answers: page count, byte size, encrypted, tagged. One syscall.

Config sets the guard rails — `ingest.max_pages_auto` (default 64). Above that, no
automatic processing: the document is stored and the user is asked. A 560-page
upload should never silently become a bill.

### Stage 1 — text probe. Free, ~2 seconds.

`pdftotext` over all 560 pages took **1.7 s** and produced 1.3 MB of text.

The output answers the question that determines everything downstream: **is there
a text layer?** Median extracted characters per page here was 2,241; a scan yields
~0. That single number selects between a free extraction path and one that costs a
vision call per page — a difference of two orders of magnitude.

### Stage 2 — the language map. Free.

Two independent methods, because **each one is wrong in a way the other catches.**

**The printed index.** Pages 2–3 carry a machine-readable contents table. A regex
recovered all 34 sections — ISO code, localised title, start page:

```
EN  User Manual              01     KK  Пайдаланушы нұсқаулығы  259
DE  Benutzerhandbuch         17     UA  Посібник користувача    291
FR  Manuel d'utilisation     33     ZH-HK 用戶手冊                499
```

**Per-page detection.** Unicode script ranges settle the non-Latin scripts almost
for free (Cyrillic, Greek, Hebrew, Arabic, Thai, kana vs Han); function-word
scoring handles the Latin ones. All 560 pages, no network, no model.

Neither is sufficient alone, and this was measured, not assumed:

| The index gets wrong | The detector gets wrong |
|---|---|
| **`CZ p.207` is a typo.** Page 207 is Arabic; Czech actually starts at printed 305. | **Indonesian never detected.** Its function words are identical to Malay, so `ID` pages classify as `MS`. |
| **The printed→PDF offset drifts**: +6 at the front, +8 later, because some sections run 17 pages rather than 16. A single global offset lands in the wrong language. | **Danish/Norwegian and Slovak/Czech flip-flop** mid-section for the same reason. |

Reconciled, 32 of 34 sections agree and each disagreement is caught:

> **The index supplies the labels. Detection supplies the boundaries.**

Where they conflict, the conflict is recorded and surfaced — never silently
resolved in favour of one. `doc_langs` stores every run with its confidence and
its provenance.

### Stage 3 — scope, behind a gate.

Intersect the languages found with the household's configured languages. For
`de, uk, en`: **48 of 560 pages, 9.6%**.

Then ask, before spending anything:

> This manual contains 34 languages across 560 pages. Yours are 3 of them — 48
> pages, ≈38k tokens. On your Claude subscription that is about 4% of a window.
> Import them?

The figure shown depends on the provider's billing mode (see
[providers.md](providers.md)) — a dollar amount for a metered API key, a share of
the plan's window for a subscription, nothing at all for a local model. The
estimate comes from `count_tokens`, not a character heuristic, so the number shown
is the number spent.

### Stage 4 — the model, on the slice only.

Convert, translate, and extract across 48 pages instead of 560.

## Why this is safe to be aggressive about

**The original is never touched.** It is stored byte-identical and
content-addressed. All 34 language runs are recorded, so *"this manual also
contains FR, IT, ES…"* is a button, not a re-import. Processing 2.9% of a document
by default is only defensible because the other 97.1% is still right there.

**Even within your own language, don't send all of it.** Sixteen English pages are
mostly setup and safety copy; the descaling and filter intervals live on perhaps
two. Once the document is converted to canonical HTML with stable block IDs,
extraction targets the maintenance and care sections — roughly another 85% off, and
it is what makes citations point at a paragraph rather than a document.

**Pick the boundary-finding technique by probe cost.** With a text layer, scanning
all 560 pages is free, so scan all of them. On a scan, every probe is an OCR or
vision call, and the same answer costs 560 of them — so treat the index's claimed
starts as hypotheses and verify ~2 pages per section (68 probes), or binary-search
each boundary in ~9 steps. Same goal, different algorithm, chosen by what a probe
costs.

## Test fixture

`testdata/fixtures/l40-ultra.json` records this document's URL, checksum, page
count, and the full expected language map.

**The PDF itself is deliberately not committed.** It is 15 MB, and it is someone
else's copyrighted manual — committing it would break the project's own rule
against redistributing manuals. Tests that need it download it on demand and skip
when it is absent, so the suite stays hermetic by default.
