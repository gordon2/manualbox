package doc

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// The character-repertoire signal: which language's alphabet a page is actually
// written with.
//
// [DominantScript] narrows a Cyrillic page to seven candidate languages and
// stops. That is not a gap in the implementation — a script table cannot know
// more. But languages sharing a script do not share an *alphabet*, and the
// letters only some of them can write cost nothing to count.
//
// Measured per column of a three-column Cyrillic page in a real manual:
//
//	column   Ukrainian marks   Russian marks   Kazakh marks   verdict
//	left                   0              40              0   Russian
//	middle                83               0              0   Ukrainian
//	right                 78             111            143   Kazakh
//
// consistent across 18 of the 19 such pages; the exception is a page of contact
// addresses rather than content. Three languages, one script, one page, one per
// column — exactly what script alone cannot resolve.
//
// The right column is why a maximum over those counts is the wrong reading.
// Kazakh's alphabet contains the і it shares with Ukrainian and the ы it shares
// with Russian, so overlapping counts are the normal case, not an anomaly. What
// decides is whether one language's alphabet can account for *everything*
// observed, and how much of that alphabet the text actually exercises.
//
// See docs/design/language-detection.md.

// repertoire is one language's distinctive characters within a script.
//
// Deliberately not the language's whole alphabet. The letters every candidate
// shares carry no information, and counting them would swamp the ones that do:
// Cyrillic и appears in six of these seven languages and roughly seven times per
// hundred letters, which would bury the ы that actually names Russian.
type repertoire struct {
	lang  string
	marks string
}

// cyrillicRepertoires are the letters that tell the Cyrillic languages apart.
//
// Two entries look wrong and are not. Kazakh lists ё ъ ы э and і as well as its
// own nine letters, because Kazakh genuinely writes them — omitting them would
// make a Kazakh page look like a contradiction rather than a Kazakh page.
// Bulgarian lists a single letter because its alphabet is a strict subset of
// Russian's: it has no exclusive letter at all, and what identifies it is ъ
// appearing in quantity while ы, э and ё never do.
var cyrillicRepertoires = []repertoire{
	{"ru", "ёъыэ"},
	{"uk", "ґєії"},
	{"be", "ёіўыэ"},
	{"bg", "ъ"},
	{"sr", "ђјљњћџ"},
	{"mk", "ѓѕјљњќџ"},
	{"kk", "әғқңөұүһіёъыэ"},
}

// latinRepertoires are the letters that tell the Latin-script languages apart.
//
// English, Indonesian and Malay are listed with nothing, which is the honest
// entry: they write no letter outside a-z, so this signal cannot see them and
// must never name them. Carrying them in the table rather than omitting them is
// what lets [RepertoireTies] report that fact instead of staying silent about it.
//
// Serbian appears here as well as in the Cyrillic table. It is written in both,
// and its Latin form is one of this signal's blind spots — see [RepertoireTies].
var latinRepertoires = []repertoire{
	{"pl", "ąćęłńóśźż"},
	{"cs", "áčďéěíňóřšťúůýž"},
	{"sk", "áäčďéíĺľňóôŕšťúýž"},
	{"hu", "áéíóöőúüű"},
	{"ro", "ăâîșț"},
	{"tr", "çğıöşü"},
	{"lt", "ąčėęįšūųž"},
	{"lv", "āčēģīķļņšūž"},
	{"et", "äöõüšž"},
	{"sl", "čšž"},
	{"hr", "čćđšž"},
	{"bs", "čćđšž"},
	{"sr", "čćđšž"},
	{"de", "äöüß"},
	{"fr", "àâæçèéêëîïôœùûüÿ"},
	{"es", "áéíñóúü"},
	{"it", "àèéìòù"},
	{"pt", "àáâãçéêíóôõú"},
	// Dutch writes no diacritic it cannot do without, so most Dutch pages carry
	// none of these and this signal simply has nothing to say about them. The
	// entry exists so that the tremas Dutch does write are not read as French.
	{"nl", "éëïü"},
	// é is not decoration in these two: Danish marks stress with it (idé, allé,
	// kontrollér) and Norwegian writes én. Leaving it out made a real Danish
	// paragraph contradict Danish.
	{"da", "æøåé"},
	// Bokmål and Nynorsk share this alphabet too, so nb and nn are equally
	// indistinguishable from Danish here. Manuals print NO, which is the code
	// listed.
	{"no", "æøåé"},
	{"sv", "åäö"},
	// Finnish differs from Swedish only by å, which belongs to the Finnish
	// alphabet but appears almost solely in Swedish loan names. Listing it would
	// make Swedish unidentifiable; leaving it out means a Swedish sample carrying
	// no å reads as Finnish. That trade is stated in the design doc.
	{"fi", "äö"},
	{"is", "áðéíóúýþæö"},
	{"en", ""},
	{"id", ""},
	{"ms", ""},
}

// langMarks is a prepared repertoire: a set for testing membership and a sorted
// slice for reporting.
type langMarks struct {
	lang string
	set  map[rune]bool
	all  []rune
}

// preparedRepertoires and repertoireUniverse are the tables above, indexed for
// use: per script, one entry per language, plus every mark any of them uses.
var preparedRepertoires, repertoireUniverse = prepareRepertoires()

func prepareRepertoires() (prepared map[string][]langMarks, universe map[string]map[rune]bool) {
	byScript := map[string][]repertoire{
		ScriptCyrillic: cyrillicRepertoires,
		ScriptLatin:    latinRepertoires,
	}

	prepared = make(map[string][]langMarks, len(byScript))
	universe = make(map[string]map[rune]bool, len(byScript))
	for script, langs := range byScript {
		all := make(map[rune]bool, 64)
		out := make([]langMarks, 0, len(langs))
		for i := range langs {
			marks := []rune(langs[i].marks)
			set := make(map[rune]bool, len(marks))
			for j, r := range marks {
				marks[j] = foldMark(r)
				set[marks[j]] = true
				all[marks[j]] = true
			}
			sort.Slice(marks, func(a, b int) bool { return marks[a] < marks[b] })
			out = append(out, langMarks{lang: langs[i].lang, set: set, all: marks})
		}
		prepared[script] = out
		universe[script] = all
	}
	return prepared, universe
}

// foldMark folds the character variants that are one letter typeset two ways.
//
// Romanian's ș and ț are routinely printed with a cedilla (ş, ţ) by fonts
// predating Unicode 3.0, which is the same letter and not Turkish. Folding them
// together stops a typesetting choice from reading as a different language, and
// applying the fold to the tables as well as to the text keeps both entries
// written the way their own language writes them.
func foldMark(r rune) rune {
	switch r {
	case 'ş':
		return 'ș'
	case 'ţ':
		return 'ț'
	}
	return r
}

// foreignMarkFraction is the share of the observed distinctive characters a
// language may be unable to write and still be considered.
//
// Not zero, because a page of one language routinely carries a brand name, a
// quoted term or a foreign address, and one character it cannot write is not
// evidence of a different language. Small, because the real cases are nowhere
// near the line: on the measured Cyrillic page's right column, Russian cannot
// account for 67% of the characters and Ukrainian 76%. This is a judgement
// rather than a measurement, and nothing below was tuned to it.
const foreignMarkFraction = 0.05

// strayForeignMarks is how many contradicting characters are forgiven outright,
// whatever the fraction says.
//
// A fraction alone is too harsh on short text, and short text is the normal case
// for a caption, a heading, or one column of a page. Eleven distinctive
// characters make a single stray worth 9%, which ruled out Danish for a Danish
// paragraph over one character. One character is never evidence of a language.
const strayForeignMarks = 1

// minRepertoireMarks is how many distinctive characters a text needs before this
// signal will name a language at all.
//
// One is a brand name: Wałęsa on an English page is not a Polish page. A page of
// prose in a language that has distinctive letters carries dozens — the measured
// columns carried 40, 83 and 332. Three is the smallest count that is not a
// single stray word, and below it the marks are still reported so a caller can
// see what was there.
const minRepertoireMarks = 3

// scoreEpsilon is how close two scores must be to count as tied. Equal
// repertoires produce bit-identical scores, so this exists only so that a tie
// never depends on floating-point luck.
const scoreEpsilon = 1e-9

// RepertoireMatch is what the character-repertoire signal concluded about a text.
//
// An empty Candidates is a normal and deliberate outcome: this signal reports
// nothing rather than guessing. Marks distinguishes the two reasons — zero means
// the text carries no distinctive characters at all, non-zero means it carries
// some that no single language accounts for, which is what a page mixing two
// languages looks like.
type RepertoireMatch struct {
	// Script is the script whose distinctive characters were read.
	Script string `json:"script"`
	// Marks is how many distinctive-character occurrences were found in it.
	Marks int `json:"marks"`
	// Candidates are the languages that can account for those characters, best
	// first.
	Candidates []RepertoireCandidate `json:"candidates,omitempty"`
	// Ambiguous reports that the leading candidates scored identically. The
	// signal has narrowed the language and cannot name it; see [RepertoireTies].
	Ambiguous bool `json:"ambiguous"`
	// Note says in checkable terms which characters produced this outcome.
	Note string `json:"note,omitempty"`
}

// RepertoireCandidate is one language's fit to the characters observed.
type RepertoireCandidate struct {
	// Lang is the language subtag.
	Lang string `json:"lang"`
	// Score is Matched/Marks × Used/Total, in 0 to 1.
	//
	// The first factor is how much of the evidence this language can write, the
	// second how much of this language the evidence exercises. Both are needed.
	// Coverage alone cannot separate a language from one whose alphabet contains
	// it — Kazakh writes every Russian letter, so it explains a Russian page
	// perfectly — and the second factor is what settles that: on a Russian page
	// none of Kazakh's own nine letters appear.
	Score float64 `json:"score"`
	// Matched and Foreign are how many of the observed characters this language
	// can and cannot write.
	Matched int `json:"matched"`
	Foreign int `json:"foreign"`
	// Used and Total are how many of this language's distinctive characters
	// appeared, and how many it has.
	Used  int `json:"used"`
	Total int `json:"total"`
	// Evidence is the characters this language accounts for and their counts,
	// most frequent first: "і×78 ы×40".
	Evidence string `json:"evidence"`
	// Missing is this language's distinctive characters that never appeared.
	Missing string `json:"missing,omitempty"`
}

// MatchRepertoire reads a text's distinctive characters and reports which
// languages of its script can account for them.
//
// Pure, free, and needs no model, network or dependency. It is the fifth
// language signal and it is not authoritative: it answers "whose alphabet is
// this", which is not the same question as "what language is this", and there
// are pairs it cannot separate at all. Use [RepertoireMatch.Language] to get an
// answer only when there is one.
func MatchRepertoire(s string) RepertoireMatch {
	script := DominantScript(s)
	m := RepertoireMatch{Script: script}

	if script == "" {
		m.Note = "no letters to read"
		return m
	}
	langs := preparedRepertoires[script]
	if len(langs) == 0 {
		// Every other script this package recognises is already resolved to one
		// language by the script itself, so there is nothing left to separate.
		m.Note = fmt.Sprintf("%s script needs no repertoire table", script)
		return m
	}

	counts := RepertoireMarks(script, s)
	for _, n := range counts {
		m.Marks += n
	}
	if m.Marks == 0 {
		m.Note = fmt.Sprintf("no %s character here belongs to one language rather than another", script)
		return m
	}
	if m.Marks < minRepertoireMarks {
		m.Note = fmt.Sprintf("only %d distinctive characters (%s), too few to name a language",
			m.Marks, markList(counts, nil))
		return m
	}

	all := make([]RepertoireCandidate, len(langs))
	admissible := make([]RepertoireCandidate, 0, len(langs))
	for i := range langs {
		l := &langs[i]
		c := RepertoireCandidate{Lang: l.lang, Total: len(l.all)}
		for r, n := range counts {
			if l.set[r] {
				c.Matched += n
				c.Used++
			} else {
				c.Foreign += n
			}
		}
		all[i] = c

		if c.Total == 0 || c.Matched == 0 {
			continue
		}
		if c.Foreign > strayForeignMarks && float64(c.Foreign)/float64(m.Marks) > foreignMarkFraction {
			continue
		}
		c.Score = float64(c.Matched) / float64(m.Marks) * float64(c.Used) / float64(c.Total)
		c.Evidence = markList(counts, l.set)
		c.Missing = missingMarks(l, counts)
		admissible = append(admissible, c)
	}

	if len(admissible) == 0 {
		m.Note = noSingleLanguageNote(m.Marks, all, counts)
		return m
	}

	sort.Slice(admissible, func(i, j int) bool {
		if admissible[i].Score != admissible[j].Score {
			return admissible[i].Score > admissible[j].Score
		}
		return admissible[i].Lang < admissible[j].Lang
	})
	m.Candidates = admissible
	m.Ambiguous = len(admissible) > 1 &&
		admissible[0].Score-admissible[1].Score < scoreEpsilon
	m.Note = decidedNote(m)
	return m
}

// Language returns the one language the characters name, and whether the signal
// is prepared to name one. It is false whenever the evidence was absent,
// contradictory, or fits two languages equally — a confident wrong answer being
// worse than an absent one.
func (m RepertoireMatch) Language() (string, bool) {
	if len(m.Candidates) == 0 || m.Ambiguous {
		return "", false
	}
	return m.Candidates[0].Lang, true
}

// Tied returns the languages that share the leading score, in order. It is the
// answer when [RepertoireMatch.Language] declines: the signal has narrowed the
// page to these and cannot go further.
func (m RepertoireMatch) Tied() []string {
	if len(m.Candidates) == 0 {
		return nil
	}
	var tied []string
	for i := range m.Candidates {
		if m.Candidates[0].Score-m.Candidates[i].Score >= scoreEpsilon {
			break
		}
		tied = append(tied, m.Candidates[i].Lang)
	}
	return tied
}

// RepertoireMarks returns the characters in s that distinguish one language of
// the given script from another, and how often each occurs. Characters of other
// scripts are ignored rather than counted against anything, so a Latin brand
// name on a Russian page is not evidence about the Russian.
//
// Case is folded, so an all-capitals heading counts the same as body text. Only
// precomposed characters are seen: a decomposed é (e plus a combining accent)
// reads as a plain e and is silently not evidence, which loses the signal rather
// than misdirecting it.
func RepertoireMarks(script, s string) map[rune]int {
	universe := repertoireUniverse[script]
	if len(universe) == 0 {
		return nil
	}
	counts := make(map[rune]int, 8)
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		if r = foldMark(unicode.ToLower(r)); universe[r] {
			counts[r]++
		}
	}
	return counts
}

// RepertoireTies returns the languages whose distinctive characters are exactly
// lang's, including lang itself, sorted. More than one entry means this signal
// can never separate them and will report them tied rather than pick one.
//
// It is derived from the same tables the signal scores against, so it cannot
// drift from what the signal actually does. Known groups:
//
//	da no      identical: æ ø å. Bokmål and Nynorsk share it too.
//	bs hr sr   identical in Latin script: č ć đ š ž.
//	en id ms   nothing at all: they write no letter outside a-z.
//
// A language written in two scripts is reported against every script it appears
// in, so Serbian ties with Bosnian and Croatian on the strength of its Latin
// form even though its Cyrillic form is unmistakable.
//
// Ties are not the only limit — a language whose repertoire is a subset of
// another's is separated only by the larger one's letters being absent, which
// short text cannot establish. See docs/design/language-detection.md.
func RepertoireTies(lang string) []string {
	base := BaseLanguage(lang)
	if base == "" {
		base = lang
	}

	tied := make(map[string]bool, 4)
	for _, langs := range preparedRepertoires {
		key, found := "", false
		for i := range langs {
			if langs[i].lang == base {
				key, found = string(langs[i].all), true
				break
			}
		}
		if !found {
			continue
		}
		for i := range langs {
			if string(langs[i].all) == key {
				tied[langs[i].lang] = true
			}
		}
	}
	if len(tied) == 0 {
		return nil
	}

	out := make([]string, 0, len(tied))
	for l := range tied {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// decidedNote explains an outcome that produced candidates, in the terms a
// reader can check against the page: which characters were counted, and why the
// runner-up lost.
func decidedNote(m RepertoireMatch) string {
	best := &m.Candidates[0]
	if m.Ambiguous {
		return fmt.Sprintf("%s write the same distinctive characters (%s) and cannot be told apart here",
			strings.Join(m.Tied(), " and "), best.Evidence)
	}
	if len(m.Candidates) == 1 {
		return fmt.Sprintf("%s: %s; no other %s language accounts for them",
			best.Lang, best.Evidence, m.Script)
	}
	next := &m.Candidates[1]
	return fmt.Sprintf("%s: %s; %s fits too but %d of its distinctive letters never appear (%s)",
		best.Lang, best.Evidence, next.Lang, next.Total-next.Used, next.Missing)
}

// noSingleLanguageNote describes marks that no one language can account for.
// Naming the two biggest contributors is what makes a mixed page reportable
// rather than merely unanswered.
func noSingleLanguageNote(marks int, all []RepertoireCandidate, counts map[rune]int) string {
	ranked := make([]RepertoireCandidate, len(all))
	copy(ranked, all)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Matched != ranked[j].Matched {
			return ranked[i].Matched > ranked[j].Matched
		}
		return ranked[i].Lang < ranked[j].Lang
	})

	var named []string
	for i := range ranked {
		if len(named) == 2 || ranked[i].Matched == 0 {
			break
		}
		named = append(named, fmt.Sprintf("%s accounts for %d", ranked[i].Lang, ranked[i].Matched))
	}
	if len(named) == 0 {
		return fmt.Sprintf("%d distinctive characters (%s) belong to no language in this table",
			marks, markList(counts, nil))
	}
	return fmt.Sprintf("%d distinctive characters (%s) fit no single language: %s",
		marks, markList(counts, nil), strings.Join(named, ", "))
}

// markList renders characters and their counts as "і×78 ы×40", most frequent
// first. only restricts it to one language's characters; nil renders all of them.
func markList(counts map[rune]int, only map[rune]bool) string {
	runes := make([]rune, 0, len(counts))
	for r := range counts {
		if only == nil || only[r] {
			runes = append(runes, r)
		}
	}
	sort.Slice(runes, func(i, j int) bool {
		if counts[runes[i]] != counts[runes[j]] {
			return counts[runes[i]] > counts[runes[j]]
		}
		return runes[i] < runes[j]
	})

	parts := make([]string, len(runes))
	for i, r := range runes {
		parts[i] = fmt.Sprintf("%c×%d", r, counts[r])
	}
	return strings.Join(parts, " ")
}

// missingMarks lists the language's distinctive characters that did not appear.
// It is the evidence *against* a language that otherwise fits, and the reason a
// Russian page is not read as Kazakh.
func missingMarks(l *langMarks, counts map[rune]int) string {
	var b strings.Builder
	for _, r := range l.all {
		if counts[r] == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
