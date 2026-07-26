package doc

import (
	"fmt"
	"sort"
	"strings"
)

// ColumnLanguage is what one text column of a page turned out to be.
//
// The unit is the column and not the page, because on a manual whose languages
// run in parallel a page holds several. Everything about naming a language has
// to be asked per column: a tag printed on the page belongs to one of them, and
// the alphabet evidence of one column says nothing about its neighbour.
type ColumnLanguage struct {
	Column Column `json:"column"`
	// Code is the label as printed, which need not be a valid tag: real manuals
	// print D, RUS, UA and KAZ.
	Code string `json:"code,omitempty"`
	// Lang is the BCP-47 tag, empty when nothing was established.
	Lang string `json:"lang,omitempty"`
	// Source is the signal that named it, empty when none could.
	Source Source `json:"source,omitempty"`
	// Conflict marks a column whose printed tag and whose alphabet disagree.
	// Recorded, never resolved silently.
	Conflict bool `json:"conflict"`
	// Note says in checkable terms how the column was read.
	Note string `json:"note,omitempty"`
}

// topRunsForTag is how many of a column's leading runs are searched for its
// printed tag.
//
// Not one: a right-to-left column puts its heading before the tab in reading
// order, and on the measured manual the tag sits on the second line. Not many
// either, since the further down the column the search goes the more ordinary
// words it meets. Five covers a heading, a subheading and the tab.
const topRunsForTag = 5

// ColumnLanguages names each column of a page.
//
// knownCodes is the vocabulary the document's own contents table declares, and
// it is what makes a single-letter tag usable: "D" is German on a manual whose
// index lists D, and a list marker everywhere else. Pass nil when no index was
// parsed — single letters are then believed only if the column's own alphabet
// agrees with them.
func ColumnLanguages(runs []TextRun, cols []Column, knownCodes map[string]bool) []ColumnLanguage {
	out := make([]ColumnLanguage, 0, len(cols))
	for i := range cols {
		out = append(out, nameColumn(runs, &cols[i], knownCodes))
	}
	return out
}

// nameColumn decides one column's language from its tag and its alphabet.
func nameColumn(runs []TextRun, col *Column, knownCodes map[string]bool) ColumnLanguage {
	inside := runsInColumn(runs, col)
	result := ColumnLanguage{Column: *col}

	tagCode, tagLang := columnTag(inside, knownCodes)

	var text strings.Builder
	for i := range inside {
		text.WriteString(inside[i].Text)
		text.WriteByte(' ')
	}
	rep := MatchRepertoire(text.String())
	repLang, repNamed := rep.Language()

	switch {
	case tagLang != "" && repNamed && SameLanguage(tagLang, repLang):
		// Both signals, agreeing. The strongest reading available: the document
		// says so and its own letters bear it out.
		result.Code, result.Lang, result.Source = tagCode, tagLang, SourcePageTag
		result.Note = fmt.Sprintf("printed %s, and the column's alphabet agrees", tagCode)

	case tagLang != "" && repNamed:
		// Both signals, disagreeing. Prefer the printed tag — it is the document
		// asserting its own language — but record the conflict, because one of
		// them is wrong and the user is better placed to say which.
		result.Code, result.Lang, result.Source = tagCode, tagLang, SourcePageTag
		result.Conflict = true
		result.Note = fmt.Sprintf("printed %s, but the column's alphabet reads as %s",
			tagCode, DisplayName(repLang))

	case tagLang != "":
		result.Code, result.Lang, result.Source = tagCode, tagLang, SourcePageTag
		result.Note = fmt.Sprintf("printed %s; no distinctive letters to corroborate it", tagCode)

	case repNamed:
		// No tag. Only three of the measured manual's five languages print one,
		// so this is the common case rather than a fallback.
		result.Lang, result.Source = repLang, SourceRepertoire
		result.Code = strings.ToUpper(BaseLanguage(repLang))
		result.Note = rep.Note

	case len(rep.Tied()) > 1:
		// The alphabet narrowed it and cannot finish. Naming one would be a coin
		// toss; saying which two it is between is useful.
		result.Note = fmt.Sprintf("alphabet fits %s equally", strings.Join(rep.Tied(), " and "))

	default:
		result.Note = "no printed tag and no distinctive letters"
	}
	return result
}

// runsInColumn returns the runs belonging to a column, in reading order.
func runsInColumn(runs []TextRun, col *Column) []TextRun {
	var inside []TextRun
	for i := range runs {
		r := &runs[i]
		// A run belongs to the column its left edge sits in. A run crossing the
		// boundary spans columns and belongs to neither.
		if r.X >= col.Min-1 && r.X+r.Width <= col.Max+1 {
			inside = append(inside, *r)
		}
	}
	sort.SliceStable(inside, func(a, b int) bool { return inside[a].Y < inside[b].Y })
	return inside
}

// columnTag finds a language code printed within a column.
func columnTag(inside []TextRun, knownCodes map[string]bool) (code, lang string) {
	limit := min(topRunsForTag, len(inside))
	for i := range inside[:limit] {
		token := strings.TrimSpace(stripFormatting(inside[i].Text))
		if token == "" || len([]rune(token)) > maxRunesInCodeLine {
			continue
		}
		if !looksLikeLanguageCode(token) || !PlausibleCodeToken(token) {
			continue
		}
		// A single letter is the most ambiguous token a page carries, so it is
		// taken only where the document's own index lists it.
		if singleLetterNeedsSupport(token) && !knownCodes[strings.ToUpper(token)] {
			continue
		}
		if normalised, ok := NormalizeCode(token); ok {
			return strings.ToUpper(token), normalised
		}
	}
	return "", ""
}
