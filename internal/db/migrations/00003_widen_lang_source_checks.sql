-- M1: admit 'repertoire' as a language signal in the two shipped tables.
--
-- `repertoire` already exists in Go as doc.SourceRepertoire — the signal that
-- names a language from the characters a page actually uses, the letters only
-- some alphabets have. 00002 listed the other five signals and missed this one,
-- so writing it would fail a CHECK at runtime rather than at review time. This
-- migration is only that correction. The doc_regions table that motivated it
-- lands separately (docs/design/regions.md), so that a schema change to other
-- people's data stays reviewable and revertible on its own.
--
-- Why a rebuild and not an ALTER. SQLite has no way to alter a CHECK
-- constraint: the constraint is part of the stored CREATE TABLE text, and only
-- create-copy-drop-rename replaces it. That is the documented procedure, and it
-- is why 00002's own comment pre-listed the two document states it knew were
-- coming — extending a closed set here costs a table rebuild.
--
-- Why this rebuild is safe. Both tables are foreign-key LEAVES: they reference
-- documents(id), and nothing references them. So the drop cannot orphan a child
-- row and the rename cannot leave a dangling reference. Their parent, documents,
-- is untouched. Every column, type, NOT NULL, DEFAULT, other CHECK, composite
-- PRIMARY KEY, STRICT and cascade below is reproduced verbatim from 00002; the
-- CHECK list is the only difference, and only by appending. Verified empirically
-- that this runs inside goose's transaction with _pragma foreign_keys(1) set
-- (internal/db/db.go), so a failure part-way leaves the old tables intact.
--
-- Append-only, and both directions rebuild. The Down migration restores 00002's
-- narrower lists rather than dropping the tables, so a downgrade keeps the rows.
-- It will fail, correctly, if any row by then holds 'repertoire'.

-- +goose Up

-- doc_pages: one row per page of the original, holding what each signal saw.
CREATE TABLE doc_pages_new (
    document_id  TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    -- 1-based page number in the original PDF.
    page_no      INTEGER NOT NULL CHECK (page_no >= 1),

    -- Extracted characters (runes). Zero means no text layer on this page.
    chars        INTEGER NOT NULL DEFAULT 0 CHECK (chars >= 0),
    -- Dominant Unicode script, e.g. 'Latin', 'Cyrillic', 'Han', 'Kana'. Empty
    -- when the page has no text to judge.
    script       TEXT    NOT NULL DEFAULT '',
    -- The language code printed on the page itself, when the manual prints one.
    -- Empty when absent, which is common and not an error.
    page_tag     TEXT    NOT NULL DEFAULT '',
    -- The page number printed in the page's own footer, which is not the PDF
    -- page number. Nullable: some pages print none at all.
    printed_folio INTEGER,

    -- The resolved language for this page and which signal decided it.
    -- 'repertoire' is the addition: see the header.
    lang         TEXT    NOT NULL DEFAULT '',
    lang_source  TEXT    NOT NULL DEFAULT ''
                         CHECK (lang_source IN ('', 'page-tag', 'index', 'script', 'repertoire', 'detector', 'reconciled')),

    PRIMARY KEY (document_id, page_no)
) STRICT;

-- Named columns on both sides, so a future column added to one table and not the
-- other fails loudly here instead of shifting values silently.
INSERT INTO doc_pages_new (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
SELECT document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source FROM doc_pages;

DROP TABLE doc_pages;
ALTER TABLE doc_pages_new RENAME TO doc_pages;

-- Indexes belong to the dropped table, so they are recreated, not renamed.
CREATE INDEX doc_pages_lang_idx ON doc_pages(document_id, lang);

-- doc_langs: a contiguous span of pages in one language, per signal.
CREATE TABLE doc_langs_new (
    document_id TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,

    -- Which signal produced this run. Part of the key, so each signal's view
    -- coexists with the others. 'repertoire' is the addition.
    source      TEXT    NOT NULL
                        CHECK (source IN ('page-tag', 'index', 'script', 'repertoire', 'detector', 'reconciled')),

    -- Zero means "this signal named a language but could not place it".
    --
    -- That is a real and useful state, not a defect to reject. A printed index
    -- routinely claims a page that does not exist, or one whose script makes the
    -- claim impossible — a real manual lists Czech at a page that is Arabic. The
    -- claim is still evidence: it tells the user their manual's contents table is
    -- wrong, which is exactly the kind of conflict this schema exists to surface
    -- rather than silently discard. So the label is kept and the boundary is not
    -- invented.
    pdf_start   INTEGER NOT NULL CHECK (pdf_start >= 0),
    pdf_end     INTEGER NOT NULL CHECK (pdf_end >= 0),

    -- code is the language as the document expresses it, which is not always a
    -- valid tag: real manuals print 'UA' for Ukrainian, 'CZ' for Czech and
    -- 'ZH-HK' for Cantonese. lang is that value normalised to BCP-47, empty when
    -- it could not be normalised — keeping both means an unrecognised code is
    -- still reportable instead of being dropped.
    code        TEXT    NOT NULL,
    lang        TEXT    NOT NULL DEFAULT '',

    -- The section title as printed in the manual's own contents table, in that
    -- language. Only the index signal can supply this.
    title       TEXT    NOT NULL DEFAULT '',
    -- The start page the printed index claims, which is frequently 1-2 off from
    -- the page actually printed. Nullable; only the index signal sets it.
    printed_page INTEGER,

    confidence  REAL    NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 1),
    -- Set on a reconciled run when the signals disagreed about it. The note says
    -- how. Surfacing the conflict is the requirement; resolving it silently is
    -- what the design forbids.
    conflict    INTEGER NOT NULL DEFAULT 0 CHECK (conflict IN (0, 1)),
    note        TEXT    NOT NULL DEFAULT '',

    created_at  INTEGER NOT NULL,

    -- Natural key, so re-probing overwrites rather than duplicating. The code is
    -- part of it, not just the starting page: a signal may name several languages
    -- it could not place, and those all share a start of 0. Keying on the page
    -- alone would silently collapse them into whichever was written last.
    PRIMARY KEY (document_id, source, code, pdf_start),

    CHECK (pdf_end >= pdf_start)
) STRICT;

INSERT INTO doc_langs_new (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page, confidence, conflict, note, created_at)
SELECT document_id, source, pdf_start, pdf_end, code, lang, title, printed_page, confidence, conflict, note, created_at FROM doc_langs;

DROP TABLE doc_langs;
ALTER TABLE doc_langs_new RENAME TO doc_langs;

CREATE INDEX doc_langs_source_idx ON doc_langs(document_id, source);
CREATE INDEX doc_langs_lang_idx   ON doc_langs(document_id, lang);

-- +goose Down

-- The same rebuild in reverse, restoring 00002's narrower CHECK lists. Rows are
-- carried across rather than dropped; a row holding 'repertoire' makes this fail,
-- which is the honest outcome — there is nowhere for that value to go.
CREATE TABLE doc_pages_old (
    document_id  TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    page_no      INTEGER NOT NULL CHECK (page_no >= 1),
    chars        INTEGER NOT NULL DEFAULT 0 CHECK (chars >= 0),
    script       TEXT    NOT NULL DEFAULT '',
    page_tag     TEXT    NOT NULL DEFAULT '',
    printed_folio INTEGER,
    lang         TEXT    NOT NULL DEFAULT '',
    lang_source  TEXT    NOT NULL DEFAULT ''
                         CHECK (lang_source IN ('', 'page-tag', 'index', 'script', 'detector', 'reconciled')),
    PRIMARY KEY (document_id, page_no)
) STRICT;

INSERT INTO doc_pages_old (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
SELECT document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source FROM doc_pages;

DROP TABLE doc_pages;
ALTER TABLE doc_pages_old RENAME TO doc_pages;

CREATE INDEX doc_pages_lang_idx ON doc_pages(document_id, lang);

CREATE TABLE doc_langs_old (
    document_id TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    source      TEXT    NOT NULL
                        CHECK (source IN ('page-tag', 'index', 'script', 'detector', 'reconciled')),
    pdf_start   INTEGER NOT NULL CHECK (pdf_start >= 0),
    pdf_end     INTEGER NOT NULL CHECK (pdf_end >= 0),
    code        TEXT    NOT NULL,
    lang        TEXT    NOT NULL DEFAULT '',
    title       TEXT    NOT NULL DEFAULT '',
    printed_page INTEGER,
    confidence  REAL    NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 1),
    conflict    INTEGER NOT NULL DEFAULT 0 CHECK (conflict IN (0, 1)),
    note        TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (document_id, source, code, pdf_start),
    CHECK (pdf_end >= pdf_start)
) STRICT;

INSERT INTO doc_langs_old (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page, confidence, conflict, note, created_at)
SELECT document_id, source, pdf_start, pdf_end, code, lang, title, printed_page, confidence, conflict, note, created_at FROM doc_langs;

DROP TABLE doc_langs;
ALTER TABLE doc_langs_old RENAME TO doc_langs;

CREATE INDEX doc_langs_source_idx ON doc_langs(document_id, source);
CREATE INDEX doc_langs_lang_idx   ON doc_langs(document_id, lang);
