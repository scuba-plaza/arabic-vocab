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

	"golang.org/x/sync/errgroup"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

const (
	DefaultVocabulary = 1000
	DefaultExamples   = 8
)

//go:embed guide.md
var guide string

//go:embed schema.json
var schema []byte

type Request struct {
	System string
	Prompt string
	Schema []byte
}

type Usage struct {
	Calls      int
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Models     []string
}

type Response struct {
	Output json.RawMessage
	Usage  Usage
}

type Model interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

type Card struct {
	Position  int          `json:"position"`
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
		Position: n.Position, Arabic: n.Arabic, Pos: n.Pos, English: n.English, Hint: n.Hint, Forms: forms,
		Example: n.Example, ExampleEn: n.ExampleEn, Comment: n.Comment,
	}
}

type Options struct {
	Batch       int
	Concurrency int
	Attempts    int
	System      string
	Feedback    map[int]string
	Progress    func(done, total int, n notes.Note, err error)
	Save        func([]notes.Note) error
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

func System(examples []notes.Note, vocabulary []string) string {
	var b strings.Builder
	b.WriteString(guide)
	if len(examples) > 0 {
		b.WriteString("\nFinished cards, in the format you return:\n")
		for _, n := range examples {
			c := CardOf(n)
			c.Comment = ""
			b.Write(marshal(c))
			b.WriteByte('\n')
		}
	}
	if len(vocabulary) > 0 {
		b.WriteString("\nThe learner's core vocabulary, most frequent first. Build example sentences mainly from these words:\n")
		b.WriteString(strings.Join(vocabulary, "\n"))
		b.WriteByte('\n')
	}
	return b.String()
}

func Prompt(drafts []notes.Note, records map[string]*rank.Record, problems map[int]string) string {
	var b strings.Builder
	if len(drafts) == 1 {
		b.WriteString("Write the finished card for this draft. Return it in \"cards\" with the position of the draft.\n")
	} else {
		fmt.Fprintf(&b, "Write the finished card for each of these %d drafts. Return them in \"cards\", each with the position of its draft.\n", len(drafts))
	}
	for _, n := range drafts {
		fmt.Fprintf(&b, "\n## Position %d", n.Position)
		if n.CEFR != "" {
			fmt.Fprintf(&b, " (CEFR %s)", n.CEFR)
		}
		b.WriteString("\nDraft generated from Wiktionary; keep what is right and fix what is wrong:\n")
		b.Write(marshal(CardOf(n)))
		b.WriteByte('\n')
		if rec := records[n.ID]; rec != nil && len(rec.Entries) > 0 {
			b.WriteString("Wiktionary entries that share this spelling, the likeliest first:\n")
			for _, e := range rec.Entries {
				b.Write(marshal(e))
				b.WriteByte('\n')
			}
		}
		if p := problems[n.Position]; p != "" {
			b.WriteString(p)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func ParseCards(raw json.RawMessage) (map[int]Card, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("the answer contained no cards")
	}
	var out struct {
		Cards []Card `json:"cards"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("the answer is not a list of cards: %w", err)
	}
	cards := map[int]Card{}
	for _, c := range out.Cards {
		if _, dup := cards[c.Position]; !dup {
			cards[c.Position] = c
		}
	}
	return cards, nil
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

func (u *Usage) add(v Usage) {
	u.Calls += v.Calls
	u.Input += v.Input
	u.Output += v.Output
	u.CacheRead += v.CacheRead
	u.CacheWrite += v.CacheWrite
	for _, m := range v.Models {
		if !slices.Contains(u.Models, m) {
			u.Models = append(u.Models, m)
		}
	}
}

func Run(ctx context.Context, model Model, ns []notes.Note, targets []int, records map[string]*rank.Record, opts Options) (*Result, error) {
	if opts.Batch <= 0 {
		opts.Batch = 10
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if opts.Attempts <= 0 {
		opts.Attempts = 2
	}
	res := &Result{}
	var mu sync.Mutex
	done := 0
	finish := func(i int, err error) error {
		done++
		if err != nil {
			res.Failed = append(res.Failed, Failure{ID: ns[i].ID, Position: ns[i].Position, Err: err})
		} else {
			res.Curated++
			if opts.Save != nil {
				if err := opts.Save(ns); err != nil {
					return err
				}
			}
		}
		if opts.Progress != nil {
			opts.Progress(done, len(targets), ns[i], err)
		}
		return nil
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	for batch := range slices.Chunk(targets, opts.Batch) {
		g.Go(func() error {
			pending := batch
			problems := map[int]error{}
			for attempt := 0; attempt < opts.Attempts && len(pending) > 0; attempt++ {
				mu.Lock()
				drafts := make([]notes.Note, len(pending))
				reasons := map[int]string{}
				for k, i := range pending {
					drafts[k] = ns[i]
					var parts []string
					if f := opts.Feedback[ns[i].Position]; f != "" {
						parts = append(parts, f)
					}
					if p := problems[i]; p != nil {
						parts = append(parts, "A previous answer for this card was rejected: "+p.Error()+". Correct it.")
					}
					if len(parts) > 0 {
						reasons[ns[i].Position] = strings.Join(parts, "\n")
					}
				}
				mu.Unlock()
				resp, err := model.Complete(gctx, Request{System: opts.System, Prompt: Prompt(drafts, records, reasons), Schema: schema})
				if err != nil {
					return err
				}
				cards, parseErr := ParseCards(resp.Output)
				mu.Lock()
				res.Usage.add(resp.Usage)
				var retry []int
				for _, i := range pending {
					card, ok := cards[ns[i].Position]
					problem := parseErr
					if problem == nil && !ok {
						problem = errors.New("the answer had no card for this position")
					}
					if problem == nil {
						problem = Validate(card)
					}
					if problem != nil {
						problems[i] = problem
						retry = append(retry, i)
						continue
					}
					ns[i] = Apply(ns[i], card)
					if err := finish(i, nil); err != nil {
						mu.Unlock()
						return err
					}
				}
				mu.Unlock()
				pending = retry
			}
			mu.Lock()
			defer mu.Unlock()
			for _, i := range pending {
				if err := finish(i, problems[i]); err != nil {
					return err
				}
			}
			return nil
		})
	}
	err := g.Wait()
	slices.SortFunc(res.Failed, func(a, b Failure) int { return a.Position - b.Position })
	return res, err
}
