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
	audio  []notes.Check
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
	return Items(f.ns, f.checks, f.audio, filter)
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

func silent(field string) notes.Issue {
	return deck.SilentIssue(field, 4)
}

func audioCheckFor(n notes.Note, issues ...notes.Issue) notes.Check {
	return notes.Check{ID: n.ID, Version: deck.AudioCheckVersion, Digest: n.Digest(), Issues: issues}
}

func TestItemsIncludeClipsWithoutSoundAfterTheVowelFlags(t *testing.T) {
	f := newFixture()
	f.audio = []notes.Check{audioCheckFor(f.ns[4], silent("WordAudio")), audioCheckFor(f.ns[3], silent("ExampleAudio"))}
	items, _ := f.items(Filter{})
	var got []string
	for _, it := range items {
		got = append(got, f.ns[it.Index].ID)
	}
	if !slices.Equal(got, []string{"مَوْقِع", "بَاب", "عَشَرَة"}) {
		t.Fatalf("items = %v: a silent clip flags a note even without minor flags", got)
	}
	if items[1].Len() != 2 || items[1].Issues[0].Kind != "diacritics" || items[1].Issues[1].Kind != deck.KindSilent {
		t.Errorf("a note with both kinds of flag lists the vowels first: %+v", items[1].Issues)
	}
	if items[2].Len() != 1 || items[2].Issues[0].Field != "WordAudio" || !items[2].Major() || toneOf(items[2]) != "bad" {
		t.Errorf("a note with only a silent clip: %+v", items[2])
	}
}

func TestItemsIgnoreClipFlagsThatNoLongerApply(t *testing.T) {
	f := newFixture()
	e := f.ns[4]
	stale := audioCheckFor(e, silent("WordAudio"))
	stale.Digest = "an older text"
	older := audioCheckFor(f.ns[1], silent("WordAudio"))
	older.Version = deck.AudioCheckVersion + 1
	f.audio = []notes.Check{stale, older, audioCheckFor(notes.Note{ID: "nobody"}, silent("WordAudio"))}
	items, _ := f.items(Filter{Minor: true})
	for _, it := range items {
		for _, is := range it.Issues {
			if is.Kind == deck.KindSilent {
				t.Errorf("%s still shows %+v", f.ns[it.Index].ID, is)
			}
		}
	}
	if len(items) != 3 {
		t.Errorf("items = %d", len(items))
	}
}

func TestClipFlagsDoNotChangeWhichNotesAreStale(t *testing.T) {
	f := newFixture()
	f.audio = []notes.Check{audioCheckFor(f.ns[2], silent("WordAudio"))}
	_, stale := f.items(Filter{Minor: true})
	if stale != 1 {
		t.Errorf("stale = %d, want the one note whose vowel check is out of date", stale)
	}
}

func TestFeedbackLeavesClipFlagsOut(t *testing.T) {
	it := Item{Issues: []notes.Issue{silent("ExampleAudio")}}
	got := Feedback(it)
	if !strings.Contains(got, "Nothing was flagged") || strings.Contains(got, "ExampleAudio") || strings.Contains(got, "silent") || strings.Contains(got, "disagreed") {
		t.Errorf("feedback for a silent clip:\n%s", got)
	}
	mixed := Item{Issues: []notes.Issue{{Field: "example", Kind: "unchecked", Detail: "CATT's reading could not be aligned"}, silent("WordAudio")}}
	got = Feedback(mixed)
	if !strings.Contains(got, "disagreed with this card") || !strings.Contains(got, "- the example: CATT's reading") || strings.Contains(got, "WordAudio") {
		t.Errorf("feedback with both:\n%s", got)
	}
}

func TestItemsShowASilentFlagWhateverTheNoteHasReviewed(t *testing.T) {
	f := newFixture()
	f.ns[4].Reviewed = []string{"عَشْر", "WordAudio:silent"}
	f.audio = []notes.Check{audioCheckFor(f.ns[4], silent("WordAudio"))}
	items, _ := f.items(Filter{})
	var got []notes.Issue
	for _, it := range items {
		if f.ns[it.Index].ID == "عَشَرَة" {
			got = it.Issues
		}
	}
	if len(got) != 1 || got[0].Kind != deck.KindSilent {
		t.Errorf("a clip without sound cannot be reviewed away: %+v", got)
	}
}
