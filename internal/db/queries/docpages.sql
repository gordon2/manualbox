-- Upsert on the natural key, because a probe job may run twice and must converge
-- on the same rows rather than duplicating them.
-- name: UpsertDocPage :exec
INSERT INTO doc_pages (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, page_no) DO UPDATE SET
    chars         = excluded.chars,
    script        = excluded.script,
    page_tag      = excluded.page_tag,
    printed_folio = excluded.printed_folio,
    lang          = excluded.lang,
    lang_source   = excluded.lang_source;

-- name: ListDocPages :many
SELECT * FROM doc_pages WHERE document_id = ? ORDER BY page_no;

-- name: GetDocPage :one
SELECT * FROM doc_pages WHERE document_id = ? AND page_no = ?;

-- name: DeleteDocPages :exec
DELETE FROM doc_pages WHERE document_id = ?;

-- How many pages the document holds in each resolved language. The CAST is
-- required: without it sqlc infers interface{} for the aggregate.
-- name: CountDocPagesByLang :many
SELECT lang, CAST(count(*) AS INTEGER) AS pages
FROM doc_pages
WHERE document_id = ? AND lang <> ''
GROUP BY lang
ORDER BY pages DESC, lang;

-- name: CountDocPagesWithText :one
SELECT CAST(count(*) AS INTEGER) AS pages
FROM doc_pages
WHERE document_id = ? AND chars > 0;

-- How far each page's PDF number runs ahead of the number printed on the paper,
-- as a histogram over the pages that print one at all.
--
-- This is derived on read rather than stored, because doc_pages already holds the
-- whole answer and a stored copy could only go stale against it: the folio is
-- re-read on every probe, so a change to how it is read must move this number in
-- the same breath. It is one small grouped scan per document over rows the probe
-- already wrote, asked once when a conversion is served, not per block or per page.
--
-- The caller decides which row to believe -- see registry.FolioOffset -- so the
-- whole histogram comes back rather than just its first row. The CASTs are
-- required: without them sqlc infers interface{} for both columns. "offset" is a
-- SQL keyword, hence the name.
-- name: DocPageFolioOffsets :many
SELECT CAST(page_no - printed_folio AS INTEGER) AS folio_offset,
       CAST(count(*) AS INTEGER) AS pages
FROM doc_pages
WHERE document_id = ? AND printed_folio IS NOT NULL
GROUP BY folio_offset
ORDER BY pages DESC, folio_offset;
