package registry

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/doc"
)

// trigramMin is the shortest query the index can answer, and it is a property of
// the tokeniser rather than a policy: a trigram index holds no token shorter than
// three characters, so a shorter query matches nothing at all. See
// 00006_block_search.sql for what that costs and why it is still the right
// tokeniser.
const trigramMin = 3

// Search modes, reported on every result so a caller can tell which question was
// actually answered.
const (
	// SearchIndex is the FTS5 index: bm25 ranking, substring matching within a
	// block, every script the corpus holds.
	SearchIndex = "index"
	// SearchSubstring is the scan that answers a query the index cannot represent
	// -- one shorter than three characters in any of its words. Case folding there
	// is SQLite's own lower(), which is ASCII only.
	SearchSubstring = "substring"
)

// Default and maximum result counts. The default is a screenful; the cap is what
// stops a client asking for the whole corpus one query at a time, since every hit
// carries a snippet and a device name.
const (
	DefaultSearchLimit = 25
	MaxSearchLimit     = 100
)

// SearchQuery is one search: what to look for, and how much of the household to
// look in.
type SearchQuery struct {
	// Text is what the user typed, unmodified. Turning it into an FTS5 expression
	// is [Service.Search]'s business and deliberately not a caller's: an API that
	// accepted FTS5 syntax would make every quote, asterisk and colon in an
	// ordinary query a syntax error.
	Text string
	// DocumentID narrows the search to one manual. Empty searches every one of
	// them, which is the question README poses -- "which manual says X".
	DocumentID string
	// Limit caps the hits. Zero means [DefaultSearchLimit]; anything above
	// [MaxSearchLimit] is clamped to it rather than rejected, because a client
	// asking for too much wants as much as it can have.
	Limit int
}

// Hit is one match: which manual, which page, which language, and enough text to
// recognise it.
//
// Page, RegionX0 and Index together are the block's natural key, the same one
// doc_blocks stores and conversion.md specifies as a citation -- so a hit can be
// deep-linked to the exact paragraph it came from and will still point there after
// a re-conversion.
type Hit struct {
	DocumentID string `json:"documentId"`
	// Filename and DeviceName are what a household recognises. "Page 47" without
	// them answers a different, useless question.
	Filename   string `json:"filename,omitempty"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	// State is the document's pipeline state, so a hit from a manual that is
	// mid-re-conversion is visible as such rather than looking stale.
	State string `json:"state"`

	Page     int `json:"page"`
	RegionX0 int `json:"regionX0"`
	Index    int `json:"index"`

	Kind  string `json:"kind"`
	Level int    `json:"level,omitempty"`
	// Lang is the block's language and Name that for a person to read, the same
	// pairing [Block] uses: the UI shows "Japanese", not "ja".
	Lang string `json:"lang,omitempty"`
	Name string `json:"name,omitempty"`

	// Snippet is the text around the match, about 64 characters of it. Chars is
	// the whole block's rune count, so a caller can tell a snippet from a complete
	// block and fetch the rest through the conversion endpoint.
	Snippet string `json:"snippet"`
	Chars   int    `json:"chars"`

	// BM25 is what FTS5 scored the match, and Score is that with the heading bonus
	// applied -- the number the results are ordered by. Both are reported because
	// the bonus is a judgement: a heading names a section and is a better answer to
	// "where does it say this" than a passing mention, and anyone who disagrees can
	// see exactly how much it moved. Both are 0 in [SearchSubstring] mode, where
	// there is no index term to weigh.
	BM25  float64 `json:"bm25"`
	Score float64 `json:"score"`
}

// SearchResults are the hits plus what was actually asked.
type SearchResults struct {
	// Query is the text as typed, echoed so a client rendering a result list does
	// not have to keep its own copy in step.
	Query string `json:"query"`
	// Mode is [SearchIndex] or [SearchSubstring]. It is reported rather than
	// hidden because the two paths differ in ways a user can see: only the index
	// ranks, and only the scan can answer a one or two character query.
	Mode  string `json:"mode"`
	Limit int    `json:"limit"`
	// Truncated says the limit cut the results off, which is the difference
	// between "these are the hits" and "these are the first hits".
	Truncated bool  `json:"truncated"`
	Hits      []Hit `json:"hits"`
	// Indexed is how many blocks exist to search, and it is filled in only when
	// nothing matched. "No results" and "nothing has been converted yet" look
	// identical otherwise, and the second is not a search failure -- it is the same
	// distinction [Service.Blocks] makes between empty and absent.
	Indexed *int `json:"indexed,omitempty"`
}

// Search answers "which manual says X, and where" across every converted document
// in the household, or within one of them.
//
// # Why the query is not passed through
//
// FTS5 has an expression syntax: unquoted AND, OR, NOT, NEAR, column filters with
// a colon, prefix stars, and quoted phrases. A search box that handed a user's text
// to it directly would fail on an apostrophe-free but perfectly ordinary query like
// `filter: reinigen` and would silently reinterpret `Motor NOT laufen`. So every
// word is quoted as a phrase and the phrases are ANDed: the query means "a block
// containing all of these", which is what a person typing two words means.
//
// # Why a short word sends the whole query to the scan
//
// The index cannot answer a word shorter than three characters at all, so a query
// mixing "Filter" with "ab" would quietly become a search for "Filter" alone -- an
// answer to a question nobody asked, and indistinguishable from a correct one. The
// rule is therefore all or nothing: every word long enough goes to the index, and
// otherwise the whole query, spaces included, is one literal substring for the scan
// to look for. Mode says which happened.
func (s *Service) Search(ctx context.Context, q SearchQuery) (*SearchResults, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return nil, fmt.Errorf("%w: a search needs something to look for", ErrInvalid)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	res := &SearchResults{Query: text, Limit: limit, Mode: SearchSubstring, Hits: []Hit{}}
	rq := gen.New(s.db.Read())

	// One more than asked for, and the extra is thrown away. It is the only way to
	// tell "these are the hits" from "these are the first hits": a result set of
	// exactly the limit is ambiguous, and reporting it as truncated would put "there
	// is more" on every complete answer that happens to fill the page.
	fetch := int64(limit) + 1

	var (
		rows []gen.SearchBlocksRow
		err  error
	)
	if match, ok := matchExpression(text); ok {
		res.Mode = SearchIndex
		if q.DocumentID != "" {
			var narrowed []gen.SearchBlocksInDocumentRow
			narrowed, err = rq.SearchBlocksInDocument(ctx, gen.SearchBlocksInDocumentParams{
				Match: match, DocumentID: q.DocumentID, Limit: fetch,
			})
			rows = narrowRows(narrowed)
		} else {
			rows, err = rq.SearchBlocks(ctx, gen.SearchBlocksParams{
				Match: match, Limit: fetch,
			})
		}
	} else if q.DocumentID != "" {
		var scanned []gen.SearchBlocksSubstringInDocumentRow
		scanned, err = rq.SearchBlocksSubstringInDocument(ctx,
			gen.SearchBlocksSubstringInDocumentParams{
				Needle: text, DocumentID: q.DocumentID, Limit: fetch,
			})
		rows = substringInDocumentRows(scanned)
	} else {
		var scanned []gen.SearchBlocksSubstringRow
		scanned, err = rq.SearchBlocksSubstring(ctx, gen.SearchBlocksSubstringParams{
			Needle: text, Limit: fetch,
		})
		rows = substringRows(scanned)
	}
	if err != nil {
		return nil, fmt.Errorf("registry: search %q: %w", text, err)
	}

	res.Truncated = len(rows) > limit
	if res.Truncated {
		rows = rows[:limit]
	}
	res.Hits = hitsFrom(rows)
	if len(res.Hits) == 0 {
		n, err := rq.CountSearchableBlocks(ctx)
		if err != nil {
			return nil, fmt.Errorf("registry: count searchable blocks: %w", err)
		}
		indexed := int(n)
		res.Indexed = &indexed
	}
	return res, nil
}

// matchExpression turns a user's text into an FTS5 expression, reporting false
// when the index cannot answer it.
//
// Every word becomes a quoted phrase, which is the only FTS5 construct with no
// syntax inside it beyond the quote character itself -- and a quote is escaped by
// doubling. The phrases are ANDed rather than joined into one phrase, so "Filter
// reinigen" finds a block that says both without demanding they be adjacent.
//
// false means at least one word is shorter than [trigramMin] and the whole query
// belongs on the scan. See [Service.Search] for why one short word disqualifies the
// query rather than being dropped from it.
func matchExpression(text string) (string, bool) {
	words := strings.FieldsFunc(text, unicode.IsSpace)
	if len(words) == 0 {
		return "", false
	}
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		if utf8.RuneCountInString(w) < trigramMin {
			return "", false
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND "), true
}

// The four generated row types are structurally identical -- the queries differ in
// their WHERE clause, not in what they return -- but sqlc emits a distinct type per
// statement, so each is converted to the one the mapper reads. Written out rather
// than reached through reflection or an interface: four small functions that the
// compiler checks against the generated types are what catches a column added to
// one statement and not the others.

func narrowRows(in []gen.SearchBlocksInDocumentRow) []gen.SearchBlocksRow {
	out := make([]gen.SearchBlocksRow, 0, len(in))
	for i := range in {
		r := &in[i]
		out = append(out, gen.SearchBlocksRow(*r))
	}
	return out
}

func substringRows(in []gen.SearchBlocksSubstringRow) []gen.SearchBlocksRow {
	out := make([]gen.SearchBlocksRow, 0, len(in))
	for i := range in {
		r := &in[i]
		out = append(out, gen.SearchBlocksRow(*r))
	}
	return out
}

func substringInDocumentRows(in []gen.SearchBlocksSubstringInDocumentRow) []gen.SearchBlocksRow {
	out := make([]gen.SearchBlocksRow, 0, len(in))
	for i := range in {
		r := &in[i]
		out = append(out, gen.SearchBlocksRow(*r))
	}
	return out
}

func hitsFrom(rows []gen.SearchBlocksRow) []Hit {
	out := make([]Hit, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, Hit{
			DocumentID: r.DocumentID,
			Filename:   r.Filename,
			DeviceID:   r.DeviceID,
			DeviceName: r.DeviceName,
			State:      r.State,
			Page:       int(r.Page),
			RegionX0:   int(r.RegionX0),
			Index:      int(r.Idx),
			Kind:       r.Kind,
			Level:      int(r.Level),
			Lang:       r.Lang,
			Name:       doc.DisplayName(r.Lang),
			Snippet:    r.Snippet,
			Chars:      int(r.Chars),
			BM25:       r.Bm25,
			Score:      r.Score,
		})
	}
	return out
}
