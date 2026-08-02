package doc

import "testing"

// TestAContentsEntryIsRecognisedByItsLeaderAndItsPageNumber pins both halves of the
// signal, on the real strings of the columns manual's contents page.
//
// Both are required, and the four short dot runs that document also prints are why:
// measured over both whole manuals, the runs of two or more dots are 3, 3, 3, 4 and
// then 34 to 91, with nothing in between, and the four short ones are ellipses in
// prose carrying no page number. Either test alone would be enough here; both are
// kept because "a leader" and "a page at the end of it" are what a contents entry is,
// and the next document gets no say in which of the two it happens to break.
func TestAContentsEntryIsRecognisedByItsLeaderAndItsPageNumber(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want bool
	}{
		{"a plain entry", "Мы поздравляем Вас  ........................................................................2", true},
		{"a page range, en dash", "Trockensaugen . ........................................................................14 – 22", true},
		{"a title holding its own digits", "Reinigung der AQUA-Box  ...........................................................40 – 44", true},
		{"the leader in a run of its own, joined on one baseline", "Ihr THOMAS  ...................................11", true},

		{"an ellipsis in prose", "und so weiter ... aber nicht mehr", false},
		{"an ellipsis before a number", "warten Sie ... 30 Sekunden lang", false},
		{"a leader with nothing after it", "Fehlerbehebung ............................................", false},
		{"a page number with no leader", "Fehlerbehebung 57", false},
		{"dots and digits with no title", "  ........................................ 12", false},
		{"empty", "   ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dots, got := contentsEntry(tc.text)
			if got != tc.want {
				t.Errorf("contentsEntry(%q) = %v (leader %d), want %v", tc.text, got, dots, tc.want)
			}
			if got && dots < minLeaderDots {
				t.Errorf("reported a leader of %d, under the floor of %d", dots, minLeaderDots)
			}
		})
	}
}
