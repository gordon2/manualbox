-- M1: store what a conversion produced -- the readable blocks of a document, and
-- the pictures printed in it.
--
-- internal/doc computes both and stores neither: blocks.go turns a region into
-- headings, paragraphs, list items and table cells, figures.go finds the
-- illustrations and renders each one. The contract, and the seven things it
-- deliberately does not solve, is docs/design/conversion.md.
--
-- Additive: two new tables, no rebuild. 00002, 00003 and 00004 are committed, and
-- editing any of them would diverge from a database already created from it --
-- the same reason 00003 exists as its own file rather than as a patch to 00002.
--
-- WHY TWO TABLES AND NOT ONE. BlockFigure is a declared kind in blocks.go and
-- nothing emits it, which invites folding figures into doc_blocks as rows of that
-- kind. They are not the same thing and the difference is in the key. A block
-- belongs to a REGION -- to one language's territory on a page -- and is keyed by
-- the region's left edge. A figure belongs to the PAGE: conversion.md settles that
-- "a picture that belongs to no language belongs to every language", so a figure
-- has no region and no language, and giving it a region_x0 would be inventing the
-- one fact the contract says it does not have. Storing them together would mean
-- either a nullable region_x0 in a primary key, which SQLite treats as never
-- conflicting and so would break the upsert outright, or a sentinel that reads as
-- a real column position.

-- +goose Up

-- One piece of readable content: a heading, a paragraph, a list item, a table
-- cell. In reading order within its region.
CREATE TABLE doc_blocks (
    document_id   TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,

    -- 1-based page number in the original PDF, the same number doc_regions and
    -- doc_pages count in. Not the folio the paper prints, which is a different
    -- thing and lives in doc_pages.printed_folio.
    page          INTEGER NOT NULL CHECK (page >= 1),

    -- The left edge of the region this block was read from, and the reason it is
    -- INTEGER although doc.Block carries float64 is 00004's reason, unchanged:
    --
    -- 1. A float in a primary key requires two conversions to produce
    --    bit-identical floats before the upsert converges. Anything else inserts
    --    a parallel set of blocks a hair to the left of the first, and the page
    --    reads twice.
    -- 2. It must be the SAME NUMBER as doc_regions.x0 or a block cannot be traced
    --    back to the language territory it came from. doc_regions stores a
    --    rounded integer; storing a float here would put the join one rounding
    --    apart from the only row it can join to. registry.roundCoord is used for
    --    both, so this is one function's output stored in two places rather than
    --    two functions that agree today.
    -- 3. Rounding cannot merge two regions: measured over all 169 columns of
    --    testdata/fixtures/thomas-drybox-amfibia.json, the two closest x0 values
    --    on any one page are 143 units apart. Half a unit is three orders of
    --    magnitude clear of that.
    --
    -- 0 for a whole-page region, which is what regions.md rule 3 stores for a page
    -- whose columns are all one language.
    region_x0     INTEGER NOT NULL CHECK (region_x0 >= 0),

    -- The block's position within its region, from 0, in reading order.
    --
    -- Named idx rather than index because INDEX is a SQLite keyword: it parses
    -- when quoted and nowhere else, and every query touching it would have to
    -- remember the quotes.
    idx           INTEGER NOT NULL CHECK (idx >= 0),

    -- What the block is. A string rather than an integer for the reason
    -- doc.BlockKind is one: "heading" survives a schema change and 1 does not.
    --
    -- All five of blocks.go's kinds are listed, including 'figure', which nothing
    -- emits yet -- it is declared there precisely so the vocabulary a database
    -- column and a reader see does not have to change when that work lands.
    -- Omitting it here would put the schema change back exactly where the Go
    -- constant was written to avoid it.
    kind          TEXT    NOT NULL
                          CHECK (kind IN ('heading', 'paragraph', 'list-item', 'table', 'figure')),

    -- The heading level, and 0 for anything that is not a heading.
    --
    -- Only 1 and 2 are reachable today and the CHECK is deliberately wider than
    -- that. blocks.go derives the level from one region's own body face and says
    -- so: a document-wide outline needs every region's sizes ranked together, and
    -- the columns manual has four heading sizes, so level 3 and 4 are real and
    -- merely not yet computed. A CHECK of (0, 1, 2) would make that later pass a
    -- migration.
    level         INTEGER NOT NULL DEFAULT 0 CHECK (level >= 0),

    -- The content, with the printed line breaks already removed: a break at the
    -- original measure is a property of the paper's column width, not of the text.
    text          TEXT    NOT NULL DEFAULT '',

    -- The region's language, empty where none was established -- '' rather than
    -- NULL for doc_regions' reason, that a caller must not have to null-check a
    -- column which is never meaningfully absent. Denormalised from doc_regions on
    -- purpose: a block is self-describing once it leaves the page, which is the
    -- state extraction and search will see it in, and it is what lets the reader
    -- set dir="rtl" from the block alone.
    lang          TEXT    NOT NULL DEFAULT '',

    -- The block's OWN bounding box, not the region's, in the space
    -- doc.ExtractRuns reports: poppler's, 1.5x the PDF's points, where one unit is
    -- one pixel of a pdftoppm -r 108 raster.
    --
    -- REAL here where region_x0 above is INTEGER, and the difference is entirely
    -- the primary key. Rounding region_x0 buys convergence and a joinable value;
    -- rounding these buys nothing, because nothing keys on them and nothing joins
    -- to them. What it would cost is the ability to compare a block's box against
    -- a doc_figures rect, which is REAL for the same reason, without one side
    -- having been quantised first. So they are stored exactly as internal/doc
    -- reported them.
    --
    -- Not constrained to be non-negative. A run parked off the left edge of the
    -- page is furniture rather than an error, and clamping it here would move a
    -- box that a caller is about to draw on a render.
    x0            REAL    NOT NULL,
    x1            REAL    NOT NULL,
    y0            REAL    NOT NULL,
    y1            REAL    NOT NULL,

    -- How many printed lines the block was folded from, and its rune count.
    --
    -- Runes rather than bytes for the reason doc_regions.chars gives: half a real
    -- manual is Cyrillic, Greek, Hebrew, Arabic or CJK, where the same amount of
    -- writing runs about a third more bytes. Lines is the folding evidence -- a
    -- 40-line "paragraph" is a paragraph break that was missed, and conversion.md
    -- records that some are.
    lines         INTEGER NOT NULL DEFAULT 0 CHECK (lines >= 0),
    chars         INTEGER NOT NULL DEFAULT 0 CHECK (chars >= 0),

    -- Why this block is the kind it is, in checkable terms, and for a table cell
    -- its place in the grid. Same stance as doc_regions.note: the evidence is
    -- countable and a reader can hold it against the page.
    note          TEXT    NOT NULL DEFAULT '',

    created_at    INTEGER NOT NULL,

    -- The natural key conversion.md specifies: the document, the page, the
    -- region's left edge and the block's index within it. Natural and not a
    -- surrogate for 00002's reason -- a job handler can run twice, and a ULID here
    -- would make the second conversion insert a parallel set instead of converging
    -- on the first.
    --
    -- It is also the citation extraction needs. "Paragraph 4 of the German region
    -- of page 62" is a reference that survives a re-convert, which is what
    -- ingest.md asks for when it says extraction must cite a paragraph rather than
    -- a document.
    --
    -- NOTE WHAT IS NOT IN THE KEY: kind and lang. Unlike doc_regions, which puts
    -- source in its key, nothing about a block's classification identifies it. A
    -- paragraph that a better heading rule promotes to a heading is the SAME block
    -- at the same index, so it must update in place rather than become a second
    -- row. That is the one way this key is simpler than doc_regions', and it is
    -- deliberate.
    PRIMARY KEY (document_id, page, region_x0, idx),

    CHECK (x1 >= x0),
    CHECK (y1 >= y0)
) STRICT;

-- What the reader asks for: a document's blocks in order, and one language's
-- content across the document. The second is the funnel's own query -- a household
-- that reads German asks for the German, not for the pages.
CREATE INDEX doc_blocks_page_idx ON doc_blocks(document_id, page);
CREATE INDEX doc_blocks_lang_idx ON doc_blocks(document_id, lang);

-- One illustration printed on a page: where it is, the evidence that it is a
-- picture, and the digest of the PNG it was rendered to.
--
-- Every illustration in both fixtures is VECTOR, which conversion.md measures:
-- pdfimages -- the obvious tool -- yields zero illustrations across all 628 pages
-- of both manuals, and what it does yield is 1,358 gradient-mesh slivers, a corner
-- logo and some CE marks. So a figure is found from what the page draws and its
-- bytes come from rendering the crop, and that is why this table stores a digest
-- rather than pointing at an embedded image.
CREATE TABLE doc_figures (
    document_id   TEXT    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,

    page          INTEGER NOT NULL CHECK (page >= 1),

    -- The figure's position in the page's reading order, from 0, down then across.
    -- Not a document-wide figure number: internal/doc has one page in view at a
    -- time and numbering across pages is a later caller's.
    idx           INTEGER NOT NULL CHECK (idx >= 0),

    -- The rectangle, in the same 1.5-scaled space as doc_blocks' box, and REAL for
    -- the same reason: it is not in the key, so there is nothing to gain by
    -- quantising it and a caller placing a figure in a column's reading order
    -- compares it against a block box that was not quantised either.
    x0            REAL    NOT NULL,
    y0            REAL    NOT NULL,
    x1            REAL    NOT NULL,
    y1            REAL    NOT NULL,

    -- How many drawn shapes the figure holds: the shape guard's evidence, kept
    -- rather than reduced to the verdict, so a page that was rejected can be shown
    -- to have been rejected for the right reason.
    ink           INTEGER NOT NULL DEFAULT 0 CHECK (ink >= 0),

    -- How much of the figure's area is covered by text: the text guard's evidence,
    -- and the only thing separating a picture from a framed illustration grid or a
    -- table. A stored figure has passed the guard at 0.15.
    --
    -- Checked as >= 0 and deliberately NOT as <= 1. doc.textFraction sums the area
    -- of every run overlapping the box without subtracting the overlaps between
    -- runs, so two runs sharing a line can exceed the box's own area. An upper
    -- bound of 1 would be asserting something the arithmetic does not promise, and
    -- it would fail in a background job rather than anywhere a person is looking.
    text_fraction REAL    NOT NULL DEFAULT 0 CHECK (text_fraction >= 0),

    -- What the render was: 216 dpi today, twice the 108 the geometry is in.
    -- Recorded rather than assumed constant, because a stored figure outlives the
    -- constant that produced it and a caller scaling the pixels back onto the page
    -- needs to know which one it got.
    dpi           INTEGER NOT NULL CHECK (dpi > 0),

    -- The PNG's pixel size, read back out of its IHDR rather than computed, so a
    -- caller comparing it against the rect is comparing what poppler did against
    -- what was asked for.
    pixel_width   INTEGER NOT NULL CHECK (pixel_width > 0),
    pixel_height  INTEGER NOT NULL CHECK (pixel_height > 0),

    -- THE PNG BYTES ARE NOT HERE. They go to the content-addressed blob store,
    -- whose filename IS the SHA-256, and this column is that name -- referencing
    -- blobs(sha256) exactly as documents.blob_sha256 does.
    --
    -- Two reasons, and the second is the one that decides it. A row per figure of
    -- a few hundred KB would put tens of MB of image into a database that is
    -- opened, WAL-checkpointed and backed up as one file; the columns manual's
    -- largest single figure is 353 KB. And content addressing already does the
    -- deduplication this table would otherwise need: the same diagram printed in
    -- five languages' sections renders to the same bytes and is stored once,
    -- because doc.renderFigure digests exactly what poppler wrote rather than
    -- re-encoding it.
    --
    -- No ON DELETE clause, matching documents.blob_sha256: a blob outlives the
    -- rows that point at it and is collected by counting references, because two
    -- documents can legitimately share one.
    --
    -- The length CHECK is what stops an unrendered figure being stored. A figure
    -- found by doc.FindFigures carries no bytes and an empty digest, and '' would
    -- otherwise have to be a blobs row for the FK to hold.
    blob_sha256   TEXT    NOT NULL REFERENCES blobs(sha256)
                          CHECK (length(blob_sha256) = 64),

    created_at    INTEGER NOT NULL,

    -- Natural, for doc_blocks' reason. No region and no language in the key: a
    -- figure belongs to the page, because conversion.md settles that a picture
    -- belonging to no language belongs to every language. A reader scoped to one
    -- language selects the figures of the pages that language occupies; it does
    -- not select figures BY language, and there is no column here to let it try.
    PRIMARY KEY (document_id, page, idx),

    CHECK (x1 >= x0),
    CHECK (y1 >= y0)
) STRICT;

CREATE INDEX doc_figures_page_idx ON doc_figures(document_id, page);
CREATE INDEX doc_figures_blob_idx ON doc_figures(blob_sha256);

-- WHY THE WHOLESALE REPLACE IS LOAD-BEARING HERE TOO, FOR A DIFFERENT REASON THAN
-- 00004'S. Do not "optimise" the delete away.
--
-- doc_regions needs its delete because source is in its key and a region's
-- attribution changes between probes, so an upsert leaves a superseded row at the
-- same x0. Neither key here carries anything that unstable -- that is what the
-- note above the doc_blocks primary key is about. The reason is simpler and it is
-- not weaker: A RE-CONVERSION CAN PRODUCE FEWER BLOCKS THAN THE ONE BEFORE IT.
--
-- It routinely will. Every threshold in blocks.go is one measurement away from
-- moving, and most of them merge: paragraphGapFactor folds two paragraphs into
-- one, the heading share cut turns two headings into one, and conversion.md
-- already names four unresolved cases where the count should fall. Indices are
-- consecutive from 0 within a region, so a region that converted to 12 blocks and
-- now converts to 9 leaves rows at idx 9, 10 and 11 -- a reader shows three
-- paragraphs of the previous run's text, in order, indistinguishable from content.
-- The same holds for a figure the trim rule now rejects.
--
-- So SaveConversion deletes a document's blocks and figures and rewrites them
-- inside one transaction. The upserts stay as belt and braces for a retry within a
-- single conversion.

-- +goose Down
DROP TABLE doc_figures;
DROP TABLE doc_blocks;
