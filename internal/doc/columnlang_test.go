package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for naming a column's language. No PDF, no poppler: runs are
// built here. The real-document acceptance lives against the fixtures.

// runsAt builds a column's worth of text runs stacked down the page.
func runsAt(x float64, lines ...string) []doc.TextRun {
	out := make([]doc.TextRun, 0, len(lines))
	for i, s := range lines {
		out = append(out, doc.TextRun{
			X: x, Y: float64(20 + i*16), Width: 250, Height: 14, Text: s,
		})
	}
	return out
}

func col(x0, x1 float64, runs int) doc.Column {
	return doc.Column{Min: x0, Max: x1, Runs: runs}
}

// german and polish are ordinary manual prose carrying each language's own
// letters, so the alphabet signal has something to work with.
const (
	german = "Gerät sorgfältig prüfen und für spätere Zwecke aufbewahren. Größe beachten."
	polish = "Urządzenie należy sprawdzić i zachować instrukcję. Część zamienna dostępna."
	ukr    = "Пристрій слід перевірити та зберегти інструкцію. Її потрібно вивчити."
	rus    = "Прибор следует проверить и сохранить инструкцию. Её нужно изучить."
)

func TestColumnNamedByTagAndAlphabetAgreeing(t *testing.T) {
	runs := runsAt(30, "PL", polish, polish)
	got := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 3)}, nil)

	if len(got) != 1 {
		t.Fatalf("expected 1 column, got %d", len(got))
	}
	if got[0].Lang != "pl" || got[0].Source != doc.SourcePageTag {
		t.Errorf("got %q via %q, want pl via page-tag", got[0].Lang, got[0].Source)
	}
	if got[0].Conflict {
		t.Error("agreement was recorded as a conflict")
	}
	if !strings.Contains(got[0].Note, "agrees") {
		t.Errorf("note should record the corroboration, got %q", got[0].Note)
	}
}

func TestColumnTagAndAlphabetDisagreeingIsRecorded(t *testing.T) {
	// The tag says Polish, the letters are unmistakably German. One of them is
	// wrong and the design forbids resolving that silently.
	runs := runsAt(30, "PL", german, german)
	got := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 3)}, nil)

	if !got[0].Conflict {
		t.Error("a tag contradicted by the alphabet must be flagged")
	}
	if got[0].Lang != "pl" {
		t.Errorf("lang = %q; the printed tag should still win, being the document's own claim", got[0].Lang)
	}
	if !strings.Contains(got[0].Note, "German") {
		t.Errorf("the note must name what the alphabet read instead, got %q", got[0].Note)
	}
}

func TestColumnNamedByAlphabetAloneWhenNoTag(t *testing.T) {
	// The common case rather than a fallback: only three of the measured
	// manual's five languages print a tag at all.
	runs := runsAt(30, ukr, ukr, ukr)
	got := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 3)}, nil)

	if got[0].Lang != "uk" || got[0].Source != doc.SourceRepertoire {
		t.Errorf("got %q via %q, want uk via repertoire", got[0].Lang, got[0].Source)
	}
}

func TestNeighbouringColumnsAreNamedIndependently(t *testing.T) {
	// The whole point of working per column. A page's two columns must not
	// contaminate one another, and reading the page as one blob would name at
	// most one of them.
	var runs []doc.TextRun
	runs = append(runs, runsAt(30, "D", german, german)...)
	runs = append(runs, runsAt(320, rus, rus, rus)...)

	got := doc.ColumnLanguages(runs,
		[]doc.Column{col(30, 280, 3), col(320, 570, 3)},
		map[string]bool{"D": true})

	if len(got) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(got))
	}
	if got[0].Lang != "de" {
		t.Errorf("left column = %q, want de", got[0].Lang)
	}
	if got[1].Lang != "ru" {
		t.Errorf("right column = %q, want ru", got[1].Lang)
	}
}

func TestSingleLetterTagNeedsTheIndexVocabulary(t *testing.T) {
	// "D" is German on a manual whose contents table lists D, and a figure label
	// everywhere else. Without the vocabulary the column falls back to its
	// alphabet, which here happens to agree — the point is which signal was used.
	runs := runsAt(30, "D", german, german)

	with := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 3)}, map[string]bool{"D": true})
	if with[0].Source != doc.SourcePageTag {
		t.Errorf("with the vocabulary the tag should be used, got %q", with[0].Source)
	}

	without := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 3)}, nil)
	if without[0].Source == doc.SourcePageTag {
		t.Error("without the vocabulary a single letter must not be taken as a tag")
	}
	if without[0].Lang != "de" {
		t.Errorf("the alphabet should still name it de, got %q", without[0].Lang)
	}
}

func TestTagIsFoundBelowTheColumnHeading(t *testing.T) {
	// A right-to-left column puts its heading before the tab in reading order,
	// so the tag is not the first run. Searching only the first would lose it.
	runs := runsAt(30, "Sicherheitshinweise", "D", german)
	got := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 3)}, map[string]bool{"D": true})

	if got[0].Source != doc.SourcePageTag || got[0].Code != "D" {
		t.Errorf("got %q via %q, want the D printed on the second line", got[0].Code, got[0].Source)
	}
}

func TestUnnameableColumnSaysSo(t *testing.T) {
	// English has no distinctive letters, so with no tag there is nothing to go
	// on. Saying nothing is the correct answer and it must carry a reason.
	runs := runsAt(30, "Please read these instructions before use and keep them safe.")
	got := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 1)}, nil)

	if got[0].Lang != "" {
		t.Errorf("named %q from text with no distinctive letters", got[0].Lang)
	}
	if got[0].Note == "" {
		t.Error("an unnamed column must explain why")
	}
}

func TestIndistinguishableLanguagesReportTheTie(t *testing.T) {
	// Danish and Norwegian share their whole repertoire. Naming one would be a
	// coin toss; naming both is useful.
	runs := runsAt(30, "Læs denne vejledning grundigt før brug og gem den på et sikkert sted.")
	got := doc.ColumnLanguages(runs, []doc.Column{col(30, 280, 1)}, nil)

	if got[0].Lang != "" {
		t.Errorf("picked %q from an ambiguous alphabet", got[0].Lang)
	}
	if !strings.Contains(got[0].Note, "equally") {
		t.Errorf("the note should name the tie, got %q", got[0].Note)
	}
}

func TestRunsSpanningTwoColumnsBelongToNeither(t *testing.T) {
	// A heading set across the measure is not evidence for either column.
	var runs []doc.TextRun
	runs = append(runs, doc.TextRun{X: 30, Y: 5, Width: 540, Height: 20, Text: "Wskazówki " + polish})
	runs = append(runs, runsAt(30, german, german)...)
	runs = append(runs, runsAt(320, german, german)...)

	got := doc.ColumnLanguages(runs,
		[]doc.Column{col(30, 280, 2), col(320, 570, 2)}, nil)

	for i, c := range got {
		if c.Lang != "de" {
			t.Errorf("column %d = %q, want de — the spanning Polish heading leaked in", i, c.Lang)
		}
	}
}

func TestNoColumnsGivesNoAnswers(t *testing.T) {
	if got := doc.ColumnLanguages(nil, nil, nil); len(got) != 0 {
		t.Errorf("got %d answers for no columns", len(got))
	}
}
