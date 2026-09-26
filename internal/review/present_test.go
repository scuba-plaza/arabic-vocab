package review

import (
	"slices"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

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
