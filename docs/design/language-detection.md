# Working out what language a page is in

A multi-language manual has to be split into language runs before anything else
can happen: it decides what gets converted, what gets translated, and what the
user is asked to pay for. See [ingest.md](ingest.md) for where this sits in the
funnel.

There are five signals. None of them is authoritative on its own, and the whole
design is about combining them and recording which one spoke.

> **Every number below was measured on a real document, and there are only two of
> them** — the Dreame L40 Ultra (560 pages, 34 languages in sequential sections)
> and the Thomas DryBox Amfibia (68 pages, 5 languages in parallel columns).
> Signals 1–4 were measured on the first, signal 5 on the second. Two manuals are
> not a corpus: treat the numbers as real but not general, and see the open
> question at the end.

## The five signals

| | Signal | Cost | Gives | Fails when |
|---|---|---|---|---|
| 1 | **Printed page tag** | free | label **and** boundary, per page | the manual doesn't print one |
| 2 | **Printed index** | free | labels, section titles, claimed starts | claims are wrong or typo'd |
| 3 | **Unicode script** | free | narrows the candidate set | 25 languages share Latin |
| 4 | **Statistical detection** | a dependency | a label per page of text | sibling languages, unsupported languages |
| 5 | **Character repertoire** | free | a language, from the alphabet used | the alphabet is shared, or plain ASCII |

Numbered in the order they were built, not by price: signal 5 costs nothing and
belongs with 1–3.

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

**The parser cannot read the Thomas manual's contents page**, and this costs more
than it appears to. `IndexRuns` yields the vocabulary `[FAX GA NDE UA VIA Z]` for
that document — only `UA` is a language, the rest scraped off a page of service
addresses. Two consequences, both measured:

- A single-letter printed tab is believed only where the index lists that code, and
  `D` is not in that vocabulary, so every German column falls back to its alphabet.
  Tag-named columns drop from 79 to 53 of 169. The total named barely changes, so
  nothing failed loudly; only the attribution moved.
- `FAX` parses as a language tag, became a reconciled page language, and labelled two
  pages `fax` over columns that read correctly as German and Polish. Guarded in
  regions.md by requiring a page-level answer to name a recognised language.

The 79 figure is what the commit introducing per-column naming recorded, measured with
a hand-supplied code list rather than through the assembled pipeline. Fixing the parser
for a contents page laid out in parallel columns is separate, unbuilt work; the gap is
pinned by a test so it stays visible.

### 3. Unicode script

Free, and settles more than it looks. On the L40 it resolved 151 of 554 pages
(27%) and uniquely identified six languages — Greek, Hebrew, Arabic, Thai,
Chinese and Japanese (the last two separated by the presence of kana). Cyrillic
narrowed to the three that document contains, out of the seven the script table
lists.

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

### 5. Character repertoire

Languages that share a script do not share an *alphabet*. Signal 3 narrows a
Cyrillic page to seven candidates and stops — that is everything a script table
knows — but the letters only some of those seven can write are already sitting in
the text stage 1 extracted, and counting them costs nothing.

The case that forced this comes from the *second* fixture, not the L40: 19 pages
of the Thomas manual carry three Cyrillic languages side by side, one per column.
The L40 cannot show this — its languages are sequential, one per page. See
[layouts.md](layouts.md). Counting each language's distinctive letters per column:

| column | Ukrainian marks | Russian marks | Kazakh marks | verdict |
|---|---|---|---|---|
| left | 0 | 40 | 0 | Russian |
| middle | 83 | 0 | 0 | Ukrainian |
| right | 78 | 111 | 143 | Kazakh |

Consistent across 18 of the 19 such pages. The exception is a page of contact
addresses, which is not content.

**The right column is why a maximum over those counts is the wrong reading.**
Kazakh's alphabet *contains* the і it shares with Ukrainian and the ы it shares
with Russian, so overlapping counts are the normal case rather than a conflict.
Two questions decide it instead:

- **Can this language write everything on the page?** Russian cannot account for
  67% of the right column and Ukrainian cannot account for 76%, so both are out —
  ruled out by what they *cannot* write, not out-voted.
- **Does the page exercise this language?** On the *left* column Kazakh can
  account for everything, because Russian's alphabet is a subset of Kazakh's. What
  settles it is that none of Kazakh's own nine letters appear.

Both are needed. Either one alone gets the left column wrong.

**Call this per column, not per page.** A page holding three languages is not text
written by one, so the honest answer for the whole page is nothing — and that is
what it gives: three columns of ordinary Cyrillic manual prose, concatenated,
decline correctly, as does any pairing of them.

The reason to state it as a rule anyway is that the margin is narrower than the
clean result suggests. On the measured page Kazakh already accounts for 422 of 455
marks — 7.25% foreign against a 5% threshold — because its alphabet contains most
of what Russian and Ukrainian can write. The guard holds because real Ukrainian
prose uses ї and є often enough to contradict it. Constructed text where one
language's own letters are unusually sparse crosses the line and is named
confidently, which is how this paragraph came to be written: the first version of
this warning claimed a realistic page fails, on the strength of a sample that
repeated one thin sentence. It does not. The margin is a property of the text, not
of a constant, so no threshold fixes it — calling it per column removes the
question instead.

**Cost, measured.** Linking it adds **1,536 bytes** to the binary (18,475,698 →
18,477,234) and it needs no dependency, no model and no network. A 1,932-rune
page takes **100 µs** on an M2 Pro, of which 53 µs is the Unicode-script pass
signal 3 already runs — 57 ms for all 560 pages, against 1.7 s for the
`pdftotext` that produced the text and 4.0 s for `lingua-go` across the 554 it
was measured on.

**Coverage.** 34 table entries over 33 languages and two scripts: seven Cyrillic
(ru, uk, be, bg, sr, mk, kk) and 27 Latin. Serbian appears in both, because it is
written in both. On 31 paragraphs of ordinary manual copy — one per language,
hermetic, no PDF — 25 were named correctly, six were declined (four carrying no
distinctive character at all, two as declared ties), and none was named wrongly.

**Accuracy on a real document, and it is lower than the hermetic figure.** The 31
paragraphs above are one clean paragraph per language. Measured instead against the
L40's own printed page tab — which is correct on all 553 of its content pages, so it
is ground truth — over the 685 columns of that manual where the signal named a
language at all: **93% correct**. The 7% are largely the sibling groups below, which
that document has in quantity, plus short table cells.

Two things follow. First, the signal earns its place as a corroborator and a
fallback, not as an authority: where a printed tab exists it must win, which is what
regions.md rule 1 does. Second, and less obvious, **the errors do not correlate with
how much evidence the signal had.** Bucketed by distinctive-character count, accuracy
is flat at every cut from 1 to 50 marks, and one wrong naming carries 118. A
minimum-evidence threshold was designed against this measurement and abandoned by it.
That is worth recording precisely because it is the intuitive fix.

**What it cannot do, named.** Three groups have byte-identical repertoires, and
the signal reports them tied rather than choosing:

| tied | why |
|---|---|
| `da` `no` | both æ ø å é. Bokmål and Nynorsk share it too |
| `bs` `hr` `sr` in Latin script | all č ć đ š ž |
| `en` `id` `ms` | nothing outside a-z: invisible, all three equally |

A second, quieter weakness is one-way rather than symmetric. Where one alphabet
is a strict subset of another — `bg` ⊂ `ru` ⊂ `kk`, `fi` ⊂ `sv`, `sl` ⊂ `hr`,
`nl` ⊂ `fr` — the smaller language wins by exercising all of itself, and it is
only *right* when the text is long enough that the larger one's extra letters
would have shown up. On a heading or a caption it is a coin toss dressed as an
answer, so the runner-up is always returned ranked beneath the winner rather than
discarded, and a floor of three distinctive characters stops one brand name being
read as a language at all.

**Czech and Slovak are not on that list, and the usual expectation is wrong
here.** They are the standard example of a pair a trigram detector flip-flops on
mid-section, and by repertoire they separate cleanly: Czech ř, ě and ů and Slovak
ľ, ĺ, ŕ, ä and ô are frequent enough in ordinary prose that each contradicts the
other outright. Measured on a paragraph each, Czech is not even admitted as a
candidate for the Slovak text.

**It does not replace signal 4, and it is not an argument for reopening that
decision.** It reads alphabets, not language: it says nothing about the 25
Latin-script languages when a page happens to carry no diacritic, and it cannot
tell Indonesian from Malay or English at all. It does dent two of the systematic
failures recorded above, in different ways and neither completely:

- **Latin-script Serbian** — `lingua-go` scored 0 of 16 pages and answered `hr` or
  `bs` with confidence. This signal narrows the same pages from 25 candidates to
  exactly three and refuses to pick. That is not a label, but a declared tie is
  worth more than a confident wrong answer, and the printed index or the page tag
  breaks it.
- **Uzbek** — untouched. `lingua-go` has no Uzbek and neither does this table.

**Status: implemented, not wired in.** `internal/doc/repertoire.go` is a pure
function with its own tests; nothing calls it from `Analyze` or `Reconcile` yet,
so it changes no stored row and no reconciled outcome.

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
