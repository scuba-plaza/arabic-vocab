package curate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

type fakeMessages struct {
	mu      sync.Mutex
	calls   []anthropic.BetaMessageNewParams
	respond func(prompt string, call int) (*anthropic.BetaMessage, error)
}

func (f *fakeMessages) New(ctx context.Context, params anthropic.BetaMessageNewParams, opts ...option.RequestOption) (*anthropic.BetaMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, params)
	call := len(f.calls)
	f.mu.Unlock()
	prompt := params.Messages[0].Content[0].OfText.Text
	return f.respond(prompt, call)
}

type block map[string]any

func message(t *testing.T, stop string, content ...block) *anthropic.BetaMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant", "model": "test-model",
		"content": content, "stop_reason": stop,
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 50, "cache_read_input_tokens": 2000, "cache_creation_input_tokens": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	var msg anthropic.BetaMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	return &msg
}

func cardText(t *testing.T, c Card) block {
	t.Helper()
	return block{"type": "text", "text": string(marshal(c))}
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

func sampleNotes() []notes.Note {
	return []notes.Note{
		{ID: "فِي", Position: 1, Arabic: "فِي", Pos: "prep", English: "in", Example: "<b>فِي</b> الْبَيْتِ.", ExampleEn: "In the house."},
		{ID: "كِتَاب", Position: 2, Arabic: "كِتَاب", Pos: "noun", Gender: "m"},
		{ID: "كَتَبَ", Position: 3, Arabic: "كَتَبَ", Pos: "verb", VerbForm: "I"},
		{ID: "قَلَم", Position: 4, Arabic: "قَلَم", Pos: "noun"},
	}
}

func sampleRecords() map[string]*rank.Record {
	return map[string]*rank.Record{
		"كِتَاب": {Rank: 2, ID: "كِتَاب", Entries: []*lexicon.Entry{{Title: "كتاب", Pos: "noun", Canonical: "كِتَاب", Senses: []lexicon.Sense{{Gloss: "book", MSA: true}}}}},
	}
}

func testOptions() Options {
	return Options{Model: "test-model", MaxTokens: 1000, Fallbacks: true, Concurrency: 2, System: System(nil, []string{"فِي", "كِتَاب"})}
}

func TestRunFillsSelectedNotes(t *testing.T) {
	ns := sampleNotes()
	fake := &fakeMessages{respond: func(prompt string, call int) (*anthropic.BetaMessage, error) {
		if strings.Contains(prompt, `"arabic":"كِتَاب"`) {
			return message(t, "end_turn", cardText(t, kitab)), nil
		}
		return message(t, "end_turn", cardText(t, kataba)), nil
	}}
	var saves int
	var saved []notes.Note
	opts := testOptions()
	opts.Save = func(ns []notes.Note) error {
		saves++
		saved = append([]notes.Note(nil), ns...)
		return nil
	}
	targets := Targets(ns, 1, 3, false)
	if len(targets) != 2 {
		t.Fatalf("targets = %v, want the two unauthored notes in 1..3", targets)
	}
	res, err := Run(context.Background(), fake, ns, targets, sampleRecords(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 2 || len(res.Failed) != 0 || saves != 2 {
		t.Fatalf("curated %d, failed %v, saves %d", res.Curated, res.Failed, saves)
	}
	if ns[0].English != "in" || ns[3].Authored() {
		t.Fatalf("notes outside the targets changed: %+v %+v", ns[0], ns[3])
	}
	if ns[1].English != "book" || ns[1].Example != kitab.Example || ns[1].Gender != "m" || len(ns[1].Forms) != 1 {
		t.Fatalf("kitab note = %+v", ns[1])
	}
	if ns[2].English != "to write" || ns[2].VerbForm != "I" || !saved[2].Authored() {
		t.Fatalf("kataba note = %+v", ns[2])
	}
	if res.Usage.Input != 200 || res.Usage.CacheRead != 4000 || res.Usage.Output != 100 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

func TestRequestShape(t *testing.T) {
	n := sampleNotes()[1]
	p := Request(testOptions(), Prompt(n, sampleRecords()[n.ID], ""))
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Model        string `json:"model"`
		MaxTokens    int    `json:"max_tokens"`
		Fallbacks    string `json:"fallbacks"`
		OutputConfig struct {
			Format struct {
				Type   string `json:"type"`
				Schema struct {
					Required             []string `json:"required"`
					AdditionalProperties bool     `json:"additionalProperties"`
				} `json:"schema"`
			} `json:"format"`
		} `json:"output_config"`
		System []struct {
			Text         string `json:"text"`
			CacheControl *struct {
				Type string `json:"type"`
			} `json:"cache_control"`
		} `json:"system"`
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "test-model" || body.MaxTokens != 1000 || body.Fallbacks != "default" {
		t.Fatalf("request = %s", raw)
	}
	if body.OutputConfig.Format.Type != "json_schema" || len(body.OutputConfig.Format.Schema.Required) != 8 || body.OutputConfig.Format.Schema.AdditionalProperties {
		t.Fatalf("output_config = %+v", body.OutputConfig)
	}
	if len(body.System) != 2 || body.System[0].CacheControl != nil || body.System[1].CacheControl == nil || body.System[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("system blocks = %+v", body.System)
	}
	if !strings.Contains(body.System[1].Text, "فِي\nكِتَاب") {
		t.Fatalf("vocabulary missing from %q", body.System[1].Text)
	}
	prompt := body.Messages[0].Content[0].Text
	for _, want := range []string{"Rank 2", `"arabic":"كِتَاب"`, `"gloss":"book"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %s:\n%s", want, prompt)
		}
	}
	if len(p.Betas) != 1 || p.Betas[0] != anthropic.AnthropicBetaServerSideFallback2026_07_01 {
		t.Fatalf("betas = %v", p.Betas)
	}

	opts := testOptions()
	opts.Fallbacks = false
	opts.Effort = "high"
	raw, err = json.Marshal(Request(opts, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fallbacks") || !strings.Contains(string(raw), `"effort":"high"`) {
		t.Fatalf("request without fallbacks = %s", raw)
	}
}

func TestRunRetriesRejectedCards(t *testing.T) {
	ns := sampleNotes()
	bad := kitab
	bad.Example = "<b>قَرَأْتُ</b> <b>الْكِتَابَ</b>."
	fake := &fakeMessages{respond: func(prompt string, call int) (*anthropic.BetaMessage, error) {
		if call == 1 {
			return message(t, "end_turn", cardText(t, bad)), nil
		}
		if !strings.Contains(prompt, "A previous answer was rejected: the example must mark the target word with exactly one <b>…</b>") {
			t.Errorf("retry prompt does not explain the problem:\n%s", prompt)
		}
		return message(t, "end_turn", cardText(t, kitab)), nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1}, sampleRecords(), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 1 || len(fake.calls) != 2 || ns[1].Example != kitab.Example {
		t.Fatalf("curated %d after %d calls; note %+v", res.Curated, len(fake.calls), ns[1])
	}
}

func TestRunGivesUpAfterAttempts(t *testing.T) {
	ns := sampleNotes()
	fake := &fakeMessages{respond: func(prompt string, call int) (*anthropic.BetaMessage, error) {
		return message(t, "end_turn", block{"type": "text", "text": "not json"}), nil
	}}
	opts := testOptions()
	opts.Attempts = 3
	res, err := Run(context.Background(), fake, ns, []int{1, 2}, sampleRecords(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Curated != 0 || len(res.Failed) != 2 || len(fake.calls) != 6 {
		t.Fatalf("curated %d, failed %v, calls %d", res.Curated, res.Failed, len(fake.calls))
	}
	if res.Failed[0].Position != 2 || res.Failed[1].Position != 3 || ns[1].Authored() {
		t.Fatalf("failures = %+v", res.Failed)
	}
}

func TestRunDoesNotRetryRefusals(t *testing.T) {
	ns := sampleNotes()
	fake := &fakeMessages{respond: func(prompt string, call int) (*anthropic.BetaMessage, error) {
		return message(t, "refusal"), nil
	}}
	res, err := Run(context.Background(), fake, ns, []int{1}, sampleRecords(), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || len(res.Failed) != 1 || !errors.Is(res.Failed[0].Err, ErrRefused) {
		t.Fatalf("calls %d, failures %+v", len(fake.calls), res.Failed)
	}
}

func TestRunStopsOnAPIErrors(t *testing.T) {
	ns := sampleNotes()
	boom := errors.New("401 invalid x-api-key")
	fake := &fakeMessages{respond: func(prompt string, call int) (*anthropic.BetaMessage, error) {
		return nil, boom
	}}
	_, err := Run(context.Background(), fake, ns, []int{1, 2}, sampleRecords(), testOptions())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseKeepsTheTextAfterAFallback(t *testing.T) {
	msg := message(t, "end_turn",
		block{"type": "text", "text": `{"arabic":`},
		block{"type": "fallback", "from": block{"model": "a"}, "to": block{"model": "b"}, "trigger": block{"type": "refusal", "category": "general_harms"}},
		cardText(t, kitab),
	)
	c, err := Parse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if c.English != "book" {
		t.Fatalf("card = %+v", c)
	}
	if _, err := Parse(message(t, "max_tokens", block{"type": "text", "text": "{"})); !errors.Is(err, ErrTruncated) {
		t.Fatalf("max_tokens: err = %v", err)
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
	n := notes.Note{ID: "x", Position: 9, Arabic: "بَعْد", Pos: "noun", Gender: "m", VerbForm: "", Reviewed: []string{"بَعْدَ"}, ReviewedAudio: []string{"ar-0.mp3"}}
	c := Card{Arabic: "بَعْدَ", Pos: "prep", English: "after", Example: "<b>بَعْدَ</b> الدَّرْسِ.", ExampleEn: "After the lesson.", Forms: []notes.Form{}}
	got := Apply(n, c)
	if got.Pos != "prep" || got.Arabic != "بَعْدَ" || got.Reviewed != nil || got.ReviewedAudio != nil || got.Forms != nil || got.ID != "x" || got.Position != 9 {
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
