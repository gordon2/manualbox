# Finding the sentence you need

The paper pile is unsearchable, and you need the router manual at exactly the
moment the internet is down. That is [README](../../README.md)'s first problem, so
this is the first thing built on top of [conversion](conversion.md): blocks are the
unit the reader stores, and blocks are what is indexed.

Everything below was measured. The corpus is both fixtures converted for German,
Russian, Japanese, Thai and Hebrew — **3,122 blocks** across the parallel-columns
manual's 68 pages and the sequential manual's 560 — loaded into one database per
FTS5 variant through the same `modernc.org/sqlite` driver the binary ships.

## What a hit has to say

Which manual, which page, which language, and enough text to recognise. "Something
matched on page 47" does not solve the problem this exists for, so every hit joins
`documents` and `devices` and carries the filename and the device's name. It also
carries the block's natural key — page, region left edge, index — which is the
citation [conversion.md](conversion.md) specifies, so a hit deep-links to the exact
paragraph and still points there after a re-conversion.

## Where the index lives

**FTS5 over `doc_blocks`, external content, maintained by triggers.**

`content='doc_blocks'` means FTS5 stores the index and reads text back out of the
table rather than keeping its own copy. Measured, whole database file, after
`optimize` and `VACUUM`, against 626,688 bytes for `doc_blocks` alone:

| | total | index | vs blocks alone |
|---|---|---|---|
| standalone `unicode61` | 1,388,544 | +761,856 | 2.22x |
| external `unicode61` | 897,024 | +270,336 | 1.43x |
| standalone `trigram` | 1,998,848 | +1,372,160 | 3.19x |
| external `trigram` | 1,507,328 | +880,640 | 2.41x |

The duplicated text is the same 491,520 bytes in both pairs. External content costs
nothing but maintenance, and maintenance is where the decision that matters is.

**Triggers, not statements next to each write.** Three paths change `doc_blocks`
and only two are visible in Go: `registry.saveBlocks`' wholesale delete-and-reinsert,
its upsert-in-place, and `documents ON DELETE CASCADE`, which runs **no Go at all** —
deleting a device removes its documents, which removes their blocks, entirely inside
SQLite. Triggers cover all three by construction and run inside whatever transaction
the write is already in, which is what `SaveConversion` needs: the blocks, the index
and the document's `ready` state commit together or not at all.

That the cascade fires them was measured rather than assumed, because SQLite's own
documentation makes trigger firing on a foreign-key action conditional on
`recursive_triggers`, which manualbox does not set. With `foreign_keys(1)` and
`recursive_triggers` off — what `internal/db` actually opens with — the cascade
removes the index rows and FTS5's `integrity-check` passes.

**And the failure it prevents is not the obvious one.** Dropping the delete trigger
does *not* leave a deleted manual findable: every search joins the index to
`doc_blocks`, so an entry whose row is gone joins to nothing and vanishes from the
results by accident. Measured that way round first, and it made the obvious control
assertion pass over an index FTS5 already reports as malformed.

The real failure needs one more step. SQLite gives a new row `max(rowid)+1`, so
deleting the highest block frees a rowid the next insert takes, and the stale entry
then points at a real row of a *different* document. Searching for a word from the
deleted manual answers with another manual, another page, and text that does not
contain the word. **A wrong citation rather than a missing one**, which is the
failure this project can least afford, because a citation is what extraction will
hang a maintenance schedule on. `revertCheckTheDeleteTrigger` in `internal/db` is
that run, kept as a test.

`doc_blocks`' rowid is not an `INTEGER PRIMARY KEY` alias, since its key is
composite, so SQLite does not promise to preserve it across a `VACUUM`. Nothing in
manualbox runs `VACUUM` (grepped, not assumed), and a `VACUUM` of a 3,122-block
database with holes in its rowid sequence left `max(rowid)` and every hit unchanged.
It is still not a promise; the repair is
`INSERT INTO doc_blocks_fts(doc_blocks_fts) VALUES ('rebuild')`.

## The tokeniser, which is the one decision that could not be reasoned out

`unicode61` splits on whitespace and punctuation. `trigram` indexes every run of
three characters and therefore matches substrings. The corpus is 34 languages
including Chinese, Japanese, Thai, Hebrew and Arabic, so the description alone
decides nothing. Real words from each script, same corpus, same driver:

| query | `unicode61` | `trigram` |
|---|---|---|
| `Filter` (de) | 21 | 69 |
| `Saugkraft` (de) | 7 | 7 |
| `Gerat` (de, folded) | 71 | 96 |
| Russian *filtr* | 31 | 96 |
| Japanese *toriatsukai setsumeisho* | **0** | 6 |
| Thai *khu mue* | **0** | 6 |
| Hebrew *madrikh*, as stored | 1 | 5 |

**`unicode61` finds nothing in Japanese and nothing in Thai.** Not degraded —
absent. A whole CJK or Thai run is one token, so it matches only a query that
happens to be the entire run: the two-character Japanese word for "power" scores 2
hits against 27 real occurrences, and those 2 are where punctuation isolated it.

**So: `trigram`, one index for every script.** It costs 880,640 bytes against
270,336 — 3.3x the index, 2.40x a blocks-only database, about 195 bytes per stored
block. The higher Latin counts are substring matches: `Filter` also finds
`Luftfilter` and `Filterdeckel`, which in German is closer to what a person meant
than token-exact matching.

**Two indexes were rejected.** `unicode61` for the space-separated scripts and
`trigram` for the rest would give the majority of languages token-exact precision
and still serve CJK. It costs 1,150,976 bytes rather than 880,640, both need their
own triggers, and every query must guess from its own characters which index can
answer it — at which point a query mixing a German word and a Japanese one has no
right answer. One index that is somewhat blunt everywhere beats two that are sharp
until the household is multilingual, which every household with this kind of manual
already is.

### The named limitation

**A query shorter than three characters is not in the index at all.** Not fewer
results — none. Two characters is an ordinary word in Chinese and Japanese: the
words for "power" and "product" occur in 27 and 24 stored blocks and the index finds
0 of each. That is a real hole in exactly the scripts `trigram` was chosen for.

So a query the index cannot represent is answered by scanning `doc_blocks` instead,
measured at **1.9 ms** over the 3,122-block corpus against 0.2 ms through the index,
and the API reports `mode: "substring"` so the difference is visible rather than
guessed at. `instr()` rather than `LIKE`, because `%` and `_` in a user's query
would be wildcards and a search box must not have a pattern language. Case folding
there is SQLite's `lower()`, which is ASCII only — exact for the CJK queries this
path exists for, case-sensitive for a two-letter Cyrillic one, which is the honest
limit of a scan that must not build an index to fix.

One short word sends the **whole** query to the scan rather than being dropped from
it. A search for two words that quietly became a search for one is indistinguishable
from a correct answer.

### Diacritics: the cost it was weighed against does not exist

A German household on any keyboard types `Gerat` and should find `Gerät`. The worry
was that `remove_diacritics` also folds Cyrillic and Greek, which would be
expensive here — half this corpus is not Latin.

Measured, across all three `unicode61` modes and both `trigram` modes:

| stored | queried | folded? |
|---|---|---|
| `Gerät` | `Gerat` | yes, when on |
| `ещё` | `еще` | **never** |
| `Київ` | `Киiв` | **never** |
| `οδηγίες` | `οδηγιες` | **never** |
| Hebrew with niqqud | without | **never** |

FTS5's folding table covers precomposed Latin and does not reach Cyrillic, Greek or
Hebrew. **It is on**, and it has to be set explicitly: `unicode61` folds by default
but `trigram` does not, and with it off `Gerat` finds 0 of the 96 blocks holding
`Gerät`. The index is 4,096 bytes *smaller* with folding on.

### What no tokeniser fixes, and what stopped needing one

**The stored Hebrew used to be in visual order, and is not any more.**
`internal/doc` reads the runs a right-to-left page paints and the PDF paints them
reversed, so the word for "manual" was stored as its own reverse: findable by a
query typed backwards (5 blocks) and not by one a Hebrew speaker would type (0
blocks). That was upstream of the index, in extraction, and search could not repair
it and did not pretend to.

`internal/doc/bidi.go` repaired it there, and the measurement is now the exact
inverse of the paragraph above: `מדריך` typed forwards finds **5** blocks and typed
backwards **0**, over the same Hebrew section. `internal/registry`'s
`TestHebrewIsFoundTypedForwards` is that measurement, run against the real manual,
and it exists because this claim had lived in prose with no test under it.

It took two steps. The repair first read 4 and 1: one line of page 188 prints the
support URL and a Hebrew sentence together, `lineIsRightToLeft` decided direction by
majority of a line's strong characters, the URL's Latin outweighed the Hebrew, and
that line was joined left to right and left reversed. `internal/verify` reported the
same page from the other side, off a comparison sharing no code with this one. Giving
the decision to the **region's language**, with the majority as fallback, closed both.

**One thing search still can't be asked about.** Word ORDER. Three now-fixed defects in
`internal/doc` reordered stored text without changing a single word of it — page 204's
support URL, page 211's Arabic list markers, page 204's laser standard. Every word was
present throughout, so the index found them all and only the reading was wrong: **no query
reveals that class of defect**, and none is pinned here. conversion.md carries all three
and why the verifier's word check could not see any of them either.

## Ranking

`bm25`, with **1.0 subtracted for a heading**, and both numbers reported.

bm25 favours short documents, and on this corpus that is often wrong: for `Filter`
in the column manual it puts the parts-list fragments `1. Filter` and `13. Filter`
first and pushes the maintenance heading `Ausblasfilter austauschen` to tenth. A
heading names a section, so it answers *where does it say this* better than a
passing mention. Within one query bm25 spans about −9 to −2 with adjacent hits
differing by 0.05 to 0.5, so 1.0 moves a heading past hits of comparable quality
without overturning a decisively better one — on `Saugkraft` the troubleshooting
cell `Saugkraft ist zu gering` at −8.5 still leads, which is right.

It is a judgement, so `bm25` and `score` are both in the response and their gap is
the bonus. A number in a response can be argued with; one buried in an `ORDER BY`
cannot.

## Scope

**Across documents by default**, because "which manual says X" is a question about
the household and someone looking for the descaling interval does not know which
manual to open. `?documentId=` narrows it, which is what a reader already inside a
document asks. An unknown id is a search of nothing rather than a 404: the parameter
scopes a search, and answering 404 would turn it into a way to test whether an id
exists.

A query matching nothing returns `indexed`, the number of blocks there are to
search. "No manual says that" and "nothing has been converted yet" are otherwise
the same empty list, and the second is not a search failure — the same distinction
`Service.Blocks` makes between empty and absent.

## What this deliberately does not do

- **No language filter.** A household's scope already decided which languages were
  converted, so the index holds only those; filtering further is a reader's
  question, not a search one. The language is on every hit.
- **No highlight markup.** The snippet is about 64 characters of plain text around
  the match. Inventing a delimiter would presume how a screen renders it, and the
  search screen is a separate slice.
- **No stemming and no synonyms.** A trigram substring match already covers German
  compounding, which is most of what stemming would buy here, and a stemmer is
  per-language — a per-language index is the two-index design rejected above.
- **No paging beyond a limit.** `limit` caps the hits at 100 and `truncated` says
  the list was cut off. Offset paging over a ranked result set that changes when a
  document is re-converted is a promise this cannot keep yet.
