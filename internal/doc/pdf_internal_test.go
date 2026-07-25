package doc

import "testing"

// These tests cover the text-shape parsing that the free language signals depend
// on. They are hermetic: no PDF, no poppler, no network. The real-document
// assertions live in fixture_test.go and skip by default.

func TestPageTagReadsBidiWrappedCode(t *testing.T) {
	// A right-to-left page wraps Latin-script furniture in bidirectional
	// embedding marks, so the tab that reads "HE" is really RLE LRE H E PDF PDF.
	// Missing this left the Hebrew and Arabic sections of a real manual
	// unlabelled, so it is the regression most worth pinning down.
	// \u202b RLE, \u202a LRE, \u202c PDF (pop directional formatting).
	const rtlPage = "\u202bמידע בטיחותי\u202c\n\u202b\u202aHE\u202c\u202c\n\u202bיש לקרוא\u202c\n"

	if got := pageTag(rtlPage); got != "HE" {
		t.Errorf("pageTag on a right-to-left page = %q, want %q", got, "HE")
	}
}

func TestPageTagVariants(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"plain first line", "EN\n21. Charging Contacts\n", "EN"},
		{"lowercase is normalised", "en\nSomething\n", "EN"},
		{"region subtag", "ZH-HK\n用戶手冊\n", "ZH-HK"},
		{"after a heading", "Safety Information\nAR\nbody text\n", "AR"},
		{"blank lines skipped", "\n\n\nDE\nBenutzerhandbuch\n", "DE"},
		{"no tag at all", "Just prose with no code on its own line.\n", ""},
		{"too far down the page", "one\ntwo\nthree\nEN\n", ""},
		{"word is not a code", "Contents\nOverview\n", ""},
		{"three letters is not a code", "ENG\nbody\n", ""},
		{"digits are not a code", "01\nbody\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageTag(tc.text); got != tc.want {
				t.Errorf("pageTag(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestPageTagCandidatesSearchesWholePage(t *testing.T) {
	// Candidates are permissive by design: a right-to-left page can carry its tab
	// well below the fold. Narrowing happens in EffectiveTags, against the codes
	// the printed index declares.
	text := "heading\nbody\nmore body\nyet more\nAR\ntrailing\n"

	if got := pageTag(text); got != "" {
		t.Errorf("pageTag should not reach past the top of the page, got %q", got)
	}
	got := pageTagCandidates(text)
	if len(got) != 1 || got[0] != "AR" {
		t.Errorf("pageTagCandidates = %v, want [AR]", got)
	}
}

func TestPageFolio(t *testing.T) {
	tests := []struct {
		name, text string
		want       int // 0 means "expect none"
	}{
		{"trailing number", "body text\nmore\n6\n", 6},
		{"bidi wrapped", "body\n\u202b\u202a194\u202c\u202c\n", 194},
		{"four digits allowed", "body\n1024\n", 1024},
		{"five digits rejected", "body\n10240\n", 0},
		{"no number", "body text only\n", 0},
		{"zero rejected", "body\n0\n", 0},
		{"last number wins", "12\nbody\n77\n", 77},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pageFolio(tc.text)
			switch {
			case tc.want == 0 && got != nil:
				t.Errorf("pageFolio(%q) = %d, want none", tc.text, *got)
			case tc.want != 0 && got == nil:
				t.Errorf("pageFolio(%q) = none, want %d", tc.text, tc.want)
			case tc.want != 0 && *got != tc.want:
				t.Errorf("pageFolio(%q) = %d, want %d", tc.text, *got, tc.want)
			}
		})
	}
}

func TestStripFormattingKeepsContent(t *testing.T) {
	// Stripping must remove only formatting. A Hebrew line stripped of bidi marks
	// must still be the same Hebrew.
	const withMarks = "\u202bמידע\u202c"
	const wantText = "מידע"
	if got := stripFormatting(withMarks); got != wantText {
		t.Errorf("stripFormatting = %q, want %q", got, wantText)
	}
	if got := stripFormatting("plain ascii"); got != "plain ascii" {
		t.Errorf("stripFormatting altered plain text: %q", got)
	}
}

func TestNewPageCountsRunesNotBytes(t *testing.T) {
	// A byte count would judge Cyrillic and CJK pages as larger than equivalent
	// Latin ones, and the text-layer threshold would then behave differently per
	// script. Chars must be runes.
	p := newPage(1, "Руководство")
	if got, want := p.Chars, 11; got != want {
		t.Errorf("Chars = %d, want %d runes (not bytes)", got, want)
	}
}

func TestParsePageSize(t *testing.T) {
	w, h := parsePageSize("612.283 x 413.858 pts")
	if w != 612.283 || h != 413.858 {
		t.Errorf("parsePageSize = %v x %v, want 612.283 x 413.858", w, h)
	}
	if w, h := parsePageSize("nonsense"); w != 0 || h != 0 {
		t.Errorf("parsePageSize on nonsense = %v x %v, want 0 x 0", w, h)
	}
}
