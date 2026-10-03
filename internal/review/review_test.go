package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func note(id string, pos int, example string) notes.Note {
	return notes.Note{ID: id, Position: pos, Arabic: id, Pos: "noun", English: "word", Example: example, ExampleEn: "A sentence."}
}

type fixture struct {
	ns     []notes.Note
	checks []notes.Check
}

func newFixture() fixture {
	a := note("مَوْقِع", 1, "<b>وَجَدْتُ</b> الْمَعْلُومَاتِ فِي الْمَوْقِعِ.")
	b := note("كَمْ", 2, "<b>كَمْ</b> سَاعَةً تَعْمَلُ؟")
	c := note("قَدِيم", 3, "هٰذَا بَيْتٌ <b>قَدِيمٌ</b>.")
	d := note("بَاب", 4, "أَغْلِقِ <b>الْبَابَ</b>.")
	e := note("عَشَرَة", 5, "عِنْدِي <b>عَشَرَةُ</b> كُتُبٍ.")
	e.Reviewed = []string{"عَشْر"}
	f := notes.Note{ID: "جَدِيد", Position: 6, Arabic: "جَدِيد", Pos: "adj"}
	ns := []notes.Note{a, b, c, d, e, f}
	checks := []notes.Check{
		{ID: a.ID, Version: deck.CheckVersion, Digest: a.Digest(), Issues: []notes.Issue{
			{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "وَجَدْتُ", Detail: "CATT reads وُجِدَتْ; CAMeL reads وُجِدَت", CATT: "وُجِدَتْ", CAMeL: "وُجِدَت"},
			{Field: "example", Kind: "diacritics", Severity: notes.Minor, Word: "الْمَعْلُومَاتِ", Detail: "CATT reads الْمَعْلُومَاتُ; CAMeL agrees with the card", CATT: "الْمَعْلُومَاتُ", CAMeL: "الْمَعْلُومَاتِ"},
		}},
		{ID: b.ID, Version: deck.CheckVersion, Digest: b.Digest(), Issues: []notes.Issue{
			{Field: "example", Kind: "diacritics", Severity: notes.Minor, Word: "سَاعَةً", Detail: "CATT reads سَاعَةٍ; CAMeL agrees with the card", CATT: "سَاعَةٍ", CAMeL: "سَاعَةً"},
		}},
		{ID: c.ID, Version: deck.CheckVersion, Digest: "stale", Issues: []notes.Issue{{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "قَدِيمٌ"}}},
		{ID: d.ID, Version: deck.CheckVersion, Digest: d.Digest(), Issues: []notes.Issue{
			{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "الْبَابَ", Detail: "CATT reads الْبَابِ; CAMeL reads الْبَابُ", CATT: "الْبَابِ", CAMeL: "الْبَابُ"},
		}},
		{ID: e.ID, Version: deck.CheckVersion, Digest: e.Digest(), Issues: []notes.Issue{{Field: "forms", Kind: "invalid", Severity: notes.Major, Word: "عَشْر"}}},
	}
	return fixture{ns: ns, checks: checks}
}

func (f fixture) items(filter Filter) ([]Item, int) {
	return Items(f.ns, f.checks, filter)
}

func TestItemsCollectsUnreviewedFlags(t *testing.T) {
	f := newFixture()
	items, stale := f.items(Filter{Minor: true})
	if stale != 1 {
		t.Errorf("stale = %d, want 1", stale)
	}
	var got []string
	for _, it := range items {
		got = append(got, f.ns[it.Index].ID)
	}
	if !slices.Equal(got, []string{"مَوْقِع", "كَمْ", "بَاب"}) {
		t.Fatalf("items = %v", got)
	}
	if items[0].Len() != 2 || items[2].Len() != 1 {
		t.Errorf("items = %+v", items)
	}

	items, _ = f.items(Filter{})
	if len(items) != 2 || items[0].Len() != 1 || items[1].Index != 3 {
		t.Fatalf("without minor flags: %+v", items)
	}
}

func TestItemsWithAllKeepsEveryWrittenNote(t *testing.T) {
	f := newFixture()
	items, stale := f.items(Filter{Minor: true, All: true})
	if stale != 1 {
		t.Errorf("stale = %d, want 1", stale)
	}
	var got []string
	for _, it := range items {
		got = append(got, f.ns[it.Index].ID)
	}
	if !slices.Equal(got, []string{"مَوْقِع", "كَمْ", "قَدِيم", "بَاب", "عَشَرَة"}) {
		t.Fatalf("items = %v", got)
	}
	if !items[2].Stale || items[2].Len() != 0 {
		t.Errorf("a note the check has not seen since it changed: %+v", items[2])
	}
	if items[4].Len() != 0 || toneOf(items[4]) != "plain" {
		t.Errorf("a note with nothing open: %+v", items[4])
	}
}

func TestFeedbackDescribesEveryFlag(t *testing.T) {
	it := Item{
		Issues: []notes.Issue{{Field: "example", Kind: "unchecked", Detail: "CATT's reading could not be aligned with the sentence"}},
	}
	got := Feedback(it)
	for _, want := range []string{"- the example: CATT's reading could not be aligned", "rewrite the example so it reads only one way"} {
		if !strings.Contains(got, want) {
			t.Errorf("feedback lacks %q:\n%s", want, got)
		}
	}
	if got := Feedback(Item{}); !strings.Contains(got, "Nothing was flagged") || strings.Contains(got, "disagreed") {
		t.Errorf("feedback for a note without flags:\n%s", got)
	}
}
