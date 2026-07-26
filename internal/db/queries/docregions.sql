-- Queries over doc_regions: one language's territory on a page. See
-- 00004_doc_regions.sql for the schema's reasoning and docs/design/regions.md for
-- the contract.
--
-- TWO RULES FOR THIS FILE, BOTH LEARNED THE HARD WAY WHILE WRITING IT.
--
-- 1. KEEP THIS FILE PURE ASCII. No em-dashes, no curly quotes. sqlc v1.31.1
--    tracks each statement's end offset in BYTES but slices the text in
--    CHARACTERS, so every non-ASCII byte anywhere earlier in the file silently
--    truncates the tail of every statement after it. Measured exactly: with one
--    em-dash in a comment, "ORDER BY first_page, code" generated as
--    "ORDER BY first_page, co"; with four em-dashes it generated as
--    "ORDER BY first_pa". The truncation equals the running count of non-ASCII
--    overhead bytes, one character lost per extra byte.
--
--    This is the worst failure shape available: `make sqlc` exits 0, the generated
--    Go compiles, the linter is happy, and the statement fails at PREPARE time
--    inside a background job against a user's database. All ten pre-existing query
--    files happen to be pure ASCII, which is the only reason this had not bitten
--    anyone yet. That was checked rather than assumed: 0 non-ASCII bytes across
--    every one of them. TestDocRegionQueriesExecute in internal/db is the guard;
--    it runs every statement below against a real migrated database, so a mangled
--    one cannot reach a user.
--
-- 2. Columns are listed explicitly rather than with SELECT *, so that adding a
--    column to doc_regions later cannot silently change every caller's row shape.

-- Upsert on the natural key (document_id, source, page, x0), because a probe job
-- may run twice and must converge on the same rows rather than duplicating them.
--
-- This is belt and braces beside the delete that SaveProbe does first, and it
-- cannot be the whole story: source is part of the key and a region's source can
-- change between probes, so an upsert alone would leave the superseded row behind
-- at the same x0. The note at the foot of 00004_doc_regions.sql explains why.
-- name: UpsertDocRegion :exec
INSERT INTO doc_regions (document_id, source, page, x0, x1, code, lang, chars, runs,
                         conflict, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, source, page, x0) DO UPDATE SET
    x1         = excluded.x1,
    code       = excluded.code,
    lang       = excluded.lang,
    chars      = excluded.chars,
    runs       = excluded.runs,
    conflict   = excluded.conflict,
    note       = excluded.note,
    created_at = excluded.created_at;

-- Reading order: down the page, then left to right across it. A whole-page region
-- sorts first on its page because it begins at x0 = 0.
-- name: ListDocRegions :many
SELECT document_id, source, page, x0, x1, code, lang, chars, runs, conflict, note, created_at
FROM doc_regions
WHERE document_id = ?
ORDER BY page, x0;

-- name: ListDocRegionsForPage :many
SELECT document_id, source, page, x0, x1, code, lang, chars, runs, conflict, note, created_at
FROM doc_regions
WHERE document_id = ? AND page = ?
ORDER BY x0;

-- Replacing a document's regions wholesale is how a re-probe stays honest, and it
-- is required rather than merely tidy: a region whose attribution changed is a new
-- row under this key, so without the delete the superseded one lingers and the
-- page reports itself twice.
-- name: DeleteDocRegions :exec
DELETE FROM doc_regions WHERE document_id = ?;

-- The region map as shown to the user: one row per language label, with the
-- characters and runs it holds, how many pages it appears on, and whether any of
-- its regions are disputed.
--
-- Characters rather than pages is the point, because a page holding three
-- languages is not a unit of size; pages are still what a reader is shown, so both
-- are reported. Every aggregate is wrapped in CAST(... AS INTEGER): without it
-- sqlc cannot infer the type and emits interface{}, pushing a type assertion onto
-- every caller.
-- name: SummarizeDocRegions :many
SELECT code,
       lang,
       CAST(sum(chars) AS INTEGER)           AS chars,
       CAST(sum(runs) AS INTEGER)            AS runs,
       CAST(count(DISTINCT page) AS INTEGER) AS pages,
       CAST(min(page) AS INTEGER)            AS first_page,
       CAST(max(conflict) AS INTEGER)        AS disputed
FROM doc_regions
WHERE document_id = ?
GROUP BY code, lang
ORDER BY first_page, code;
