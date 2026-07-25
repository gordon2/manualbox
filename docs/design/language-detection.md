# Working out what language a page is in

A multi-language manual has to be split into language runs before anything else
can happen: it decides what gets converted, what gets translated, and what the
user is asked to pay for. See [ingest.md](ingest.md) for where this sits in the
funnel.

There are four signals. None of them is authoritative on its own, and the whole
design is about combining them and recording which one spoke.

> **Every number below was measured on one document** — the Dreame L40 Ultra,
> 560 pages, 34 languages. That is one manual, not a corpus. Treat the numbers as
> real but not general; the open question at the end is how to fix that.

## The four signals, cheapest first

| | Signal | Cost | Gives | Fails when |
|---|---|---|---|---|
| 1 | **Printed page tag** | free | label **and** boundary, per page | the manual doesn't print one |
| 2 | **Printed index** | free | labels, section titles, claimed starts | claims are wrong or typo'd |
| 3 | **Unicode script** | free | narrows the candidate set | 25 languages share Latin |
| 4 | **Statistical detection** | a dependency | a label per page of text | sibling languages, unsupported languages |

### 1. The printed page tag

Many manuals print a small tab in a page corner containing that page's own
language code. On the L40 it is white text in the top-left, and — usefully — it
is the **first non-blank line of plain `pdftotext` output**, so reading it costs
nothing beyond the text extraction that already happens.

Measured: 553 of 553 content pages carried a tag, and all 553 agreed with the
corrected section map. It labelled the two sections statistical detection cannot
(see below). It is the single best signal when present.

**It is not present in every manual**, so it is a high-confidence input rather
than the answer. Two guards are required, both of which came from real failures
on this document:

- **Contents pages produce false positives.** Pages 2–4 list language codes in the
  same corner, yielding spurious single-page runs for `EN`, `MS` and `RO`.
  Requiring a run of **≥2 consecutive pages** removes all three and leaves exactly
  the 34 real sections.
- **A two-letter uppercase token is not necessarily a language code.** `ON`, `OK`,
  `NO` and `TV` all match `[A-Z]{2}`. Cross-check the tag against the page's
  dominant Unicode script before trusting it.

### 2. The printed index

Recovers labels and localised titles for every section, which detection cannot do
at all. Its *claimed page numbers* are unreliable: on the L40, 10 of 34 sections
claim a printed page 1–2 off from the folio actually printed, because two sections
run 17 pages rather than 16. So a claimed start is a hypothesis, never a boundary.

### 3. Unicode script

Free, and settles more than it looks. On the L40 it resolved 151 of 554 pages
(27%) and uniquely identified six languages — Greek, Hebrew, Arabic, Thai,
Chinese and Japanese (the last two separated by the presence of kana). Cyrillic
narrowed to three candidates.

It cannot help with the remaining 403 pages, which span 25 Latin-script
languages. That residue is what signal 4 exists for.

### 4. Statistical detection

`lingua-go` v1.4.0 was the candidate. Measured across all 554 labelled pages:

| Configuration | Peak RSS | Accuracy | Speed |
|---|---|---|---|
| lazy, 75 languages, **low** accuracy | 130 MB | **93.7%** | 7.3 ms/page |
| lazy, 75 languages, high accuracy | 129 MB | **93.7%** | 8.0 ms/page |
| **preloaded**, 75 languages, high accuracy | **2154 MB** | — | — |
| preloaded, 3 languages, low accuracy | 13 MB | — | — |

Four things follow, and they are the reason this page exists:

**Never call `WithPreloadedLanguageModels()`.** It is a 2 GB resident-set footgun
on a machine that may be a NAS, and it buys nothing — see the next point.

**High-accuracy mode is not worth it here.** Identical 93.7%. Manual pages average
~1700 characters, and lingua's advantage is on short strings. Use low-accuracy
mode: same result, less memory, faster.

**The binary cost is +118 MB and cannot be avoided.** Linking `lingua-go` takes
the manualbox binary from 11.7 MB to 129.5 MB, because the language models are
`go:embed`ded as a whole directory. Referencing only three languages does *not*
prune them — a build that names German, English and Ukrainian is still 129.5 MB.
128 MB of the binary is `runtime.rodata`. This is the real price, and it is paid
in the Docker image too.

**Accuracy tops out around 94%, and the residue is systematic, not random:**

- **Uzbek: 0 of 16 pages.** `lingua-go` does not support Uzbek at all. No
  configuration fixes this; the language is simply absent. Detected as
  Azerbaijani on 14 pages.
- **Serbian: 0 of 16 pages.** This manual's Serbian is Latin script, which is
  near-identical to Croatian and Bosnian. Detected as `hr` or `bs` throughout.
- Japanese 20/22, Czech 15/16 — isolated pages, not systematic.

Also worth knowing: `lingua-go` has no `no` macrolanguage, only Bokmål and
Nynorsk, so a code map must translate `no → nb`. It covers 32 of this document's
34 languages.

## Why detection is still needed

The page tag worked perfectly on the one manual measured, which is a weak reason
to drop signal 4. Manuals vary enormously in how they are produced, and a signal
that depends on a publisher's layout convention will be absent or different often
enough that it cannot be the only mechanism. The tag is a cheap, high-confidence
*shortcut* — when it is there, take it; when it is not, something has to still
work.

## How the signals combine

The rule from [ingest.md](ingest.md) generalises once there are four sources
rather than two:

> Prefer the cheapest signal that is present. Corroborate it with the next.
> Record every source, its confidence, and its provenance. Where sources
> conflict, surface the conflict — never silently resolve it.

`doc_langs` stores one row per run per source, so *"the tag says DA, the index
says FI"* is a reportable state rather than a coin toss. That is also what makes a
later, better detector a drop-in addition rather than a rewrite.

## Open question

**Every number here comes from one document.** The L40 happens to have a page tag,
a machine-readable index, and a clean text layer. A real library will contain
manuals with none of those.

So the detector decision — whether `lingua-go`'s +118 MB is worth paying, or
whether a lighter library such as `whatlanggo` (~100 KB, script plus trigram) gets
close enough — is **deliberately deferred until there is a corpus to measure
against**, rather than settled on a sample of one.

What to measure when that corpus exists:

- How many manuals print a per-page language tag at all, and in which corner.
- Accuracy of each signal per manual, not averaged across them.
- Whether the sibling-language failures (SR/HR/BS, ID/MS, DA/NB, SK/CS) are
  common enough in practice to need the printed index as a tiebreak.
- `whatlanggo` on the same pages, against the same ground truth, before accepting
  a 10× binary.

Until then the pipeline is built on signals 1–3, which need no dependency, and
signal 4 is an interface with no implementation behind it.
