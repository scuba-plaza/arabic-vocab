package curate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

type fakeModel struct {
	mu       sync.Mutex
	requests []Request
	respond  func(req Request, call int) (*Response, error)
}

func (f *fakeModel) Complete(_ context.Context, req Request) (*Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	call := len(f.requests)
	f.mu.Unlock()
	return f.respond(req, call)
}

func answer(cards ...Card) *Response {
	return &Response{
		Output: marshal(map[string][]Card{"cards": cards}),
		Usage:  Usage{Calls: 1, Input: 100, Output: 50, CacheRead: 2000, Models: []string{"test-model"}},
	}
}

func at(c Card, position int) Card {
	c.Position = position
	return c
}

var kitab = Card{
	Arabic: "كِتَاب", Pos: "noun", English: "book", Forms: []notes.Form{{Label: "pl.", Arabic: "كُتُب"}},
	Example: "قَرَأْتُ <b>الْكِتَابَ</b> أَمْسِ.", ExampleEn: "I read the book yesterday.",
}

var kataba = Card{
	Arabic: "كَتَبَ", Pos: "verb", English: "to write",
	Forms:   []notes.Form{{Label: "pres.", Arabic: "يَكْتُبُ"}, {Label: "masdar", Arabic: "كِتَابَة"}},
	Example: "<b>كَتَبَ</b> أَخِي رِسَالَةً طَوِيلَةً.", ExampleEn: "My brother wrote a long letter.",
}

var qalam = Card{
	Arabic: "قَلَم", Pos: "noun", English: "pen", Forms: []notes.Form{{Label: "pl.", Arabic: "أَقْلَام"}},
	Example: "هٰذَا <b>قَلَمٌ</b> جَدِيدٌ.", ExampleEn: "This is a new pen.",
}

func sampleNotes() []notes.Note {
	return []notes.Note{
		{ID: "فِي", Position: 1, Arabic: "فِي", Pos: "prep", English: "in", Example: "<b>فِي</b> الْبَيْتِ.", ExampleEn: "In the house."},
		{ID: "كِتَاب", Position: 2, Arabic: "كِتَاب", Pos: "noun", Gender: "m", CEFR: "A1"},
		{ID: "كَتَبَ", Position: 3, Arabic: "كَتَبَ", Pos: "verb", VerbForm: "I"},
		{ID: "قَلَم", Position: 4, Arabic: "قَلَم", Pos: "noun"},
		{ID: "بَاب", Position: 5, Arabic: "بَاب", Pos: "noun"},
	}
}

func sampleRecords() map[string]*rank.Record {
	return map[string]*rank.Record{
		"كِتَاب": {Rank: 2, ID: "كِتَاب", Entries: []*lexicon.Entry{{Title: "كتاب", Pos: "noun", Canonical: "كِتَاب", Senses: []lexicon.Sense{{Gloss: "book", MSA: true}}}}},
	}
}

func TestRunFillsSelectedNotesInOneBatch(t *testing.T) {
	ns := sampleNotes()
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		return answer(at(kataba, 3), at(kitab, 2)), nil
	}}
	var saves int
	opts := Options{System: System(nil, []string{"فِي", "كِتَاب"}), Save: func([]notes.Note) error { saves++; return nil }}
	res, err := Run(context.Background(), fake, ns, []int{1, 2}, sampleRecords(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 2 || len(res.Failed) != 0 || saves != 2 || len(fake.requests) != 1 {
		t.Fatalf("curated %d, failed %v, saves %d, calls %d", res.Curated, res.Failed, saves, len(fake.requests))
	}
	if ns[0].English != "in" || ns[3].Authored() {
		t.Fatalf("notes outside the targets changed: %+v %+v", ns[0], ns[3])
	}
	if ns[1].English != "book" || ns[1].Example != kitab.Example || ns[1].Gender != "m" || len(ns[1].Forms) != 1 {
		t.Fatalf("kitab note = %+v", ns[1])
	}
	if ns[2].English != "to write" || ns[2].VerbForm != "I" {
		t.Fatalf("kataba note = %+v", ns[2])
	}
	req := fake.requests[0]
	for _, want := range []string{"these 2 drafts", "## Position 2 (CEFR A1)", "## Position 3", `"arabic":"كِتَاب"`, `"gloss":"book"`} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("prompt lacks %s:\n%s", want, req.Prompt)
		}
	}
	if !strings.HasPrefix(req.System, "You write flashcards") || !strings.HasSuffix(req.System, "فِي\nكِتَاب\n") {
		t.Errorf("system prompt = %q", req.System)
	}
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(req.Schema, &s); err != nil || len(s.Required) != 1 || s.Required[0] != "cards" {
		t.Errorf("schema = %s", req.Schema)
	}
	if res.Usage.Calls != 1 || res.Usage.Input != 100 || res.Usage.CacheRead != 2000 || len(res.Usage.Models) != 1 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestRunRetriesOnlyRejectedCards(t *testing.T) {
	ns := sampleNotes()
	bad := at(kataba, 3)
	bad.Example = "<b>كَتَبَ</b> <b>أَخِي</b>."
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		if call == 1 {
			return answer(at(kitab, 2), bad), nil
		}
		if strings.Contains(req.Prompt, "## Position 2") {
			t.Errorf("an accepted card was sent again:\n%s", req.Prompt)
		}
		for _, want := range []string{"these 2 drafts", "exactly one <b>…</b>", "the answer had no card for this position"} {
			if !strings.Contains(req.Prompt, want) {
				t.Errorf("retry prompt lacks %q:\n%s", want, req.Prompt)
			}
		}
		return answer(at(kataba, 3), at(qalam, 4)), nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1, 2, 3}, sampleRecords(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 3 || len(fake.requests) != 2 || ns[2].Example != kataba.Example || ns[3].English != "pen" {
		t.Fatalf("curated %d after %d calls; notes %+v %+v", res.Curated, len(fake.requests), ns[2], ns[3])
	}
}

func TestRunSplitsTargetsIntoBatches(t *testing.T) {
	ns := sampleNotes()
	byPosition := map[int]Card{2: kitab, 3: kataba, 4: qalam, 5: at(qalam, 5)}
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		var cards []Card
		for p, c := range byPosition {
			if strings.Contains(req.Prompt, "## Position "+string(rune('0'+p))) {
				cards = append(cards, at(c, p))
			}
		}
		return answer(cards...), nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1, 2, 3, 4}, nil, Options{Batch: 3, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 4 || len(fake.requests) != 2 || !strings.Contains(fake.requests[1].Prompt, "for this draft") {
		t.Fatalf("curated %d in %d calls", res.Curated, len(fake.requests))
	}
}

func TestRunGivesUpAfterAttempts(t *testing.T) {
	ns := sampleNotes()
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		return &Response{Usage: Usage{Calls: 1}}, nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1, 2}, sampleRecords(), Options{Attempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 0 || len(res.Failed) != 2 || len(fake.requests) != 3 {
		t.Fatalf("curated %d, failed %v, calls %d", res.Curated, res.Failed, len(fake.requests))
	}
	if res.Failed[0].Position != 2 || res.Failed[1].Position != 3 || ns[1].Authored() {
		t.Fatalf("failures = %+v", res.Failed)
	}
}

func TestRunStopsWhenClaudeFails(t *testing.T) {
	ns := sampleNotes()
	limit := errors.New("claude: Claude AI usage limit reached")
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		if call == 1 {
			return answer(at(kitab, 2)), nil
		}
		return nil, limit
	}}
	var saved int
	res, err := Run(context.Background(), fake, ns, []int{1, 2, 3}, sampleRecords(), Options{Batch: 1, Concurrency: 1, Save: func([]notes.Note) error { saved++; return nil }})
	if !errors.Is(err, limit) {
		t.Fatalf("err = %v", err)
	}
	if res.Curated != 1 || saved != 1 || ns[1].English != "book" {
		t.Fatalf("work before the failure should be kept: curated %d, saved %d", res.Curated, saved)
	}
}

func TestParseCards(t *testing.T) {
	cards, err := ParseCards(json.RawMessage(`{"cards":[{"position":2,"arabic":"a"},{"position":2,"arabic":"b"}]}`))
	if err != nil || cards[2].Arabic != "a" {
		t.Fatalf("cards = %+v, err = %v", cards, err)
	}
	for _, raw := range []string{"", "null", `{"cards":[{"position":1,"surprise":true}]}`, `[1,2]`} {
		if _, err := ParseCards(json.RawMessage(raw)); err == nil {
			t.Errorf("ParseCards(%q) should fail", raw)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Card)
		want   string
	}{
		{"valid", func(c *Card) {}, ""},
		{"no english", func(c *Card) { c.English = " " }, "english is empty"},
		{"latin headword", func(c *Card) { c.Arabic = "kitāb" }, "arabic has no Arabic text"},
		{"mixed headword", func(c *Card) { c.Arabic = "كِتَاب (kitāb)" }, "arabic contains Latin letters"},
		{"no bold", func(c *Card) { c.Example = "قَرَأْتُ الْكِتَابَ." }, "exactly one <b>…</b>"},
		{"empty bold", func(c *Card) { c.Example = "قَرَأْتُ <b> </b> الْكِتَابَ." }, "is empty"},
		{"other markup", func(c *Card) { c.Example = "<i>قَرَأْتُ</i> <b>الْكِتَابَ</b>." }, "contains markup"},
		{"bare ending", func(c *Card) { c.Example = "قَرَأْتُ <b>الْكِتَاب</b> أَمْس." }, "the ending of الْكِتَاب، أَمْس without a vowel mark"},
		{"ending after the bold", func(c *Card) { c.Example = "قَرَأْتُ <b>الْكِتَاب</b>َ أَمْسِ." }, ""},
		{"tanween on the alif", func(c *Card) { c.Example = "قَرَأْتُ <b>كِتَاباً</b> أَمْسِ." }, ""},
		{"unlabelled form", func(c *Card) { c.Forms = []notes.Form{{Arabic: "كُتُب"}} }, "has no label"},
		{"no translation", func(c *Card) { c.ExampleEn = "" }, "example_en is empty"},
	}
	for _, tc := range cases {
		c := kitab
		tc.change(&c)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestApplySwitchingPartOfSpeech(t *testing.T) {
	n := notes.Note{ID: "x", Position: 9, Arabic: "بَعْد", Pos: "noun", Gender: "m", Reviewed: []string{"بَعْدَ"}}
	c := Card{Position: 9, Arabic: "بَعْدَ", Pos: "prep", English: "after", Example: "<b>بَعْدَ</b> الدَّرْسِ.", ExampleEn: "After the lesson.", Forms: []notes.Form{}}
	got := Apply(n, c)
	if got.Pos != "prep" || got.Arabic != "بَعْدَ" || got.Reviewed != nil || got.Forms != nil || got.ID != "x" || got.Position != 9 {
		t.Fatalf("applied = %+v", got)
	}
	v := Apply(notes.Note{Pos: "noun", Gender: "f"}, Card{Pos: "verb"})
	if v.Gender != "" {
		t.Fatalf("a verb kept its gender: %+v", v)
	}
}

func TestPickExamplesPrefersDistinctPartsOfSpeech(t *testing.T) {
	ns := []notes.Note{
		{ID: "a", Position: 1, Pos: "prep", English: "e", Example: "x", ExampleEn: "y"},
		{ID: "b", Position: 2, Pos: "prep", English: "e", Example: "x", ExampleEn: "y"},
		{ID: "c", Position: 3, Pos: "noun"},
		{ID: "d", Position: 4, Pos: "verb", English: "e", Example: "x", ExampleEn: "y"},
	}
	got := PickExamples(ns, 2)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "d" {
		t.Fatalf("examples = %+v", got)
	}
	if got := PickExamples(ns, 5); len(got) != 3 || got[1].ID != "b" {
		t.Fatalf("examples = %+v", got)
	}
}

func TestSystemShowsExamplesWithoutEscaping(t *testing.T) {
	ex := sampleNotes()[:1]
	s := System(ex, nil)
	if !strings.Contains(s, `"example":"<b>فِي</b> الْبَيْتِ."`) || !strings.Contains(s, `"position":1`) {
		t.Fatalf("system prompt = %s", s)
	}
}

func TestRunPassesFeedbackToEveryAttempt(t *testing.T) {
	ns := sampleNotes()
	bad := at(kitab, 2)
	bad.English = ""
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		if !strings.Contains(req.Prompt, "The checker flagged قَرَأْتُ.") {
			t.Errorf("call %d lacks the feedback:\n%s", call, req.Prompt)
		}
		if call == 1 {
			return answer(bad), nil
		}
		if !strings.Contains(req.Prompt, "rejected: english is empty") {
			t.Errorf("retry lacks the rejection:\n%s", req.Prompt)
		}
		return answer(at(kitab, 2)), nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1}, nil, Options{Feedback: map[int]string{2: "The checker flagged قَرَأْتُ."}})
	if err != nil || res.Curated != 1 || len(fake.requests) != 2 {
		t.Fatalf("curated %d in %d calls, err %v", res.Curated, len(fake.requests), err)
	}
}

func TestRunSendsBackCardsThatTheAcceptHookRefuses(t *testing.T) {
	ns := sampleNotes()
	wrong := at(kitab, 2)
	wrong.Pos = "verb"
	var drafts []string
	accept := func(draft notes.Note, card Card) error {
		drafts = append(drafts, draft.Pos+"→"+card.Pos)
		if card.Pos != draft.Pos {
			return errors.New("the card is for another entry")
		}
		return nil
	}
	fake := &fakeModel{respond: func(req Request, call int) (*Response, error) {
		if call == 1 {
			return answer(wrong, at(kataba, 3)), nil
		}
		if !strings.Contains(req.Prompt, "rejected: the card is for another entry") || strings.Contains(req.Prompt, "## Position 3") {
			t.Errorf("only the refused card goes back, with the reason:\n%s", req.Prompt)
		}
		return answer(at(kitab, 2)), nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1, 2}, sampleRecords(), Options{Accept: accept})
	if err != nil || res.Curated != 2 || len(fake.requests) != 2 || ns[1].Pos != "noun" {
		t.Fatalf("curated %d in %d calls, err %v, note %+v", res.Curated, len(fake.requests), err, ns[1])
	}
	if want := []string{"noun→verb", "verb→verb", "noun→noun"}; strings.Join(drafts, " ") != strings.Join(want, " ") {
		t.Errorf("the hook saw %v, want %v: it must see the draft as it was before the card was applied", drafts, want)
	}

	ns = sampleNotes()
	fake = &fakeModel{respond: func(Request, int) (*Response, error) { return answer(wrong), nil }}
	res, err = Run(context.Background(), fake, ns, []int{1}, sampleRecords(), Options{Accept: accept, Attempts: 2})
	if err != nil || res.Curated != 0 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Err.Error(), "another entry") || ns[1].Authored() {
		t.Fatalf("a card that is refused every time fails: %+v, %v", res, err)
	}
}

func TestPromptDoesNotClaimThatTheEntriesShareASpelling(t *testing.T) {
	ns := sampleNotes()
	got := Prompt(ns[1:2], sampleRecords(), nil)
	if strings.Contains(got, "share this spelling") || !strings.Contains(got, "Wiktionary entries for this word, the likeliest first:") {
		t.Errorf("the entries of a named word are only the ones the learner chose:\n%s", got)
	}
}
