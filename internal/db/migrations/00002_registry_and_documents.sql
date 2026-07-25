-- M1: the registry (locations, devices) and the document ingest tables.
--
-- Conventions are those of 00001_init.sql: prefixed ULID primary keys, INTEGER
-- Unix millisecond timestamps, STRICT tables.
--
-- One deliberate departure. The derived tables — doc_pages and doc_langs — use
-- COMPOSITE natural primary keys rather than ULIDs. That is not a style
-- preference: a job handler can run twice (a worker may die after doing its work
-- but before recording success), so the probe must be able to write its results
-- again without duplicating them. A natural key turns "run it again" into an
-- upsert over the same rows. With surrogate ULIDs the second run would insert a
-- parallel set of 560 page rows and the reconciliation would silently double.

-- +goose Up

-- Where things are. Nestable, so "House > Kitchen > Under the sink" works
-- without a separate hierarchy table.
CREATE TABLE locations (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    -- Self-reference for nesting. ON DELETE SET NULL rather than CASCADE:
    -- deleting a room should orphan its shelves, never silently delete the
    -- devices filed under them.
    parent_id  TEXT    REFERENCES locations(id) ON DELETE SET NULL,
    notes      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX locations_parent_idx ON locations(parent_id);

-- The things a household owns.
--
-- Deliberately absent: serial number and purchase price. Both are high-harm
-- fields that docs/design/privacy.md says must be encrypted with a key held
-- outside the data directory, and the keyring is not wired into the schema yet.
-- Adding them as plaintext columns now would mean either migrating real user
-- data later or quietly storing the most identifying field manualbox holds in
-- the clear. They land with the keyring, in their encrypted form.
CREATE TABLE devices (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL,
    brand       TEXT    NOT NULL DEFAULT '',
    model       TEXT    NOT NULL DEFAULT '',
    category    TEXT    NOT NULL DEFAULT '',
    location_id TEXT    REFERENCES locations(id) ON DELETE SET NULL,
    notes       TEXT    NOT NULL DEFAULT '',
    -- Date of purchase, millis. Nullable because it is frequently unknown, and
    -- an unknown date must not become the epoch.
    purchased_at INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX devices_location_idx ON devices(location_id);
CREATE INDEX devices_name_idx     ON devices(name);

-- An uploaded file belonging to a device. The bytes live in the blob store; this
-- row is the document's identity, its classification, and the result of probing
-- it.
CREATE TABLE documents (
    id          TEXT    PRIMARY KEY,
    device_id   TEXT    NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    blob_sha256 TEXT    NOT NULL REFERENCES blobs(sha256),

    -- The name the user's file had, for display only. Never used to build a path.
    filename    TEXT    NOT NULL DEFAULT '',
    media_type  TEXT    NOT NULL DEFAULT '',

    -- Classification drives privacy behaviour, not just presentation: receipts
    -- and warranties are never sent to a cloud provider (privacy.md), so the
    -- class has to be known before any provider is called.
    kind        TEXT    NOT NULL DEFAULT 'manual'
                        CHECK (kind IN ('manual', 'receipt', 'warranty', 'photo', 'other')),

    -- Pipeline state. 'converting' and 'ready' are listed now although nothing
    -- sets them yet: extending a CHECK constraint in SQLite means rebuilding the
    -- table, and naming the two states that are certainly coming costs nothing.
    --   uploaded       stored, probe queued
    --   probing        a worker is probing it
    --   awaiting_scope probed; waiting for the user to approve what to process
    --   declined       the user said no; the original is kept regardless
    --   converting     conversion in progress
    --   ready          nothing further to do automatically
    --   failed         probing or conversion failed permanently
    state       TEXT    NOT NULL DEFAULT 'uploaded'
                        CHECK (state IN ('uploaded', 'probing', 'awaiting_scope',
                                         'declined', 'converting', 'ready', 'failed')),
    last_error  TEXT    NOT NULL DEFAULT '',

    -- Stage 0 and stage 1 results. All nullable: they are unknown until the
    -- probe runs, and NULL says "not yet" where 0 would claim "none".
    page_count            INTEGER,
    encrypted             INTEGER CHECK (encrypted IN (0, 1)),
    tagged                INTEGER CHECK (tagged IN (0, 1)),
    has_text_layer        INTEGER CHECK (has_text_layer IN (0, 1)),
    -- Median extracted characters (runes, not bytes) on a content page. A scan
    -- yields ~0, which is what selects between the free extraction path and one
    -- that costs a vision call per page.
    median_chars_per_page INTEGER,
    -- Page range holding actual content, excluding front matter and back cover.
    content_start_page    INTEGER,
    content_end_page      INTEGER,

    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    probed_at   INTEGER
) STRICT;

-- The same bytes attached to the same device twice is the same document. This is
-- what makes an accidental double upload a no-op instead of a duplicate, and it
-- is the constraint the upload handler relies on to be idempotent.
CREATE UNIQUE INDEX documents_device_blob_idx ON documents(device_id, blob_sha256);
CREATE INDEX documents_device_idx ON documents(device_id);
CREATE INDEX documents_blob_idx   ON documents(blob_sha256);
CREATE INDEX documents_state_idx  ON documents(state);

-- Per-page facts recorded by the probe. One row per page of the original.
--
-- This is the evidence behind the language map: it holds what each individual
-- signal saw on that page, so a disagreement can be shown to the user rather
-- than averaged away. See docs/design/language-detection.md.
CREATE TABLE doc_pages (
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
    lang         TEXT    NOT NULL DEFAULT '',
    lang_source  TEXT    NOT NULL DEFAULT ''
                         CHECK (lang_source IN ('', 'page-tag', 'index', 'script', 'detector', 'reconciled')),

    PRIMARY KEY (document_id, page_no)
) STRICT;

CREATE INDEX doc_pages_lang_idx ON doc_pages(document_id, lang);

-- Language runs within a document: a contiguous span of pages in one language.
--
-- Every signal writes its own rows for the same document, so the map is not one
-- opinion but several, each attributed. The reconciled view is source
-- 'reconciled'; the others are kept because "this manual also contains FR, IT,
-- ES..." must be answerable without re-probing, and because a conflict has to
-- remain inspectable after the fact.
CREATE TABLE doc_langs (
    document_id TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,

    -- Which signal produced this run. Part of the key, so each signal's view
    -- coexists with the others.
    source      TEXT    NOT NULL
                        CHECK (source IN ('page-tag', 'index', 'script', 'detector', 'reconciled')),

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

CREATE INDEX doc_langs_source_idx ON doc_langs(document_id, source);
CREATE INDEX doc_langs_lang_idx   ON doc_langs(document_id, lang);

-- +goose Down
DROP TABLE doc_langs;
DROP TABLE doc_pages;
DROP TABLE documents;
DROP TABLE devices;
DROP TABLE locations;
