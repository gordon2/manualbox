package doc

import (
	"strings"

	"golang.org/x/text/language"
)

// codeAliases maps the language labels manuals actually print onto BCP-47.
//
// Two distinct problems live here. The first is that some manuals use a code
// that is simply wrong: the measured fixture prints UA for Ukrainian (the tag is
// uk; UA is the country) and CZ for Czech (the tag is cs). The second is that
// many manuals label a section by the *country* they sell it in rather than by
// the language it is written in — DK for Danish, SE for Swedish, JP for Japanese.
//
// Both are aliases rather than errors to reject. A manual that says CZ is telling
// us something true in a non-standard way, and dropping the section because the
// label is not a valid tag would lose real information.
var codeAliases = map[string]string{
	// Wrong tag for the right language, seen in real manuals.
	"UA": "uk", // Ukraine (country) used for Ukrainian
	"CZ": "cs", // Czechia (country) used for Czech
	"GR": "el", // Greece used for Greek
	"RS": "sr", // Serbia used for Serbian
	"SI": "sl", // Slovenia used for Slovenian
	"EE": "et", // Estonia used for Estonian
	"DK": "da", // Denmark used for Danish
	"SE": "sv", // Sweden used for Swedish
	"JP": "ja", // Japan used for Japanese
	"CN": "zh", // China used for Chinese
	"KR": "ko", // Korea used for Korean
	"IL": "he", // Israel used for Hebrew
	"IR": "fa", // Iran used for Persian
	"BR": "pt-BR",
	"TW": "zh-TW",
	"HK": "zh-HK",

	// Three-letter codes, as manuals actually print them. Mostly ISO 639-2/B,
	// which is the vernacular form — GER rather than DEU, and both of the
	// measured manual's Cyrillic codes are of this kind.
	"ENG": "en", "GER": "de", "DEU": "de", "FRA": "fr", "FRE": "fr",
	"ITA": "it", "SPA": "es", "ESP": "es", "POR": "pt", "NLD": "nl",
	"DUT": "nl", "POL": "pl", "CZE": "cs", "CES": "cs", "SVK": "sk",
	"SLO": "sk", "SLV": "sl", "HUN": "hu", "ROM": "ro", "RON": "ro",
	"BUL": "bg", "RUS": "ru", "UKR": "uk", "BLR": "be", "SRP": "sr",
	"SRB": "sr", "HRV": "hr", "BOS": "bs", "MKD": "mk", "LIT": "lt",
	"LAV": "lv", "EST": "et", "FIN": "fi", "SWE": "sv", "NOR": "no",
	"DAN": "da", "ISL": "is", "GRE": "el", "ELL": "el", "TUR": "tr",
	"KAZ": "kk", "UZB": "uz", "ARA": "ar", "HEB": "he", "THA": "th",
	"VIE": "vi", "IND": "id", "MSA": "ms", "CHN": "zh", "ZHO": "zh",
	"JPN": "ja", "KOR": "ko",

	// Single letters. Real and common on European manuals, and the most
	// ambiguous token a page can carry — "D" is also a list marker and a
	// diagram label — so these are believed only with corroboration. See
	// singleLetterNeedsSupport.
	"D": "de", "F": "fr", "I": "it", "E": "es", "P": "pt",
	"N": "no", "S": "sv", "H": "hu",
}

// PlausibleCodeToken reports whether a token could be a printed language label.
//
// Shape is not enough, and the gap is wider than it looks. golang.org/x/text
// accepts "one", "two", "the", "and", "for" and "abc" as languages — they are
// real ISO 639-3 codes for languages no appliance manual is printed in — so
// letting a three-letter token fall through to the parser turns the first word of
// any page into a language tag. That happened: "one" was read as a code.
//
// So the rule is by length. Two letters, with an optional region, go to the
// parser, which knows the small closed set of ISO 639-1. One and three letters
// must appear in codeAliases, which lists what manuals actually print.
func PlausibleCodeToken(s string) bool {
	base, region, hasRegion := strings.Cut(strings.TrimSpace(s), "-")
	if hasRegion && len(region) != 2 {
		return false
	}
	switch len(base) {
	case 2:
		_, ok := NormalizeCode(s)
		return ok
	case 1, 3:
		_, ok := codeAliases[strings.ToUpper(base)]
		return ok
	default:
		return false
	}
}

// NormalizeCode turns a printed language label into a BCP-47 tag.
//
// The raw code is always preserved by the caller alongside the result, so a label
// that cannot be normalised is still reportable rather than discarded. That is
// the point of storing both `code` and `lang` on a language run.
func NormalizeCode(raw string) (string, bool) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return "", false
	}
	upper := strings.ToUpper(code)

	if alias, ok := codeAliases[upper]; ok {
		code = alias
	}

	tag, err := language.Parse(code)
	if err != nil {
		return "", false
	}
	// Canonical form, e.g. "zh-hk" becomes "zh-HK" and "EN" becomes "en".
	return tag.String(), true
}

// BaseLanguage reduces a BCP-47 tag to its base subtag: "zh-HK" becomes "zh",
// "pt-BR" becomes "pt". Used when comparing against the household's configured
// languages, where a reader of pt reads pt-BR.
func BaseLanguage(tag string) string {
	parsed, err := language.Parse(tag)
	if err != nil {
		return ""
	}
	base, _ := parsed.Base()
	return base.String()
}

// SameLanguage reports whether two tags name the same language, ignoring region
// and script. It is what decides whether a section is one the household reads.
func SameLanguage(a, b string) bool {
	ba, bb := BaseLanguage(a), BaseLanguage(b)
	return ba != "" && ba == bb
}

// MatchesAny reports whether tag is one of the household's languages, and which
// one it matched.
func MatchesAny(tag string, household []string) (string, bool) {
	for _, h := range household {
		if SameLanguage(tag, h) {
			return h, true
		}
	}
	return "", false
}

// DisplayName returns a human-readable language name in English, falling back to
// the tag itself when it cannot be named. Used in the pre-flight gate, where
// "Ukrainian" is far more useful than "uk".
func DisplayName(tag string) string {
	parsed, err := language.Parse(tag)
	if err != nil {
		return tag
	}
	if name := languageNames[BaseLanguage(parsed.String())]; name != "" {
		return name
	}
	return parsed.String()
}

// languageNames covers the languages that actually turn up in appliance manuals.
// x/text can produce display names only with the full display package and its
// tables, which is a large dependency for a label; this is the subset that
// matters. Anything absent falls back to the tag.
var languageNames = map[string]string{
	"ar": "Arabic", "be": "Belarusian", "bg": "Bulgarian", "bs": "Bosnian",
	"ca": "Catalan", "cs": "Czech", "da": "Danish", "de": "German",
	"el": "Greek", "en": "English", "es": "Spanish", "et": "Estonian",
	"fa": "Persian", "fi": "Finnish", "fr": "French", "he": "Hebrew",
	"hi": "Hindi", "hr": "Croatian", "hu": "Hungarian", "hy": "Armenian",
	"id": "Indonesian", "is": "Icelandic", "it": "Italian", "ja": "Japanese",
	"ka": "Georgian", "kk": "Kazakh", "ko": "Korean", "lt": "Lithuanian",
	"lv": "Latvian", "mk": "Macedonian", "ms": "Malay", "nb": "Norwegian Bokmål",
	"ne": "Nepali", "nl": "Dutch", "nn": "Norwegian Nynorsk", "no": "Norwegian",
	"pl": "Polish", "pt": "Portuguese", "ro": "Romanian", "ru": "Russian",
	"sk": "Slovak", "sl": "Slovenian", "sq": "Albanian", "sr": "Serbian",
	"sv": "Swedish", "th": "Thai", "tr": "Turkish", "uk": "Ukrainian",
	"ur": "Urdu", "uz": "Uzbek", "vi": "Vietnamese", "zh": "Chinese",
}
