-- Queries over doc_blocks and doc_figures: what a conversion produced. See
-- 00005_doc_blocks.sql for the schema's reasoning and docs/design/conversion.md
-- for the contract.
--
-- THIS FILE MUST STAY PURE ASCII. No em-dashes, no curly quotes. sqlc v1.31.1
-- (pinned in tools/go.mod) mixes up character and byte offsets when it cuts
-- statements out of a file, so one non-ASCII character anywhere above corrupts
-- every statement after it -- silently, in the dangerous case: `make sqlc` exits
-- 0, the Go compiles, the linter passes, and the statement fails at PREPARE time
-- inside a background job against a user's database. The full measurement is in
-- the header of docregions.sql; TestQueryFilesAreASCII is the cause-side guard
-- and TestDocBlockQueriesExecute the symptom-side one.
--
-- Columns are listed explicitly rather than with SELECT *, so that adding a
-- column later cannot silently change every caller's row shape.

-- Upsert on the natural key (document_id, page, region_x0, idx), because a
-- conversion job may run twice and must converge on the same rows rather than
-- duplicating them.
--
-- Every non-key column is updated, kind and lang included. Nothing about a
-- block's classification is in the key, so a paragraph that a better heading rule
-- promotes to a heading is the same block updated in place -- see the note above
-- the primary key in 00005_doc_blocks.sql.
--
-- This is belt and braces beside the delete SaveConversion does first, and it
-- cannot be the whole story: a re-conversion that produces FEWER blocks in a
-- region would otherwise leave the tail of the previous run behind at higher
-- indices, where it reads as content.
-- name: UpsertDocBlock :exec
INSERT INTO doc_blocks (document_id, page, region_x0, idx, kind, level, text, lang,
                        x0, x1, y0, y1, lines, chars, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, page, region_x0, idx) DO UPDATE SET
    kind       = excluded.kind,
    level      = excluded.level,
    text       = excluded.text,
    lang       = excluded.lang,
    x0         = excluded.x0,
    x1         = excluded.x1,
    y0         = excluded.y0,
    y1         = excluded.y1,
    lines      = excluded.lines,
    chars      = excluded.chars,
    note       = excluded.note,
    created_at = excluded.created_at;

-- Reading order across the whole document: down the pages, then left to right
-- across each, then in order within a region. A whole-page region sorts first on
-- its page because its region_x0 is 0.
-- name: ListDocBlocks :many
SELECT document_id, page, region_x0, idx, kind, level, text, lang,
       x0, x1, y0, y1, lines, chars, note, created_at
FROM doc_blocks
WHERE document_id = ?
ORDER BY page, region_x0, idx;

-- The funnel's own query: one household's language, and nothing else. A German
-- reader of the columns manual gets the German column of each page rather than
-- the page, which conversion.md measures as a fifth of the work.
--
-- Blocks whose language was never established have lang = '' and are therefore
-- NOT returned by any language's query. That is deliberate rather than an
-- oversight: passing '' asks for exactly those, which is how the unnamed content
-- of a document stays reachable instead of becoming invisible.
-- name: ListDocBlocksByLang :many
SELECT document_id, page, region_x0, idx, kind, level, text, lang,
       x0, x1, y0, y1, lines, chars, note, created_at
FROM doc_blocks
WHERE document_id = ? AND lang = ?
ORDER BY page, region_x0, idx;

-- name: ListDocBlocksForPage :many
SELECT document_id, page, region_x0, idx, kind, level, text, lang,
       x0, x1, y0, y1, lines, chars, note, created_at
FROM doc_blocks
WHERE document_id = ? AND page = ?
ORDER BY region_x0, idx;

-- Replacing a document's blocks wholesale is how a re-conversion stays honest,
-- and it is required rather than merely tidy: a region that converted to 12
-- blocks and now converts to 9 would otherwise keep rows at idx 9, 10 and 11,
-- which a reader renders as three paragraphs of the previous run's text.
-- name: DeleteDocBlocks :exec
DELETE FROM doc_blocks WHERE document_id = ?;

-- What a conversion cost and covered, for the pipeline to report without reading
-- every block back. Every aggregate is wrapped in CAST(... AS INTEGER): without
-- it sqlc cannot infer an aggregate's type in SQLite and emits interface{},
-- pushing a type assertion onto every caller.
-- name: SummarizeDocBlocks :many
SELECT lang,
       CAST(count(*) AS INTEGER)             AS blocks,
       CAST(sum(chars) AS INTEGER)           AS chars,
       CAST(sum(lines) AS INTEGER)           AS lines,
       CAST(count(DISTINCT page) AS INTEGER) AS pages,
       CAST(min(page) AS INTEGER)            AS first_page
FROM doc_blocks
WHERE document_id = ?
GROUP BY lang
ORDER BY first_page, lang;

-- Upsert on (document_id, page, idx). A figure has no region and no language in
-- its key, because conversion.md settles that a picture belonging to no language
-- belongs to every language.
-- name: UpsertDocFigure :exec
INSERT INTO doc_figures (document_id, page, idx, x0, y0, x1, y1, ink, text_fraction,
                         dpi, pixel_width, pixel_height, blob_sha256, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, page, idx) DO UPDATE SET
    x0            = excluded.x0,
    y0            = excluded.y0,
    x1            = excluded.x1,
    y1            = excluded.y1,
    ink           = excluded.ink,
    text_fraction = excluded.text_fraction,
    dpi           = excluded.dpi,
    pixel_width   = excluded.pixel_width,
    pixel_height  = excluded.pixel_height,
    blob_sha256   = excluded.blob_sha256,
    created_at    = excluded.created_at;

-- name: ListDocFigures :many
SELECT document_id, page, idx, x0, y0, x1, y1, ink, text_fraction,
       dpi, pixel_width, pixel_height, blob_sha256, created_at
FROM doc_figures
WHERE document_id = ?
ORDER BY page, idx;

-- name: ListDocFiguresForPage :many
SELECT document_id, page, idx, x0, y0, x1, y1, ink, text_fraction,
       dpi, pixel_width, pixel_height, blob_sha256, created_at
FROM doc_figures
WHERE document_id = ? AND page = ?
ORDER BY idx;

-- name: DeleteDocFigures :exec
DELETE FROM doc_figures WHERE document_id = ?;
