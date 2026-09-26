package review

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
			{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "وَجَدْتُ", Detail: "CATT reads وُجِدَتْ; CAMeL reads وُجِدَت", CATT: "وُجِدَتْ", CAMeL: "وُجِدَت"},
			{Field: "example", Kind: "diacritics", Severity: notes.Minor, Word: "الْمَعْلُومَاتِ", Detail: "CATT reads الْمَعْلُومَاتُ; CAMeL agrees with the card", CATT: "الْمَعْلُومَاتُ", CAMeL: "الْمَعْلُومَاتِ"},
		}},
		{ID: b.ID, Digest: b.Digest(), Issues: []notes.Issue{
			{Field: "example", Kind: "diacritics", Severity: notes.Minor, Word: "سَاعَةً", Detail: "CATT reads سَاعَةٍ; CAMeL agrees with the card", CATT: "سَاعَةٍ", CAMeL: "سَاعَةً"},
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

type harness struct {
	t         *testing.T
	f         fixture
	m         *model
	saves     int
	saveErr   error
	played    []string
	feedbacks []string
	rewrite   func(notes.Note) (notes.Note, error)
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, f: newFixture()}
	items, _ := h.f.items(true)
	opts := Options{
		Save: func([]notes.Note) error {
			if h.saveErr != nil {
				return h.saveErr
			}
			h.saves++
			return nil
		},
		Rewrite: func(_ context.Context, n notes.Note, feedback string) (notes.Note, error) {
			h.feedbacks = append(h.feedbacks, feedback)
			return h.rewrite(n)
		},
		Clip: func(n notes.Note, field string) string {
			if n.ID == "بَاب" && field == "ExampleAudio" {
				return "media/ar-bab.mp3"
			}
			return ""
		},
		Play: func(_ context.Context, path string) error {
			h.played = append(h.played, path)
			return nil
		},
		Editor:     []string{"true"},
		ScratchDir: t.TempDir(),
	}
	h.m = newModel(context.Background(), h.f.ns, items, opts)
	h.m.Update(tea.WindowSizeMsg{Width: 110, Height: 60})
	return h
}

func keyPress(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}

func (h *harness) press(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = h.m.Update(keyPress(k))
	}
	return cmd
}

func (h *harness) deliver(cmd tea.Cmd) {
	for _, msg := range collect(cmd) {
		switch msg.(type) {
		case tickMsg, tea.QuitMsg:
		default:
			h.m.Update(msg)
		}
	}
}

func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func (h *harness) screen() string {
	return ansi.Strip(h.m.View().Content)
}

func (h *harness) sees(want ...string) {
	h.t.Helper()
	screen := h.screen()
	for _, w := range want {
		if !strings.Contains(screen, w) {
			h.t.Errorf("screen lacks %q:\n%s", w, screen)
		}
	}
}

func TestEnterMarksTheCardRightAndMovesOn(t *testing.T) {
	h := newHarness(t)
	h.sees("note 1 of 3", "2 flags to look at", "enter card is right", "c ask Claude")
	h.press("enter")
	if !slices.Equal(h.f.ns[0].Reviewed, []string{"وَجَدْتُ", "الْمَعْلُومَاتِ"}) || h.saves != 1 {
		t.Fatalf("reviewed %q after %d saves", h.f.ns[0].Reviewed, h.saves)
	}
	h.sees("note 2 of 3", "is right as it is", "✓ 1 right")
	h.press("right")
	h.sees("note 3 of 3", "Speech recognition heard something else", "p play sentence")
	h.deliver(h.press("p"))
	if !slices.Equal(h.played, []string{"media/ar-bab.mp3"}) {
		t.Errorf("played %q", h.played)
	}
	h.press("enter")
	if !slices.Equal(h.f.ns[3].ReviewedAudio, []string{"ar-bab.mp3"}) {
		t.Errorf("reviewed audio %q", h.f.ns[3].ReviewedAudio)
	}
	h.sees("note 2 of 3")
	h.press("enter")
	h.sees("All 3 flagged notes are done", "'arabic-vocab build'")
	if s := h.m.summary(); s.Kept != 3 || s.Open != 0 || h.saves != 3 {
		t.Errorf("summary %+v after %d saves", s, h.saves)
	}
}

func TestUndoRestoresTheNote(t *testing.T) {
	h := newHarness(t)
	h.press("enter", "u")
	if h.f.ns[0].Reviewed != nil || h.m.entries[0].state != open || h.m.cur != 0 || h.saves != 2 {
		t.Fatalf("after undo: reviewed %q, state %v, cur %d, saves %d", h.f.ns[0].Reviewed, h.m.entries[0].state, h.m.cur, h.saves)
	}
	h.sees("Undid your last change")
	h.press("u")
	h.sees("Nothing to undo")
}

func TestEditSavesChangesAndKeepsBrokenJSON(t *testing.T) {
	h := newHarness(t)
	h.press("e")
	job := h.m.editing
	if job == nil {
		t.Fatal("e did not start the editor")
	}
	os.WriteFile(job.path, []byte(`{"id": "مَوْقِع",`), 0o600)
	h.m.Update(editedMsg{})
	h.sees("the JSON has a mistake")
	if h.saves != 0 {
		t.Fatalf("broken JSON was saved")
	}

	h.press("e")
	raw, _ := os.ReadFile(h.m.editing.path)
	if string(raw) != `{"id": "مَوْقِع",` {
		t.Fatalf("the broken text was not reopened: %q", raw)
	}
	n := h.f.ns[0]
	n.ID = "other"
	text, _ := encodeNote(n)
	os.WriteFile(h.m.editing.path, text, 0o600)
	h.m.Update(editedMsg{})
	h.sees("the id cannot change")

	h.press("e")
	n = h.f.ns[0]
	n.Example = "<b>وَجَدْتُ</b> الْجَوَابَ."
	text, _ = encodeNote(n)
	os.WriteFile(h.m.editing.path, text, 0o600)
	h.m.Update(editedMsg{})
	if h.f.ns[0].Example != n.Example || h.m.entries[0].state != edited || h.saves != 1 {
		t.Fatalf("edit not saved: %+v, state %v, saves %d", h.f.ns[0], h.m.entries[0].state, h.saves)
	}
	h.sees("Saved your changes", "✎ 1 edited")
}

func TestClaudeCodeSuggestsAVersion(t *testing.T) {
	h := newHarness(t)
	h.rewrite = func(n notes.Note) (notes.Note, error) {
		if n.ID == "كَمْ" {
			return n, errors.New("usage limit reached")
		}
		n.Example = "<b>وَجَدَ</b> الطَّالِبُ الْمَعْلُومَاتِ فِي الْمَوْقِعِ."
		n.ExampleEn = "The student found the information on the website."
		return n, nil
	}
	cmd := h.press("c")
	h.sees("Claude Code is writing a new version", "esc stop Claude Code")
	h.deliver(cmd)
	if !strings.Contains(h.feedbacks[0], "وَجَدْتُ in the example: CATT reads وُجِدَتْ") {
		t.Errorf("feedback = %q", h.feedbacks[0])
	}
	h.sees("Claude Code suggests this version", "sentence", "translation", "y use this version", "n keep yours")
	h.press("n")
	if h.m.entries[0].proposal != nil || h.saves != 0 {
		t.Fatal("n should drop the suggestion without saving")
	}

	h.deliver(h.press("c"))
	h.press("y")
	if !strings.HasPrefix(h.f.ns[0].Example, "<b>وَجَدَ</b>") || h.m.entries[0].state != rewritten || h.saves != 1 {
		t.Fatalf("suggestion not kept: %q, state %v, saves %d", h.f.ns[0].Example, h.m.entries[0].state, h.saves)
	}
	h.sees("note 2 of 3", "✦ 1 rewritten")

	h.deliver(h.press("c"))
	h.sees("Claude Code could not write a new version of كَمْ: usage limit reached")
}

func TestEscStopsClaudeCode(t *testing.T) {
	h := newHarness(t)
	h.rewrite = func(n notes.Note) (notes.Note, error) {
		n.English = "late answer"
		return n, nil
	}
	cmd := h.press("c")
	h.press("esc")
	h.deliver(cmd)
	if h.m.entries[0].asking || h.m.entries[0].proposal != nil {
		t.Fatal("an answer that arrives after esc should be ignored")
	}
	h.sees("Stopped asking Claude Code")
}

func TestAnswersForOtherNotesWaitForYou(t *testing.T) {
	h := newHarness(t)
	h.rewrite = func(n notes.Note) (notes.Note, error) {
		n.English = "a better gloss"
		return n, nil
	}
	cmd := h.press("c")
	h.press("right")
	h.deliver(cmd)
	h.sees("note 2 of 3", "Claude Code's version of مَوْقِع is ready (note 1)")
	h.press("left")
	h.sees("Claude Code suggests this version", "meaning", "a better gloss")
}

func TestSaveFailureStopsTheReview(t *testing.T) {
	h := newHarness(t)
	h.saveErr = errors.New("disk full")
	cmd := h.press("enter")
	if h.m.err == nil || cmd == nil {
		t.Fatal("a failed save should end the review")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected tea.Quit")
	}
	if h.f.ns[0].Reviewed != nil {
		t.Error("the note should stay unchanged when saving fails")
	}
}

func TestKeysFollowTheKeyboardLayout(t *testing.T) {
	msg := tea.KeyPressMsg{Code: 'ث', Text: "ث", BaseCode: 'e'}
	if got := keyName(msg); got != "e" {
		t.Errorf("keyName = %q, want e", got)
	}
	if got := keyName(tea.KeyPressMsg{Code: 'e', Text: "e"}); got != "e" {
		t.Errorf("keyName = %q, want e", got)
	}
}

func TestFlagsAreExplained(t *testing.T) {
	h := newHarness(t)
	h.sees(
		"CATT and CAMeL both read this word with other vowels", "sentence · major",
		"card", "وَجَدْتُ", "وُجِدَتْ", "وُجِدَت",
		"Only CATT reads this word with other vowels", "agrees with the card",
	)
	h.press("right", "right")
	h.sees("اغلق الباب الان", "heard", "If it sounds right, the card is right")
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

func TestNothingToReviewShowsTheSummary(t *testing.T) {
	m := newModel(context.Background(), nil, nil, Options{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if !strings.Contains(ansi.Strip(m.View().Content), "flagged notes are done") {
		t.Error("an empty review should show the summary")
	}
}
