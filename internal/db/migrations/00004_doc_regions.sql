-- M1: store a language that occupies part of a page.
--
-- internal/doc already computes regions (internal/doc/regions.go) and nothing
-- stores them, so the parallel-columns manual's five languages do not survive a
-- restart. This is the table that lets them. The contract, and what it
-- deliberately leaves unsolved, is docs/design/regions.md.
--
-- Additive: a new table only, no rebuild. 00002 and 00003 are committed, and
-- editing either would diverge from any database already created from it — the
-- same reason 00003 exists as its own file rather than as a patch to 00002.
-- doc_pages stays exactly as it is: a page genuinely has one dominant script, one
-- printed folio and one tag position, and those are per page. What is not per page
-- is language, and that is what moves here. Widening doc_pages instead would have
-- made every existing column ambiguous about which part of the page it describes.

-- +goose Up

-- One language's territory on a page: the whole page where a manual runs its
-- languages in sequence, a box where it runs them in parallel columns.
--
-- A whole-page region has no box in the sense that x0 = 0 and x1 = the page
-- width. That is the compatibility stance rather than a shortcut: a caller
-- clipping text to the box gets the whole page, so a page-at-a-time reader needs
-- no special case and no null check for "this one has no box".
CREATE TABLE doc_regions (
    document_id TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,

    -- Which signal named this region, and '' when none could.
    --
    -- '' is a real, reportable state and not a defect to reject. The column
    -- manual's page 68 is a page of service addresses in six languages: no
    -- printed tag, no usable alphabet, nothing that can name it honestly. Saying
    -- "nothing established" beats guessing, and saying it with '' rather than NULL
    -- means no caller has to null-check a column that is never meaningfully
    -- absent. The other values are doc.Source, all six of them, including
    -- 'repertoire' — which 00003 exists because 00002 omitted.
    source      TEXT    NOT NULL
                        CHECK (source IN ('', 'page-tag', 'index', 'script', 'repertoire', 'detector', 'reconciled')),

    -- 1-based page number in the original PDF.
    page        INTEGER NOT NULL CHECK (page >= 1),

    -- The region's horizontal bounds, INTEGER although doc.Region carries
    -- float64. Three reasons, in order of how much they cost to get wrong:
    --
    -- 1. A float in a primary key requires two probes to produce bit-identical
    --    floats before the upsert converges. Anything else inserts a second row a
    --    hair to the left of the first and reports the page twice.
    -- 2. The coordinate space is poppler's, 1.5x the PDF's points (108 dpi against
    --    72). One unit is therefore exactly one pixel of a `pdftoppm -r 108`
    --    raster — which is how a stored box is checked against a render at all —
    --    and sub-pixel precision says nothing about where a column begins.
    -- 3. Rounding cannot merge two columns. Measured over all 169 columns of
    --    testdata/fixtures/thomas-drybox-amfibia.json: the two closest x0 values
    --    on any one page are 143 units apart, and the narrowest column in the
    --    document is 122 units wide. Both are three orders of magnitude clear of
    --    the half-unit that rounding can move an edge. That fixture records its
    --    own ground-truth edges as integers for the same reason.
    --
    -- Round, do not truncate: truncation biases every edge left by up to a unit,
    -- and it biases x1 and x0 in the same direction, so a width stays right by
    -- luck rather than by construction.
    x0          INTEGER NOT NULL CHECK (x0 >= 0),
    x1          INTEGER NOT NULL CHECK (x1 >= 0),

    -- code is the label as the document prints it, which need not be a valid tag:
    -- real manuals print D, RUS, UA and KAZ. lang is that normalised to BCP-47,
    -- empty when it could not be. Keeping both is what makes an unrecognised label
    -- reportable instead of dropped — the same pairing as doc_langs.
    code        TEXT    NOT NULL DEFAULT '',
    lang        TEXT    NOT NULL DEFAULT '',

    -- Characters (runes, not bytes) of the text inside the box, and how many text
    -- runs it holds.
    --
    -- Characters are the unit of size that replaces pages, because a page holding
    -- three languages cannot be a unit of anything — "48 of 560 pages" was always
    -- a proxy. Runes rather than bytes because half a real manual is Cyrillic,
    -- Greek, Hebrew, Arabic or CJK, where the same amount of writing runs about a
    -- third more bytes. Runs are the density evidence: a region of five runs is
    -- page furniture, whatever its area.
    chars       INTEGER NOT NULL DEFAULT 0 CHECK (chars >= 0),
    runs        INTEGER NOT NULL DEFAULT 0 CHECK (runs >= 0),

    -- Set when the region's printed tag and its alphabet disagreed. The note says
    -- how, in checkable terms. Surfacing the disagreement is the requirement;
    -- resolving it silently is what the design forbids.
    conflict    INTEGER NOT NULL DEFAULT 0 CHECK (conflict IN (0, 1)),
    note        TEXT    NOT NULL DEFAULT '',

    created_at  INTEGER NOT NULL,

    -- Key on GEOMETRY, not on the label. x0 is what tells the German left column
    -- from the German right column; the code cannot, because under doc_langs' key
    -- those two collide on the same page with the same code and the same source,
    -- which is the concrete breakage this table exists to fix (regions.md, and
    -- 00002:205 for the key that breaks).
    --
    -- Natural, not surrogate, for the reason 00002's header sets out at length: a
    -- probe job can run twice, so a second probe must converge on the same rows.
    -- A ULID here would make it insert a parallel set instead.
    PRIMARY KEY (document_id, source, page, x0),

    CHECK (x1 >= x0)
) STRICT;

-- What the reader asks for: a page's regions, and a language's territory across
-- the document.
CREATE INDEX doc_regions_page_idx ON doc_regions(document_id, page);
CREATE INDEX doc_regions_lang_idx ON doc_regions(document_id, lang);

-- WHY source IS IN THE KEY, AND WHY THAT MAKES THE WHOLESALE REPLACE
-- LOAD-BEARING RATHER THAN INCIDENTAL. Do not "optimise" the delete away.
--
-- Unlike doc_langs, which stores every signal's separate view of the same
-- document side by side, internal/doc produces ONE resolved set of regions in
-- which source merely records which signal named each one. That attribution is
-- not stable across probes: a column named by its alphabet on one run can be
-- named by its printed tag on the next, because the tag reader's vocabulary comes
-- from the document's own contents table and that parse can improve. Same
-- document, same page, same x0, same column — different source.
--
-- With source in the key, that region is a DIFFERENT row. An upsert alone would
-- therefore leave the old row behind at the same x0 and the page would report two
-- regions where the document has one. So SaveProbe deletes a document's regions
-- and rewrites them inside one transaction; the upsert stays as belt and braces
-- for a retry within a single probe.
--
-- The alternative was to drop source from the key and carry it as a plain column.
-- That was rejected because geometry-plus-source is what regions.md specifies and
-- because it would silently discard the case where two signals genuinely describe
-- the same box — but the cost is this delete, and it is not optional.

-- +goose Down
DROP TABLE doc_regions;
