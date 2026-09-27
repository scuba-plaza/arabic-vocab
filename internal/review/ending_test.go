package review

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func ending(word string, missing int, catt, camel string) notes.Issue {
	return notes.Issue{Field: "example", Kind: "unmarked", Severity: notes.Major, Word: word, Missing: []int{missing}, CATT: catt, CAMeL: camel}
}

func TestEndingFixesAddOnlyTheEnding(t *testing.T) {
	n := notes.Note{Example: "<b>صَبَاح الخَيْر</b>، كَيْفَ حَالُكَ؟"}
	issues := []notes.Issue{ending("صَبَاح", 3, "صَبَاحُ", "صَباحُ"), ending("الخَيْر", 4, "الْخَيْرِ", "الخَيْرِ")}
	fixes := endingFixes(n, issues)
	if len(fixes) != 1 || fixes[0].Source != "both" || fixes[0].Example != "<b>صَبَاحُ الخَيْرِ</b>، كَيْفَ حَالُكَ؟" {
		t.Fatalf("when CATT and CAMeL agree there is one fix that keeps the card's other marks: %+v", fixes)
	}
	if !slices.Equal(fixes[0].Words, []string{"صَبَاحُ", "الخَيْرِ"}) {
		t.Errorf("words = %q", fixes[0].Words)
	}

	n.Example = "بَدَا أَنَّهُ <b>تَعِب</b>."
	fixes = endingFixes(n, []notes.Issue{ending("تَعِب", 2, "تَعَبٌ", "تَعِبَ")})
	if len(fixes) != 2 || fixes[0].Source != "catt" || fixes[0].Example != "بَدَا أَنَّهُ <b>تَعِبٌ</b>." || fixes[1].Example != "بَدَا أَنَّهُ <b>تَعِبَ</b>." {
		t.Fatalf("fixes = %+v", fixes)
	}

	for _, example := range []string{"<i>بَدَا</i> أَنَّهُ <b>تَعِب</b>.", "بَدَا أَنَّهُ <b>تَعِبٌ</b>."} {
		n.Example = example
		if fixes := endingFixes(n, []notes.Issue{ending("تَعِب", 2, "تَعَبٌ", "")}); len(fixes) != 0 {
			t.Errorf("%s: no fix when the sentence has other markup or no longer has the bare word: %+v", example, fixes)
		}
	}
	n.Example = "بَدَا أَنَّهُ <b>تَعِب</b>."
	inside := notes.Issue{Field: "example", Kind: "unmarked", Severity: notes.Major, Word: "تَعِب", Missing: []int{0, 2}, CATT: "تَعِبٌ"}
	if fixes := endingFixes(n, []notes.Issue{inside}); len(fixes) != 0 {
		t.Errorf("a flag with more than the ending missing needs a real edit: %+v", fixes)
	}
}

func TestEndingActionSavesTheFixedSentence(t *testing.T) {
	n := notes.Note{ID: "بَدَا", Position: 166, Arabic: "بَدَا", Pos: "verb", English: "to appear", Example: "<b>بَدَا</b> أَنَّهُ تَعِب.", ExampleEn: "It appeared that he was tired."}
	ns := []notes.Note{n}
	var saved []notes.Note
	opts := Options{Save: func(ns []notes.Note) error { saved = slices.Clone(ns); return nil }}
	h := &harness{t: t}
	h.s = newSession(context.Background(), ns, []Item{{Index: 0, Issues: []notes.Issue{ending("تَعِب", 2, "تَعَبٌ", "تَعِبَ")}}}, opts)
	h.h = h.s.handler("tok", "127.0.0.1:9999", func() {})

	e := h.state(0).Entry
	if len(e.Fixes) != 2 || e.Fixes[1].Source != "camel" || !slices.Equal(e.Fixes[1].Words, []string{"تَعِبَ"}) {
		t.Fatalf("fixes = %+v", e.Fixes)
	}
	if code, res := h.post("ending", 0, map[string]string{"source": "nobody"}); code != http.StatusBadRequest || !strings.Contains(res.Message, "no ending to add") {
		t.Fatalf("unknown source: %d %+v", code, res)
	}
	code, res := h.post("ending", 0, map[string]string{"source": "catt"})
	if code != http.StatusOK || res.Message != "✎ Added CATT's ending: تَعِبٌ; run check again before building" {
		t.Fatalf("ending: %d %+v", code, res)
	}
	if len(saved) != 1 || saved[0].Example != "<b>بَدَا</b> أَنَّهُ تَعِبٌ." {
		t.Fatalf("saved %+v", saved)
	}
	after := h.state(0).Entry
	if after.State != "edited" || len(after.Fixes) != 0 {
		t.Errorf("after the fix the note is edited and offers nothing more: %+v", after)
	}
	if code, _ := h.post("undo", 0, nil); code != http.StatusOK || saved[0].Example != n.Example {
		t.Errorf("undo should bring the sentence back: %q", saved[0].Example)
	}
}
