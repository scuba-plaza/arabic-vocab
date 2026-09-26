package review

import (
	"bufio"
	"context"
	"errors"
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
	audio  []notes.AudioCheck
	index  map[string]string
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
		{ID: a.ID, Digest: a.Digest(), Issues: []notes.Issue{
			{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "وَجَدْتُ", Detail: "CATT reads وُجِدَتْ; CAMeL reads وُجِدَت"},
			{Field: "example", Kind: "diacritics", Severity: notes.Minor, Word: "الْمَعْلُومَاتِ", Detail: "CATT reads الْمَعْلُومَاتُ; CAMeL agrees with the card"},
		}},
		{ID: b.ID, Digest: b.Digest(), Issues: []notes.Issue{
			{Field: "example", Kind: "diacritics", Severity: notes.Minor, Word: "سَاعَةً", Detail: "CATT reads سَاعَةٍ; CAMeL agrees with the card"},
		}},
		{ID: c.ID, Digest: "stale", Issues: []notes.Issue{{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "قَدِيمٌ"}}},
		{ID: d.ID, Digest: d.Digest()},
		{ID: e.ID, Digest: e.Digest(), Issues: []notes.Issue{{Field: "forms", Kind: "invalid", Severity: notes.Major, Word: "عَشْر"}}},
	}
	dText := deck.AudioTexts(d)[1].Text
	index := map[string]string{dText: "ar-bab.mp3"}
	audio := []notes.AudioCheck{{ID: d.ID, Field: "ExampleAudio", Text: dText, File: "ar-bab.mp3", Transcript: "اغلق الباب الان"}}
	return fixture{ns: ns, checks: checks, audio: audio, index: index}
}

func (f fixture) items(minor bool) ([]Item, int) {
	return Items(f.ns, f.checks, f.audio, f.index, minor)
}

func TestItemsCollectsUnreviewedFlags(t *testing.T) {
	f := newFixture()
	items, stale := f.items(true)
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
	if items[0].Len() != 2 || len(items[2].Audio) != 1 {
		t.Errorf("items = %+v", items)
	}

	items, _ = f.items(false)
	if len(items) != 2 || items[0].Len() != 1 || items[1].Index != 3 {
		t.Fatalf("without minor flags: %+v", items)
	}
}

type script struct {
	saves     int
	edit      func(notes.Note) (notes.Note, error)
	rewrite   func(context.Context, notes.Note, string) (notes.Note, error)
	played    []string
	feedbacks []string
}

func (sc *script) session(input string, out *strings.Builder) *Session {
	return &Session{
		In:   bufio.NewReader(strings.NewReader(input)),
		Out:  out,
		Save: func([]notes.Note) error { sc.saves++; return nil },
		Edit: sc.edit,
		Rewrite: func(ctx context.Context, n notes.Note, feedback string) (notes.Note, error) {
			sc.feedbacks = append(sc.feedbacks, feedback)
			return sc.rewrite(ctx, n, feedback)
		},
		Clip: func(n notes.Note) string {
			if n.ID == "بَاب" {
				return "media/ar-bab.mp3"
			}
			return ""
		},
		Play: func(path string) error { sc.played = append(sc.played, path); return nil },
	}
}

func TestSessionAcceptsFlagsAndSkips(t *testing.T) {
	f := newFixture()
	items, _ := f.items(true)
	sc := &script{}
	var out strings.Builder
	sum, err := sc.session("a 1\na\n\np\na\n", &out).Run(context.Background(), f.ns, items)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Accepted != 2 || sum.Skipped != 1 || sum.Left != 0 || sc.saves != 3 {
		t.Fatalf("summary %+v, saves %d\n%s", sum, sc.saves, out.String())
	}
	if !slices.Equal(f.ns[0].Reviewed, []string{"وَجَدْتُ", "الْمَعْلُومَاتِ"}) {
		t.Errorf("reviewed = %q", f.ns[0].Reviewed)
	}
	if f.ns[1].Reviewed != nil {
		t.Errorf("a skipped note changed: %q", f.ns[1].Reviewed)
	}
	if !slices.Equal(f.ns[3].ReviewedAudio, []string{"ar-bab.mp3"}) || !slices.Equal(sc.played, []string{"media/ar-bab.mp3"}) {
		t.Errorf("audio reviewed %q, played %q", f.ns[3].ReviewedAudio, sc.played)
	}
	for _, want := range []string{"1 of 3 · position 1", "major  example  وَجَدْتُ", "heard: اغلق الباب الان", "[p] play example", "[a N] accept flag N"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestSessionEditsNotes(t *testing.T) {
	f := newFixture()
	items, _ := f.items(true)
	sc := &script{edit: func(n notes.Note) (notes.Note, error) {
		if n.ID == "كَمْ" {
			n.ID = "other"
			return n, nil
		}
		n.Example = "<b>وَجَدْتُ</b> الْجَوَابَ."
		return n, nil
	}}
	var out strings.Builder
	sum, err := sc.session("e\ne\ns\nq\n", &out).Run(context.Background(), f.ns, items)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Edited != 1 || sum.Skipped != 1 || sum.Left != 1 || sc.saves != 1 {
		t.Fatalf("summary %+v, saves %d\n%s", sum, sc.saves, out.String())
	}
	if f.ns[0].Example != "<b>وَجَدْتُ</b> الْجَوَابَ." || f.ns[1].ID != "كَمْ" {
		t.Errorf("notes = %+v / %+v", f.ns[0], f.ns[1])
	}
	if !strings.Contains(out.String(), "not saved: the id cannot change") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestSessionAsksClaudeCodeForANewVersion(t *testing.T) {
	f := newFixture()
	items, _ := f.items(true)
	sc := &script{rewrite: func(_ context.Context, n notes.Note, _ string) (notes.Note, error) {
		if n.ID == "كَمْ" {
			return n, errors.New("claude: usage limit reached")
		}
		n.Example = "<b>وَجَدَ</b> الطَّالِبُ الْمَعْلُومَاتِ."
		return n, nil
	}}
	var out strings.Builder
	sum, err := sc.session("c\nn\nc\ny\nc\ns\n", &out).Run(context.Background(), f.ns, items)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Rewritten != 1 || sum.Skipped != 1 || sum.Left != 1 || sc.saves != 1 {
		t.Fatalf("summary %+v, saves %d\n%s", sum, sc.saves, out.String())
	}
	if f.ns[0].Example != "<b>وَجَدَ</b> الطَّالِبُ الْمَعْلُومَاتِ." {
		t.Errorf("example = %q", f.ns[0].Example)
	}
	if len(sc.feedbacks) != 3 || !strings.Contains(sc.feedbacks[0], "وَجَدْتُ in the example: CATT reads وُجِدَتْ") {
		t.Errorf("feedback = %q", sc.feedbacks)
	}
	for _, want := range []string{"kept the current version", "new version:", "no new version: claude: usage limit reached"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestSessionHandlesBadInputAndEndOfInput(t *testing.T) {
	f := newFixture()
	items, _ := f.items(true)
	sc := &script{}
	var out strings.Builder
	sum, err := sc.session("x\na 9\n", &out).Run(context.Background(), f.ns, items)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Left != 3 || sc.saves != 0 {
		t.Fatalf("summary %+v, saves %d", sum, sc.saves)
	}
	for _, want := range []string{`unknown choice "x"`, "pick flags between 1 and 2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestFeedbackDescribesEveryFlag(t *testing.T) {
	it := Item{
		Issues: []notes.Issue{{Field: "example", Kind: "unchecked", Detail: "CATT's reading could not be aligned with the sentence"}},
		Audio:  []notes.AudioCheck{{Field: "ExampleAudio", Transcript: "فذهبت الى السوق"}},
	}
	got := Feedback(it)
	for _, want := range []string{"- the example: CATT's reading could not be aligned", `heard "فذهبت الى السوق" when the example audio`, "rewrite the example so it reads only one way"} {
		if !strings.Contains(got, want) {
			t.Errorf("feedback lacks %q:\n%s", want, got)
		}
	}
}
