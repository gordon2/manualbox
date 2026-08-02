-- M1: make a household's manuals searchable, which is the first problem README
-- claims this project solves -- "the paper pile is unsearchable, and you need the
-- router manual at exactly the moment the internet is down".
--
-- 00005 stores the blocks. This indexes them. The contract is
-- docs/design/search.md; every number below was measured against both real
-- manuals, 3,122 blocks converted for de, ru, ja, th and he, and the harness that
-- produced them is in that document's own history.
--
-- Additive: one virtual table, three triggers, and a rebuild. No existing table is
-- touched, for the reason 00004 and 00005 give -- 00002 through 00005 are
-- committed and editing any of them would diverge from a database already created
-- from it.
--
--
-- ONE: EXTERNAL CONTENT, NOT A STANDALONE INDEX.
--
-- content='doc_blocks' means FTS5 stores only the index and reads the text back out
-- of doc_blocks when a query needs it. A standalone table stores its own copy of
-- every block's text, which is the simpler thing and doubles the text on disk.
-- Measured over the 3,122-block corpus, all with 'optimize' then VACUUM run and
-- the whole database file compared against one holding doc_blocks alone (626,688
-- bytes):
--
--   standalone unicode61   1,388,544 total    +761,856 index
--   external   unicode61     897,024 total    +270,336 index
--   standalone trigram     1,998,848 total  +1,372,160 index
--   external   trigram      1,507,328 total    +880,640 index
--
-- The duplicated text is the 491,520-byte difference in both pairs, which is 56%
-- more index for the trigram pair and nothing gained. So: external content.
--
-- WHAT EXTERNAL CONTENT COSTS, AND WHY IT IS PAID IN TRIGGERS. An external content
-- table is NOT maintained by SQLite. Nothing updates it when doc_blocks changes,
-- and a delete has to be told the OLD text, because FTS5 no longer has a copy to
-- work out which terms to remove.
--
-- Triggers rather than Go statements next to each write, and this is the decision
-- the correctness of the whole feature rests on. There are three paths that change
-- doc_blocks and only two of them are visible in Go:
--
--   1. registry.saveBlocks deletes a document's blocks and reinserts them -- the
--      wholesale replace 00005 explains at length.
--   2. The same function's upsert updates a block in place when the key is unchanged.
--   3. documents ON DELETE CASCADE removes every block of a deleted document, and
--      NO GO CODE RUNS AT ALL. Nothing calls DeleteDocBlocks and no handler is
--      involved: deleting a device removes its documents, which removes their
--      blocks, entirely inside SQLite. A rule that had to be remembered in Go on
--      that path would be a rule nobody can see in the source they are editing.
--
-- Triggers cover all three by construction, and they run inside whatever
-- transaction the write is already in, which is exactly what SaveConversion needs:
-- the blocks, the index and the document's 'ready' state commit together or not at
-- all.
--
-- That the cascade fires them was MEASURED rather than assumed, because SQLite's
-- own documentation makes trigger firing on a foreign-key action conditional on
-- the recursive_triggers setting, and manualbox does not set it. With
-- foreign_keys(1) and recursive_triggers off -- the pragmas internal/db actually
-- opens with -- deleting the document removed its rows from the index and FTS5's
-- 'integrity-check' passed.
--
-- WHAT GOES WRONG WITHOUT THE DELETE TRIGGER IS NOT WHAT IT LOOKS LIKE, and the
-- first version of this comment had it wrong. It is NOT that a deleted manual stays
-- findable: every search joins the index to doc_blocks, so an index entry whose row
-- is gone joins to nothing and vanishes from the results by accident. Measured that
-- way round, and it made the obvious control assertion pass over a corrupt index.
--
-- The real failure is worse and needs one more step. SQLite gives a new row
-- max(rowid)+1, so deleting the highest block frees a rowid the next insert takes.
-- The stale entry then points at a REAL row of a DIFFERENT document, and searching
-- for a word from the deleted manual returns a confident hit naming another manual,
-- another page, and text that does not contain the word. A wrong citation rather
-- than a missing one, which is the failure this project can least afford, since a
-- citation is what extraction will hang a maintenance schedule on.
--
-- Measured end to end, including the reuse: dropping the delete trigger makes
-- 'integrity-check' report "database disk image is malformed" immediately, and the
-- next insert makes a search for the deleted manual's word answer with the
-- unrelated document's text. revertCheckTheDeleteTrigger in internal/db is that
-- run, kept as a test.
--
-- ROWID STABILITY, THE ONE RISK THIS SHAPE CARRIES. An external content index joins
-- on doc_blocks' rowid, and doc_blocks' primary key is composite, so its rowid is
-- not an INTEGER PRIMARY KEY alias -- the kind of rowid SQLite does not promise to
-- preserve across a VACUUM. Nothing in manualbox runs VACUUM (grepped, not
-- assumed), and a VACUUM of a 3,122-block database with holes punched in its rowid
-- sequence was measured to leave max(rowid) and every hit unchanged with
-- 'integrity-check' passing. It is still not a promise: if a database is ever
-- vacuumed by hand and search starts returning the wrong block, the repair is
-- `INSERT INTO doc_blocks_fts(doc_blocks_fts) VALUES ('rebuild')`, which is the
-- same statement this migration ends with.
--
--
-- TWO: THE TOKENISER, WHICH IS THE ONE DECISION THAT COULD NOT BE REASONED OUT.
--
-- unicode61, FTS5's default, splits on whitespace and punctuation. That is right
-- for German, Russian, Ukrainian and Greek and it is USELESS for Chinese, Japanese
-- and Thai, which do not put spaces between words. trigram indexes every run of
-- three characters and therefore matches substrings, which works for those scripts
-- and costs a larger index and a three-character minimum.
--
-- Measured, not chosen from that description. Real words from each script, against
-- the real corpus:
--
--   query                             unicode61   trigram
--   "Filter"          (de)                   21        69
--   "Saugkraft"       (de)                    7         7
--   "Gerat"           (de, folded)           71        96
--   Russian "filter"                         31        96
--   Japanese "instruction manual"             0         6
--   Thai "manual"                             0         6
--
-- unicode61 finds NOTHING in Japanese and NOTHING in Thai. It is not degraded
-- there, it is absent: a whole CJK or Thai run is one token, so it matches only a
-- query that happens to be the entire run. (The two-character Japanese word for
-- "power" scores 2 hits under unicode61 against 27 real occurrences, and those 2
-- are where punctuation happened to isolate it. That is the shape of the failure.)
--
-- trigram finds a real word in all five scripts, and it costs 880,640 bytes of
-- index against unicode61's 270,336 -- 3.3x, or 2.40x the size of a database
-- holding the blocks alone, which is 195 bytes of index per stored block. The
-- higher hit counts are substring matches: "Filter" also finds "Luftfilter" and
-- "Filterdeckel", which in German is closer to what a person meant than
-- token-exact matching is.
--
-- SO: trigram, ONE INDEX FOR EVERY SCRIPT, with one named limitation.
--
-- THE LIMITATION: A QUERY SHORTER THAN THREE CHARACTERS MATCHES NOTHING. Not fewer
-- results -- none, because there is no such token in the index. Measured: the
-- two-character Japanese words for "power" and "product" occur in 27 and 24 stored
-- blocks and the index finds 0 of each. Two characters is an ordinary word in
-- Chinese and Japanese, so this is a real hole in exactly the scripts trigram was
-- chosen for. queries/search.sql fills it by scanning instead for a query that
-- short, measured at 1.9 ms over this corpus, and the API reports which path
-- answered.
--
-- WHAT WAS REJECTED, AND WHAT IT WOULD HAVE COST. Two indexes -- unicode61 for the
-- space-separated scripts and trigram for the rest -- would give token-exact
-- precision to the majority of languages and still serve CJK. It was rejected: it
-- costs 1,150,976 bytes of index rather than 880,640, both must be maintained by
-- their own triggers, and every query has to guess from the query's own characters
-- which index can answer it. A query mixing a German word and a Japanese one then
-- has no right answer. One index that is somewhat blunt everywhere beats two that
-- are sharp until a household is multilingual, which every household with this
-- kind of manual already is.
--
--
-- THREE: DIACRITICS ARE FOLDED, AND THE STATED COST TURNED OUT NOT TO EXIST.
--
-- remove_diacritics is what lets a German household on any keyboard find "Gerat"
-- and get "Gerat". The worry was that it also folds Cyrillic and Greek, which would
-- be a real cost on this corpus -- half of it is not Latin.
--
-- MEASURED: IT FOLDS LATIN AND NOTHING ELSE. Stored against queried, across all
-- three modes of unicode61 and both modes of trigram:
--
--   German "Gerat" for stored "Gerat"        folded when on, missed when off
--   Russian "esche" for stored "eschyo"      NEVER folded (yo stays yo)
--   Ukrainian "Kyiv" with i for yi           NEVER folded
--   Greek "odigies" for stored "odigies"     NEVER folded (tonos stays)
--   Hebrew without niqqud for stored with    NEVER folded
--
-- FTS5's folding table covers precomposed Latin and does not reach Cyrillic, Greek
-- or Hebrew, so the cost this decision was weighed against is not there. It is
-- turned ON, and it has to be said explicitly here: unicode61 folds by default but
-- TRIGRAM DOES NOT, and with it off "Gerat" finds 0 of the 96 blocks holding
-- "Gerat". The index is 4,096 bytes SMALLER with folding on.
--
-- WHAT IS NOT FIXED BY ANY OF THIS, and it is worth knowing before someone tests
-- with Hebrew. The stored Hebrew of the sequential manual is in VISUAL order --
-- internal/doc reads the runs a right-to-left page paints, and the PDF paints them
-- reversed. The word for "manual" is stored as its own reverse, so it is findable
-- by a query typed backwards (5 blocks) and not by one a Hebrew speaker would type
-- (0 blocks). No tokeniser touches that; it is upstream of the index, in
-- extraction, and it belongs to internal/doc rather than here.

-- +goose Up

-- The index over every stored block's text. One indexed column, because a block's
-- other columns are how a hit is described rather than what is searched: matching
-- on the language code or the kind would let a query for "table" find every table.
CREATE VIRTUAL TABLE doc_blocks_fts USING fts5(
    text,
    content='doc_blocks',
    content_rowid='rowid',
    tokenize='trigram remove_diacritics 1'
);

-- The three triggers that keep it correct. See the header: these are the whole
-- maintenance story, including for the ON DELETE CASCADE from documents, which no
-- Go code observes.
--
-- Each needs goose's StatementBegin/StatementEnd, because a trigger body contains
-- semicolons and goose otherwise cuts the statement at the first one.

-- +goose StatementBegin
CREATE TRIGGER doc_blocks_fts_insert AFTER INSERT ON doc_blocks BEGIN
    INSERT INTO doc_blocks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
-- +goose StatementEnd

-- The 'delete' command has to be given the OLD text, not just the rowid: FTS5 has
-- no copy of it to work out which terms to remove, which is exactly what external
-- content means.
-- +goose StatementBegin
CREATE TRIGGER doc_blocks_fts_delete AFTER DELETE ON doc_blocks BEGIN
    INSERT INTO doc_blocks_fts(doc_blocks_fts, rowid, text)
    VALUES ('delete', old.rowid, old.text);
END;
-- +goose StatementEnd

-- An update is a delete of the old terms and an insert of the new ones. It fires on
-- the upsert path in registry.saveBlocks, which updates a block in place when a
-- re-conversion produces the same key with different text -- a paragraph promoted
-- to a heading, or the same paragraph folded differently.
-- +goose StatementBegin
CREATE TRIGGER doc_blocks_fts_update AFTER UPDATE ON doc_blocks BEGIN
    INSERT INTO doc_blocks_fts(doc_blocks_fts, rowid, text)
    VALUES ('delete', old.rowid, old.text);
    INSERT INTO doc_blocks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
-- +goose StatementEnd

-- Blocks that are already stored. 00005 shipped, so a database reaching this
-- migration can already hold a converted manual, and the triggers above only see
-- what happens next. Without this, an existing household would have to re-approve
-- every document to become searchable. 'rebuild' costs one pass over doc_blocks and
-- is a no-op on a fresh database.
INSERT INTO doc_blocks_fts(doc_blocks_fts) VALUES ('rebuild');

-- +goose Down
DROP TRIGGER doc_blocks_fts_update;
DROP TRIGGER doc_blocks_fts_delete;
DROP TRIGGER doc_blocks_fts_insert;
DROP TABLE doc_blocks_fts;
