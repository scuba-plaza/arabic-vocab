package review

import (
	"slices"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func TestUnmarkedFlagsShowTheBareLettersAndTheReadings(t *testing.T) {
	ending := issueView(notes.Issue{Field: "example", Kind: "unmarked", Severity: notes.Major, Word: "جَمِيل", Missing: []int{3}, CATT: "جَمِيلٌ", CAMeL: "جَمِيلَ"}, false)
	if ending.Title != "The ending has no vowel mark" || len(ending.Rows) != 3 {
		t.Fatalf("ending flag %+v", ending)
	}
	for _, r := range ending.Rows {
		if len(r.Spans) != 2 || r.Spans[0].C != "" || r.Spans[1].C != "mark-bad" {
			t.Errorf("the %s row should mark only the last letter: %+v", r.Label, r.Spans)
		}
	}
	inside := issueView(notes.Issue{Field: "arabic", Kind: "unmarked", Severity: notes.Major, Word: "مَكتَبَة", Missing: []int{1}}, false)
	if inside.Title != "Some letters have no vowel mark" || len(inside.Rows) != 1 {
		t.Fatalf("inside flag %+v", inside)
	}
	if spans := inside.Rows[0].Spans; !slices.Equal(classes(spans), []string{"mark-bad"}) || spans[1].T != "ك" {
		t.Errorf("only ك should be marked: %+v", spans)
	}
}

func TestChangesOnlyMarkWordsWhenBothSidesHaveText(t *testing.T) {
	cur := notes.Note{Arabic: "كِتَاب", Pos: "noun", English: "book", Example: "<b>كِتَابٌ</b> جَدِيدٌ.", ExampleEn: "A new book."}
	fresh := cur
	fresh.Hint = "a written work"
	fresh.ExampleEn = "A thick new book."
	cs := changes(cur, fresh)
	if len(cs) != 2 || cs[0].Label != "hint" || cs[1].Label != "translation" {
		t.Fatalf("changes = %+v", cs)
	}
	if len(cs[0].Old) != 0 || len(classes(cs[0].New)) != 0 || cs[0].New[0].T != "a written work" {
		t.Errorf("a new hint should not be marked word by word: %+v", cs[0])
	}
	if !slices.Equal(classes(cs[1].New), []string{"ins"}) || len(classes(cs[1].Old)) != 0 {
		t.Errorf("translation diff: old %+v new %+v", cs[1].Old, cs[1].New)
	}
}
