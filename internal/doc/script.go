package doc

import (
	"sort"
	"unicode"
)

// Script names reported by [DominantScript]. These are Unicode script names
// except for Kana, which is a deliberate composite — see below.
const (
	ScriptLatin      = "Latin"
	ScriptCyrillic   = "Cyrillic"
	ScriptGreek      = "Greek"
	ScriptHebrew     = "Hebrew"
	ScriptArabic     = "Arabic"
	ScriptThai       = "Thai"
	ScriptHan        = "Han"
	ScriptKana       = "Kana"
	ScriptHangul     = "Hangul"
	ScriptDevanagari = "Devanagari"
	ScriptArmenian   = "Armenian"
	ScriptGeorgian   = "Georgian"
)

// scriptTables maps a reported script name to its Unicode range table.
//
// Hiragana and Katakana are counted together as Kana rather than separately,
// because the distinction between them is orthographic rather than linguistic:
// both mean Japanese.
var scriptTables = []struct {
	name  string
	table *unicode.RangeTable
}{
	{ScriptLatin, unicode.Latin},
	{ScriptCyrillic, unicode.Cyrillic},
	{ScriptGreek, unicode.Greek},
	{ScriptHebrew, unicode.Hebrew},
	{ScriptArabic, unicode.Arabic},
	{ScriptThai, unicode.Thai},
	{ScriptHan, unicode.Han},
	{ScriptKana, unicode.Hiragana},
	{ScriptKana, unicode.Katakana},
	{ScriptHangul, unicode.Hangul},
	{ScriptDevanagari, unicode.Devanagari},
	{ScriptArmenian, unicode.Armenian},
	{ScriptGeorgian, unicode.Georgian},
}

// kanaEvidenceRunes is how many kana are enough to call a Han-dominant page
// Japanese. A handful of stray kana in a Chinese document is possible; a page of
// Japanese prose always carries many, because grammatical particles are kana.
const kanaEvidenceRunes = 5

// ScriptCounts returns the number of letters per script in s.
func ScriptCounts(s string) map[string]int {
	counts := make(map[string]int, 4)
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		for _, st := range scriptTables {
			if unicode.Is(st.table, r) {
				counts[st.name]++
				break
			}
		}
	}
	return counts
}

// DominantScript reports which script a page is written in, or "" when there are
// no letters to judge.
//
// This is the cheapest language signal there is: it needs no models, no network
// and no dependency, and it settles the non-Latin scripts outright. Measured on a
// 34-language manual it resolved 27% of pages to a single language and narrowed
// the Cyrillic pages to three candidates. What it cannot do is separate the 25
// languages that share the Latin alphabet, which is the residue that needs a
// statistical detector. See docs/design/language-detection.md.
func DominantScript(s string) string {
	counts := ScriptCounts(s)
	if len(counts) == 0 {
		return ""
	}

	// Japanese mixes kanji with kana, and kanji usually outnumber kana on a page,
	// so a plain maximum would report Han and lose the distinction from Chinese.
	// Any substantial kana presence is decisive.
	if counts[ScriptKana] >= kanaEvidenceRunes {
		return ScriptKana
	}

	best, bestCount := "", 0
	for name, n := range counts {
		// Ties resolve by name so the result is deterministic; a tie between two
		// scripts on one page is a mixed page and either answer is arbitrary.
		if n > bestCount || (n == bestCount && name < best) {
			best, bestCount = name, n
		}
	}
	return best
}

// ScriptLanguages maps a script to the language subtags that use it, most common
// first. It is used to narrow candidates and to sanity-check a printed page tag:
// a page tagged EL that is not Greek script is not really Greek.
//
// The Latin entry is deliberately empty. Listing the dozens of languages that
// use the Latin alphabet would imply a discrimination this signal cannot make,
// and callers must treat an empty result as "this script tells you nothing".
var scriptLanguages = map[string][]string{
	ScriptGreek:      {"el"},
	ScriptHebrew:     {"he"},
	ScriptArabic:     {"ar", "fa", "ur"},
	ScriptThai:       {"th"},
	ScriptHan:        {"zh"},
	ScriptKana:       {"ja"},
	ScriptHangul:     {"ko"},
	ScriptDevanagari: {"hi", "mr", "ne"},
	ScriptArmenian:   {"hy"},
	ScriptGeorgian:   {"ka"},
	ScriptCyrillic:   {"ru", "uk", "bg", "sr", "kk", "mk", "be"},
	ScriptLatin:      {},
}

// ScriptLanguages returns the language subtags that use a script.
func ScriptLanguages(script string) []string {
	langs := scriptLanguages[script]
	out := make([]string, len(langs))
	copy(out, langs)
	return out
}

// ScriptAllows reports whether a language subtag is plausible for a script.
//
// A Latin-script page allows any language, because the signal cannot narrow it;
// saying otherwise would turn "no information" into a false rejection. An unknown
// script also allows anything, for the same reason.
func ScriptAllows(script, lang string) bool {
	if script == "" || script == ScriptLatin {
		return true
	}
	langs, known := scriptLanguages[script]
	if !known || len(langs) == 0 {
		return true
	}
	for _, l := range langs {
		if l == lang {
			return true
		}
	}
	return false
}

// languageScripts records the scripts a language is actually written in, for the
// languages that are not written in the Latin alphabet.
//
// This is the converse of scriptLanguages and it catches a different error. A
// Latin-script page permits any language as far as [ScriptAllows] is concerned,
// which is correct — Latin cannot narrow a language. But it is still absurd for a
// page of English prose to be labelled Japanese, and that is exactly what happened
// on the measured fixture: the printed index's Japanese entry, being last, claimed
// every page to the end of the document, absorbing an English back cover.
//
// Serbian deliberately lists both Cyrillic and Latin: it is genuinely written in
// both, and the measured fixture uses Latin.
var languageScripts = map[string][]string{
	"ja": {ScriptKana, ScriptHan},
	"zh": {ScriptHan, ScriptKana},
	"ko": {ScriptHangul, ScriptHan},
	"ru": {ScriptCyrillic}, "uk": {ScriptCyrillic}, "bg": {ScriptCyrillic},
	"be": {ScriptCyrillic}, "mk": {ScriptCyrillic}, "kk": {ScriptCyrillic},
	"sr": {ScriptCyrillic, ScriptLatin},
	"el": {ScriptGreek},
	"he": {ScriptHebrew},
	"ar": {ScriptArabic}, "fa": {ScriptArabic}, "ur": {ScriptArabic},
	"th": {ScriptThai},
	"hy": {ScriptArmenian}, "ka": {ScriptGeorgian},
	"hi": {ScriptDevanagari}, "mr": {ScriptDevanagari}, "ne": {ScriptDevanagari},
}

// LanguageAllowsScript reports whether a language can be written in a script.
// Languages absent from the table use the Latin alphabet and are unconstrained.
func LanguageAllowsScript(lang, script string) bool {
	if script == "" || lang == "" {
		return true
	}
	scripts, constrained := languageScripts[lang]
	if !constrained {
		return true
	}
	for _, s := range scripts {
		if s == script {
			return true
		}
	}
	return false
}

// ScriptCompatible reports whether a script and a language can coexist on a page,
// checked in both directions: the script must permit the language, and the
// language must be written in that script. Either check alone lets an obvious
// nonsense through.
func ScriptCompatible(script, lang string) bool {
	base := BaseLanguage(lang)
	if base == "" {
		base = lang
	}
	return ScriptAllows(script, base) && LanguageAllowsScript(base, script)
}

// SortedScripts returns the scripts present in s, most letters first. Used for
// reporting rather than for decisions.
func SortedScripts(s string) []string {
	counts := ScriptCounts(s)
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}
