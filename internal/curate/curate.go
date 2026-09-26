package curate

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"golang.org/x/sync/errgroup"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

//go:embed guide.md
var guide string

//go:embed schema.json
var schema []byte

type Messages interface {
	New(ctx context.Context, params anthropic.BetaMessageNewParams, opts ...option.RequestOption) (*anthropic.BetaMessage, error)
}

type Card struct {
	Arabic    string       `json:"arabic"`
	Pos       string       `json:"pos"`
	English   string       `json:"english"`
	Hint      string       `json:"hint"`
	Forms     []notes.Form `json:"forms"`
	Example   string       `json:"example"`
	ExampleEn string       `json:"example_en"`
	Comment   string       `json:"comment"`
}

func CardOf(n notes.Note) Card {
	forms := n.Forms
	if forms == nil {
		forms = []notes.Form{}
	}
	return Card{
		Arabic: n.Arabic, Pos: n.Pos, English: n.English, Hint: n.Hint, Forms: forms,
		Example: n.Example, ExampleEn: n.ExampleEn, Comment: n.Comment,
	}
}

var (
	ErrRefused   = errors.New("the model declined to write this card")
	ErrTruncated = errors.New("the answer was cut off at the max_tokens limit")
)

type Options struct {
	Model       string
	MaxTokens   int64
	Effort      string
	Fallbacks   bool
	Concurrency int
	Attempts    int
	System      []anthropic.BetaTextBlockParam
	Progress    func(done, total int, n notes.Note, err error)
	Save        func([]notes.Note) error
}

type Usage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Fallbacks  int
}

type Failure struct {
	ID       string
	Position int
	Err      error
}

type Result struct {
	Curated int
	Failed  []Failure
	Usage   Usage
}

func marshal(v any) []byte {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return []byte(strings.TrimRight(b.String(), "\n"))
}

func PickExamples(ns []notes.Note, n int) []notes.Note {
	var out []notes.Note
	used := map[string]bool{}
	pos := map[string]bool{}
	for _, distinct := range []bool{true, false} {
		for _, x := range ns {
			if len(out) == n {
				break
			}
			if !x.Authored() || used[x.ID] || distinct && pos[x.Pos] {
				continue
			}
			used[x.ID] = true
			pos[x.Pos] = true
			out = append(out, x)
		}
	}
	notes.Sort(out)
	return out
}

func System(examples []notes.Note, vocabulary []string) []anthropic.BetaTextBlockParam {
	blocks := []anthropic.BetaTextBlockParam{{Text: guide}}
	var b strings.Builder
	if len(examples) > 0 {
		b.WriteString("Finished cards, in the format you return:\n")
		for _, n := range examples {
			c := CardOf(n)
			c.Comment = ""
			b.Write(marshal(c))
			b.WriteByte('\n')
		}
	}
	if len(vocabulary) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("The learner's core vocabulary, most frequent first. Build example sentences mainly from these words:\n")
		b.WriteString(strings.Join(vocabulary, "\n"))
	}
	if b.Len() > 0 {
		blocks = append(blocks, anthropic.BetaTextBlockParam{Text: b.String()})
	}
	blocks[len(blocks)-1].CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
	return blocks
}

func Prompt(n notes.Note, rec *rank.Record, problem string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Rank %d in the frequency list", n.Position)
	if n.CEFR != "" {
		fmt.Fprintf(&b, ", CEFR level %s", n.CEFR)
	}
	b.WriteString(".\n\nDraft card generated from Wiktionary; keep what is right and fix what is wrong:\n")
	b.Write(marshal(CardOf(n)))
	if rec != nil && len(rec.Entries) > 0 {
		b.WriteString("\n\nWiktionary entries that share this spelling, the likeliest first:\n")
		for _, e := range rec.Entries {
			b.Write(marshal(e))
			b.WriteByte('\n')
		}
	}
	if problem != "" {
		fmt.Fprintf(&b, "\nA previous answer was rejected: %s. Return a corrected card.\n", problem)
	}
	return b.String()
}

func Request(opts Options, prompt string) anthropic.BetaMessageNewParams {
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(opts.Model),
		MaxTokens: opts.MaxTokens,
		System:    opts.System,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: json.RawMessage(schema)},
		},
	}
	if opts.Effort != "" {
		p.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(opts.Effort)
	}
	if opts.Fallbacks {
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		p.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}
	return p
}

func Parse(msg *anthropic.BetaMessage) (Card, error) {
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return Card{}, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return Card{}, ErrTruncated
	}
	var text strings.Builder
	for _, block := range msg.Content {
		switch block.Type {
		case "fallback":
			text.Reset()
		case "text":
			text.WriteString(block.Text)
		}
	}
	var c Card
	dec := json.NewDecoder(strings.NewReader(text.String()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Card{}, fmt.Errorf("the answer is not a card: %w", err)
	}
	return c, nil
}

func isLatin(r rune) bool {
	return unicode.In(r, unicode.Latin)
}

func arabicOnly(field, s string) error {
	switch {
	case !arabic.HasArabic(s):
		return fmt.Errorf("%s has no Arabic text", field)
	case strings.ContainsAny(s, "<>"):
		return fmt.Errorf("%s contains markup", field)
	case strings.ContainsFunc(s, isLatin):
		return fmt.Errorf("%s contains Latin letters", field)
	}
	return nil
}

func Validate(c Card) error {
	if strings.TrimSpace(c.English) == "" {
		return errors.New("english is empty")
	}
	if err := arabicOnly("arabic", c.Arabic); err != nil {
		return err
	}
	for _, f := range c.Forms {
		if strings.TrimSpace(f.Label) == "" {
			return fmt.Errorf("the form %s has no label", f.Arabic)
		}
		if err := arabicOnly("the form "+f.Label, f.Arabic); err != nil {
			return err
		}
	}
	if strings.Count(c.Example, "<b>") != 1 || strings.Count(c.Example, "</b>") != 1 {
		return errors.New("the example must mark the target word with exactly one <b>…</b>")
	}
	start, end := strings.Index(c.Example, "<b>"), strings.Index(c.Example, "</b>")
	if strings.TrimSpace(c.Example[min(start+len("<b>"), end):end]) == "" {
		return errors.New("the <b>…</b> in the example is empty")
	}
	if err := arabicOnly("the example", strings.NewReplacer("<b>", "", "</b>", "").Replace(c.Example)); err != nil {
		return err
	}
	if strings.TrimSpace(c.ExampleEn) == "" {
		return errors.New("example_en is empty")
	}
	return nil
}

func Apply(n notes.Note, c Card) notes.Note {
	trim := strings.TrimSpace
	n.Arabic = trim(c.Arabic)
	if c.Pos != n.Pos {
		if c.Pos == "verb" {
			n.Gender = ""
		} else {
			n.VerbForm = ""
		}
		n.Pos = c.Pos
	}
	n.English = trim(c.English)
	n.Hint = trim(c.Hint)
	n.Forms = nil
	for _, f := range c.Forms {
		n.Forms = append(n.Forms, notes.Form{Label: trim(f.Label), Arabic: trim(f.Arabic)})
	}
	n.Example = trim(c.Example)
	n.ExampleEn = trim(c.ExampleEn)
	n.Comment = trim(c.Comment)
	n.Reviewed = nil
	n.ReviewedAudio = nil
	return n
}

func Targets(ns []notes.Note, from, to int, redo bool) []int {
	var out []int
	for i, n := range ns {
		if n.Position >= from && n.Position <= to && (redo || !n.Authored()) {
			out = append(out, i)
		}
	}
	return out
}

func (u *Usage) add(msg *anthropic.BetaMessage) {
	u.Input += msg.Usage.InputTokens
	u.Output += msg.Usage.OutputTokens
	u.CacheRead += msg.Usage.CacheReadInputTokens
	u.CacheWrite += msg.Usage.CacheCreationInputTokens
	for _, it := range msg.Usage.Iterations {
		if it.Type == "fallback_message" {
			u.Fallbacks++
			break
		}
	}
}

func Run(ctx context.Context, client Messages, ns []notes.Note, targets []int, records map[string]*rank.Record, opts Options) (*Result, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Attempts <= 0 {
		opts.Attempts = 2
	}
	res := &Result{}
	var mu sync.Mutex
	done := 0
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	for _, i := range targets {
		g.Go(func() error {
			mu.Lock()
			n := ns[i]
			mu.Unlock()
			var (
				card    Card
				failure error
				problem string
			)
			for range opts.Attempts {
				msg, err := client.New(gctx, Request(opts, Prompt(n, records[n.ID], problem)))
				if err != nil {
					return fmt.Errorf("%s: %w", n.ID, err)
				}
				mu.Lock()
				res.Usage.add(msg)
				mu.Unlock()
				card, failure = Parse(msg)
				if failure == nil {
					failure = Validate(card)
				}
				if failure == nil || errors.Is(failure, ErrRefused) {
					break
				}
				problem = failure.Error()
			}
			mu.Lock()
			defer mu.Unlock()
			done++
			if failure != nil {
				res.Failed = append(res.Failed, Failure{ID: n.ID, Position: n.Position, Err: failure})
			} else {
				ns[i] = Apply(n, card)
				res.Curated++
				if opts.Save != nil {
					if err := opts.Save(ns); err != nil {
						return err
					}
				}
			}
			if opts.Progress != nil {
				opts.Progress(done, len(targets), ns[i], failure)
			}
			return nil
		})
	}
	err := g.Wait()
	slices.SortFunc(res.Failed, func(a, b Failure) int { return a.Position - b.Position })
	return res, err
}
