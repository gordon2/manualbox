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
