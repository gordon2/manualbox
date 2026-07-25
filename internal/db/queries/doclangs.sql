-- name: UpsertDocLang :exec
INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, lang, title,
                       printed_page, confidence, conflict, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, source, code, pdf_start) DO UPDATE SET
    pdf_end      = excluded.pdf_end,
    lang         = excluded.lang,
    title        = excluded.title,
    printed_page = excluded.printed_page,
    confidence   = excluded.confidence,
    conflict     = excluded.conflict,
    note         = excluded.note;

-- name: ListDocLangs :many
SELECT * FROM doc_langs WHERE document_id = ? ORDER BY source, pdf_start;

-- name: ListDocLangsBySource :many
SELECT * FROM doc_langs WHERE document_id = ? AND source = ? ORDER BY pdf_start;

-- Replacing one signal's view wholesale is how a re-probe stays honest: a run
-- that no longer exists must disappear rather than linger from the previous
-- attempt. Scoped to one source so the other signals' rows survive.
-- name: DeleteDocLangsBySource :exec
DELETE FROM doc_langs WHERE document_id = ? AND source = ?;

-- name: DeleteDocLangs :exec
DELETE FROM doc_langs WHERE document_id = ?;

-- The language map as shown to the user: one row per language in the reconciled
-- view, with its page total and whether any of its runs are disputed.
--
-- A run with pdf_start = 0 named a language it could not place, so it covers no
-- pages at all. Counting its span reported a language the printed index merely
-- mentioned as a one-page section.
-- name: SummarizeDocLangs :many
SELECT code,
       lang,
       CAST(sum(CASE WHEN pdf_start = 0 THEN 0 ELSE pdf_end - pdf_start + 1 END)
            AS INTEGER)                              AS pages,
       CAST(count(*) AS INTEGER)                     AS runs,
       CAST(max(conflict) AS INTEGER)                AS disputed,
       CAST(min(pdf_start) AS INTEGER)               AS first_page
FROM doc_langs
WHERE document_id = ? AND source = ?
GROUP BY code, lang
ORDER BY first_page;

-- name: CountDocLangConflicts :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM doc_langs
WHERE document_id = ? AND source = ? AND conflict = 1;
