-- Queries over doc_blocks_fts: which manual says X, and where. See
-- 00006_block_search.sql for the index's reasoning and the measurement behind the
-- tokeniser, and docs/design/search.md for the contract.
--
-- THIS FILE MUST STAY PURE ASCII. No em-dashes, no curly quotes. sqlc v1.31.1
-- mixes up character and byte offsets when it cuts statements out of a file, so
-- one non-ASCII character anywhere above corrupts every statement after it --
-- silently in the dangerous case: `make sqlc` exits 0, the Go compiles, the
-- linter passes, and the statement fails at PREPARE time inside a request. The
-- full measurement is in the header of docregions.sql; TestQueryFilesAreASCII is
-- the cause-side guard and TestSearchQueriesExecute the symptom-side one.
--
-- TWO THINGS SQLC CANNOT PARSE, BOTH LEARNED HERE AND BOTH LOAD-BEARING.
--
-- 1. `WHERE doc_blocks_fts MATCH ?` -- the documented FTS5 form, where the left
--    side is the table's own hidden column -- fails generation with `column
--    "doc_blocks_fts" does not exist`, because sqlc models the virtual table as
--    its declared columns only. `doc_blocks_fts.text MATCH ?` generates and is
--    the same query: text is the only indexed column, so a column-scoped match
--    over it covers the whole index. Verified against a real database rather than
--    assumed, in TestSearchQueriesExecute.
--
-- 2. `AS rank` fails generation with `mismatched input 'rank'`, so the ordering
--    column is named `score`. That is a happy accident: `rank` is also FTS5's own
--    magic column, and a result column of that name reads as if it were that.
--
-- Columns are listed explicitly rather than with SELECT *, so that adding a
-- column later cannot silently change every caller's row shape.
--
-- WHY EVERY QUERY JOINS documents AND devices. A hit has to say WHICH manual, not
-- merely that something matched: README's first problem is that the paper pile is
-- unsearchable, and "page 47 of something" does not solve it. The filename and the
-- device's name are what a household recognises, and they cost one join each
-- against a primary key.
--
-- WHY THE HEADING BONUS IS 1.0. bm25 is negative and lower is better, so the
-- bonus is subtracted. Measured on both real manuals: within one query bm25 spans
-- about -9 to -2, and adjacent hits differ by 0.05 to 0.5, so 1.0 moves a heading
-- past hits of comparable quality without overturning a decisively better one. On
-- "Filter" in the column manual it lifts the maintenance heading "Ausblasfilter
-- austauschen" over the parts-list fragments ("1. Filter", "13. Filter") that
-- bm25's short-document bias otherwise puts first; on "Saugkraft" the
-- troubleshooting cell "Saugkraft ist zu gering" at -8.5 stays first, which is
-- right. Both numbers are returned, so the judgement can be argued with rather
-- than merely trusted.

-- name: SearchBlocks :many
SELECT b.document_id, d.filename, d.state, v.id AS device_id, v.name AS device_name,
       b.page, b.region_x0, b.idx, b.kind, b.level, b.lang, b.chars,
       snippet(doc_blocks_fts, 0, '', '', '...', 64) AS snippet,
       CAST(bm25(doc_blocks_fts) AS REAL) AS bm25,
       CAST(bm25(doc_blocks_fts)
            - (CASE b.kind WHEN 'heading' THEN 1.0 ELSE 0.0 END) AS REAL) AS score
FROM doc_blocks_fts
JOIN doc_blocks b ON b.rowid = doc_blocks_fts.rowid
JOIN documents d ON d.id = b.document_id
JOIN devices v ON v.id = d.device_id
WHERE doc_blocks_fts.text MATCH sqlc.arg(match)
ORDER BY score, b.document_id, b.page, b.region_x0, b.idx
LIMIT sqlc.arg(limit);

-- The same question narrowed to one manual, which is what a reader already inside
-- a document asks. A separate statement rather than an optional parameter, because
-- sqlc has no optional parameters and `b.document_id = ? OR ? = ''` would put the
-- widest query in the household on the sentinel path.
-- name: SearchBlocksInDocument :many
SELECT b.document_id, d.filename, d.state, v.id AS device_id, v.name AS device_name,
       b.page, b.region_x0, b.idx, b.kind, b.level, b.lang, b.chars,
       snippet(doc_blocks_fts, 0, '', '', '...', 64) AS snippet,
       CAST(bm25(doc_blocks_fts) AS REAL) AS bm25,
       CAST(bm25(doc_blocks_fts)
            - (CASE b.kind WHEN 'heading' THEN 1.0 ELSE 0.0 END) AS REAL) AS score
FROM doc_blocks_fts
JOIN doc_blocks b ON b.rowid = doc_blocks_fts.rowid
JOIN documents d ON d.id = b.document_id
JOIN devices v ON v.id = d.device_id
WHERE doc_blocks_fts.text MATCH sqlc.arg(match)
  AND b.document_id = sqlc.arg(document_id)
ORDER BY score, b.page, b.region_x0, b.idx
LIMIT sqlc.arg(limit);

-- THE HOLE THE TOKENISER LEAVES, AND WHAT FILLS IT.
--
-- A trigram index holds no token shorter than three characters, so a query of one
-- or two characters matches nothing at all -- not "fewer results", none. That is
-- tolerable in German and Russian, where a two-letter query is not a word anyone
-- searches for, and it is not tolerable in Chinese or Japanese, where two
-- characters is an ordinary word: measured on the sequential manual, the two
-- characters for "power" occur in 27 stored blocks and those for "product" in 24,
-- and the index finds 0 of each.
--
-- So a query the index cannot represent is answered by scanning instead. Measured
-- over the 3,122 blocks of both real manuals: 1.9 ms for a two-character Japanese
-- query, against 0.2 ms for the same question through the index. A household's
-- whole library is a small multiple of that corpus, so the scan stays inside a
-- request rather than becoming a job.
--
-- instr rather than LIKE, because `%` and `_` in a user's query are LIKE wildcards
-- and a search box must not have a pattern language. lower() on both sides is
-- SQLite's own, which folds ASCII and nothing else -- exact for the CJK queries
-- this path exists for, and case-sensitive for a two-letter Cyrillic one, which is
-- the honest limit of a scan that must not build an index to fix.
--
-- There is no bm25 here because there is no index term to weigh, so score is 0 on
-- every row and the order is the heading rule followed by reading order. A caller
-- tells the two paths apart by the mode the API reports, not by inferring it from
-- the numbers.
-- name: SearchBlocksSubstring :many
SELECT b.document_id, d.filename, d.state, v.id AS device_id, v.name AS device_name,
       b.page, b.region_x0, b.idx, b.kind, b.level, b.lang, b.chars,
       substr(b.text, max(1, instr(lower(b.text), lower(sqlc.arg(needle))) - 24), 64) AS snippet,
       CAST(0.0 AS REAL) AS bm25,
       CAST(0.0 AS REAL) AS score
FROM doc_blocks b
JOIN documents d ON d.id = b.document_id
JOIN devices v ON v.id = d.device_id
WHERE instr(lower(b.text), lower(sqlc.arg(needle))) > 0
ORDER BY (CASE b.kind WHEN 'heading' THEN 0 ELSE 1 END),
         b.document_id, b.page, b.region_x0, b.idx
LIMIT sqlc.arg(limit);

-- name: SearchBlocksSubstringInDocument :many
SELECT b.document_id, d.filename, d.state, v.id AS device_id, v.name AS device_name,
       b.page, b.region_x0, b.idx, b.kind, b.level, b.lang, b.chars,
       substr(b.text, max(1, instr(lower(b.text), lower(sqlc.arg(needle))) - 24), 64) AS snippet,
       CAST(0.0 AS REAL) AS bm25,
       CAST(0.0 AS REAL) AS score
FROM doc_blocks b
JOIN documents d ON d.id = b.document_id
JOIN devices v ON v.id = d.device_id
WHERE instr(lower(b.text), lower(sqlc.arg(needle))) > 0
  AND b.document_id = sqlc.arg(document_id)
ORDER BY (CASE b.kind WHEN 'heading' THEN 0 ELSE 1 END),
         b.page, b.region_x0, b.idx
LIMIT sqlc.arg(limit);

-- How many blocks are indexed at all, so a caller can tell "nothing matched" from
-- "nothing has been converted yet". Wrapped in CAST(... AS INTEGER) for the reason
-- every other aggregate in these files is: without it sqlc cannot infer an
-- aggregate's type in SQLite and emits interface{}.
-- name: CountSearchableBlocks :one
SELECT CAST(count(*) AS INTEGER) FROM doc_blocks;
