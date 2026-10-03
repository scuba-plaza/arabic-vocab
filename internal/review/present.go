package review

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type span struct {
	T string `json:"t"`
	C string `json:"c,omitempty"`
}

type row struct {
	Label string `json:"label"`
	Spans []span `json:"spans"`
	RTL   bool   `json:"rtl"`
	Note  string `json:"note,omitempty"`
}

type flagView struct {
	Tone    string `json:"tone"`
	Symbol  string `json:"symbol"`
	Title   string `json:"title"`
	Where   string `json:"where"`
	Rows    []row  `json:"rows"`
	Explain string `json:"explain,omitempty"`
	Faded   bool   `json:"faded"`
}

type formView struct {
	Label string `json:"label"`
	Spans []span `json:"spans"`
}

type cardView struct {
	Head      []span     `json:"head"`
	Meta      string     `json:"meta"`
	Source    string     `json:"source,omitempty"`
	English   string     `json:"english"`
	Hint      string     `json:"hint,omitempty"`
	Forms     []formView `json:"forms"`
	Example   []span     `json:"example"`
	ExampleEn string     `json:"example_en"`
	Comment   string     `json:"comment,omitempty"`
}

type changeView struct {
	Label string `json:"label"`
	RTL   bool   `json:"rtl"`
	Old   []span `json:"old"`
	New   []span `json:"new"`
}

type clipView struct {
	Kind  string `json:"kind"`
	Ready bool   `json:"ready"`
}

var clipKinds = []struct{ Kind, Field string }{
	{"word", "WordAudio"},
	{"forms", "FormsAudio"},
	{"sentence", "ExampleAudio"},
}

func clipField(kind string) string {
	for _, c := range clipKinds {
		if c.Kind == kind {
			return c.Field
		}
	}
	return ""
}

func clipKind(field string) string {
	for _, c := range clipKinds {
		if c.Field == field {
			return c.Kind
		}
	}
	return ""
}

type entryView struct {
	Index      int          `json:"index"`
	State      string       `json:"state"`
	Asking     bool         `json:"asking"`
	Seconds    int          `json:"seconds"`
	Notice     string       `json:"notice,omitempty"`
	NoticeTone string       `json:"notice_tone,omitempty"`
	Note       notes.Note   `json:"note"`
	Card       cardView     `json:"card"`
	Flags      []flagView   `json:"flags"`
	Proposal   *notes.Note  `json:"proposal,omitempty"`
	Changes    []changeView `json:"changes,omitempty"`
	Fixes      []endingFix  `json:"fixes,omitempty"`
	Clips      []clipView   `json:"clips"`
	Stale      bool         `json:"stale"`
}

type listView struct {
	Index      int    `json:"index"`
	Position   int    `json:"position"`
	Arabic     string `json:"arabic"`
	English    string `json:"english"`
	State      string `json:"state"`
	Tone       string `json:"tone"`
	Flags      int    `json:"flags"`
	Asking     bool   `json:"asking"`
	Ready      bool   `json:"ready"`
	Notice     string `json:"notice,omitempty"`
	NoticeTone string `json:"notice_tone,omitempty"`
}

type stateView struct {
	Summary Summary    `json:"summary"`
	Next    string     `json:"next,omitempty"`
	History int        `json:"history"`
	Claude  bool       `json:"claude"`
	Audio   bool       `json:"audio"`
	Voice   string     `json:"voice,omitempty"`
	All     bool       `json:"all"`
	List    []listView `json:"list"`
	Entry   *entryView `json:"entry,omitempty"`
}

type voicesView struct {
	Voice  string        `json:"voice"`
	Voices []VoiceOption `json:"voices"`
}

func (s *session) view(i int) stateView {
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := s.count()
	v := stateView{
		Summary: sum, Next: NextSteps(sum), History: len(s.history),
		Claude: s.opts.Rewrite != nil, Audio: s.opts.Remake != nil || s.opts.Remove != nil,
		Voice: s.voice, All: s.opts.All, List: []listView{},
	}
	for k, e := range s.entries {
		n := s.ns[e.Index]
		v.List = append(v.List, listView{
			Index: k, Position: n.Position, Arabic: n.Arabic, English: n.English,
			State: e.state.String(), Tone: toneOf(e.Item), Flags: e.Len(),
			Asking: e.asking, Ready: e.proposal != nil, Notice: e.notice, NoticeTone: e.noticeTone,
		})
	}
	if i >= 0 && i < len(s.entries) {
		ev := s.entryView(i)
		v.Entry = &ev
	}
	return v
}

func toneOf(it Item) string {
	if it.Len() == 0 {
		return "plain"
	}
	for _, is := range it.Issues {
		if is.Severity == notes.Major {
			return "bad"
		}
	}
	return "warn"
}

func (s *session) entryView(i int) entryView {
	e := s.entries[i]
	n := s.ns[e.Index]
	v := entryView{
		Index: i, State: e.state.String(), Asking: e.asking,
		Notice: e.notice, NoticeTone: e.noticeTone,
		Note: n, Card: cardOf(n, e), Flags: []flagView{}, Proposal: e.proposal,
		Clips: s.clipsOf(n), Stale: e.Stale,
	}
	if e.asking {
		v.Seconds = int(time.Since(e.since).Seconds())
	}
	faded := e.state != open
	for _, is := range e.Issues {
		v.Flags = append(v.Flags, issueView(is, faded))
	}
	if e.proposal != nil {
		v.Changes = changes(n, *e.proposal)
	}
	if e.state == open {
		v.Fixes = endingFixes(n, e.Issues)
	}
	return v
}

func (s *session) clipsOf(n notes.Note) []clipView {
	out := []clipView{}
	for _, at := range deck.AudioTexts(n) {
		kind := clipKind(at.Field)
		if kind == "" {
			continue
		}
		out = append(out, clipView{Kind: kind, Ready: s.opts.Clip != nil && s.opts.Clip(n, at.Field) != ""})
	}
	return out
}

func fieldName(field string) string {
	switch field {
	case "arabic", "WordAudio":
		return "word"
	case "example", "ExampleAudio":
		return "sentence"
	case "forms", "FormsAudio":
		return "forms"
	}
	return field
}

func appendSpan(out []span, text, class string) []span {
	if n := len(out); n > 0 && out[n-1].C == class {
		out[n-1].T += text
		return out
	}
	return append(out, span{T: text, C: class})
}

func withClass(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

func flaggedWords(e *entry) map[string]map[string]string {
	out := map[string]map[string]string{}
	if e.state != open {
		return out
	}
	for _, is := range e.Issues {
		if is.Word == "" {
			continue
		}
		if out[is.Field] == nil {
			out[is.Field] = map[string]string{}
		}
		if out[is.Field][is.Word] == "flag-major" {
			continue
		}
		out[is.Field][is.Word] = "flag-minor"
		if is.Severity == notes.Major {
			out[is.Field][is.Word] = "flag-major"
		}
	}
	return out
}

func pieceSpans(out []span, p piece, base string) []span {
	start := 0
	for i := 1; i <= len(p.runes); i++ {
		if i < len(p.runes) && p.bold[i] == p.bold[start] {
			continue
		}
		class := base
		if p.bold[start] {
			class = withClass(class, "target")
		}
		out = appendSpan(out, string(p.runes[start:i]), class)
		start = i
	}
	return out
}

func markup(s string, flagged map[string]string) []span {
	out := []span{}
	for _, p := range pieces(s) {
		base := ""
		if p.word {
			base = flagged[p.String()]
		}
		out = pieceSpans(out, p, base)
	}
	return out
}

func meta(n notes.Note) string {
	parts := []string{deck.PosLabel(n.Pos)}
	switch n.Gender {
	case "m":
		parts = append(parts, "masc.")
	case "f":
		parts = append(parts, "fem.")
	case "m+f":
		parts = append(parts, "masc./fem.")
	}
	if n.VerbForm != "" {
		parts = append(parts, "form "+n.VerbForm)
	}
	return strings.Join(append(parts, fmt.Sprintf("#%d", n.Position)), " · ")
}

func cardOf(n notes.Note, e *entry) cardView {
	flags := flaggedWords(e)
	c := cardView{
		Head: markup(n.Arabic, flags["arabic"]), Meta: meta(n), Source: n.Source,
		English: n.English, Hint: n.Hint, Forms: []formView{},
		Example: markup(n.Example, flags["example"]), ExampleEn: n.ExampleEn, Comment: n.Comment,
	}
	for _, f := range n.Forms {
		c.Forms = append(c.Forms, formView{Label: f.Label, Spans: markup(f.Arabic, flags["forms"])})
	}
	return c
}

func issueView(is notes.Issue, faded bool) flagView {
	tone, symbol := "bad", "✗"
	if is.Severity == notes.Minor {
		tone, symbol = "warn", "!"
	}
	hl := "mark-" + tone
	if faded {
		hl = ""
	}
	title, rows, explain := describe(is, hl)
	return flagView{Tone: tone, Symbol: symbol, Title: title, Where: fieldName(is.Field) + " · " + string(is.Severity), Rows: rows, Explain: explain, Faded: faded}
}

func describe(is notes.Issue, hl string) (string, []row, string) {
	word := func(label, w string, marked []bool) row {
		if w == "" {
			return row{Label: label, Spans: []span{}, Note: "no reading"}
		}
		return row{Label: label, Spans: letterSpans(w, marked, hl), RTL: true}
	}
	switch is.Kind {
	case "diacritics":
		catt := tashkeel.Differences(is.Word, is.CATT)
		camel := tashkeel.Differences(is.Word, is.CAMeL)
		either := make([]bool, len(tashkeel.Letters(is.Word)))
		for i := range either {
			either[i] = i < len(catt) && catt[i] || is.Severity == notes.Major && i < len(camel) && camel[i]
		}
		rows := []row{word("card", is.Word, either), word("CATT", is.CATT, catt)}
		switch {
		case is.CAMeL == "":
			rows = append(rows, word("CAMeL", "", nil))
			return "CATT reads this word with other vowels", rows, "CAMeL had no reading for it, so check the vowels yourself."
		case is.Severity == notes.Minor:
			agrees := word("CAMeL", is.CAMeL, nil)
			agrees.Note = "agrees with the card"
			rows = append(rows, agrees)
			return "Only CATT reads this word with other vowels", rows, "CAMeL agrees with the card, so the card is most likely right."
		}
		rows = append(rows, word("CAMeL", is.CAMeL, camel))
		return "CATT and CAMeL both read this word with other vowels", rows, "Check the vowels. If the card is right, the sentence can probably be read another way without vowel marks; Claude Code can suggest a clearer one."
	case "invalid":
		rows := []row{word("card", is.Word, nil), word("known", strings.Join(is.Known, "، "), nil)}
		return "CAMeL does not know these vowels for this word", rows, "CAMeL's dictionary has the word, but not with these vowels. Compare with Wiktionary."
	case "unmarked":
		rows := []row{word("card", is.Word, bareLetters(is))}
		if is.CATT != "" {
			rows = append(rows, word("CATT", is.CATT, tashkeel.Differences(is.Word, is.CATT)))
		}
		if is.CAMeL != "" {
			rows = append(rows, word("CAMeL", is.CAMeL, tashkeel.Differences(is.Word, is.CAMeL)))
		}
		if is.Field == "example" && slices.Equal(is.Missing, []int{tashkeel.EndingIndex(is.Word)}) {
			return "The ending has no vowel mark", rows, "Every word in a sentence carries its ending, the last one too. The audio drops the last word's ending by itself."
		}
		return "Some letters have no vowel mark", rows, "No reading from CAMeL or CATT has these letters without a vowel, so one is probably missing."
	case "unknown":
		return "CAMeL does not know this word", []row{word("card", is.Word, nil)}, "Only Wiktionary vouches for its vowels, which is usually fine for less common words."
	case "unchecked":
		return "CATT could not be compared with this sentence", []row{}, "CATT split the sentence into different words, so its vowels were not compared. Read the sentence yourself."
	}
	return is.Kind, []row{word("card", is.Word, nil)}, is.Detail
}

func letterSpans(word string, marked []bool, hl string) []span {
	ls := tashkeel.Letters(word)
	if hl == "" || len(marked) != len(ls) {
		return []span{{T: word}}
	}
	var out []span
	for i, l := range ls {
		class := ""
		if marked[i] {
			class = hl
		}
		out = appendSpan(out, l, class)
	}
	return out
}

func bareLetters(is notes.Issue) []bool {
	out := make([]bool, len(tashkeel.Letters(is.Word)))
	for _, i := range is.Missing {
		if i >= 0 && i < len(out) {
			out[i] = true
		}
	}
	return out
}

func wordSpans(ws []string, marked []bool, class string) []span {
	out := []span{}
	for i, w := range ws {
		if i > 0 {
			out = appendSpan(out, " ", "")
		}
		c := ""
		if marked[i] {
			c = class
		}
		out = appendSpan(out, w, c)
	}
	return out
}

func formsText(n notes.Note) string {
	var parts []string
	for _, f := range n.Forms {
		parts = append(parts, f.Label+" "+f.Arabic)
	}
	return strings.Join(parts, "   ")
}

func changes(cur, fresh notes.Note) []changeView {
	fields := []struct {
		label, old, new string
		rtl, markup     bool
	}{
		{"word", cur.Arabic, fresh.Arabic, true, false},
		{"type", deck.PosLabel(cur.Pos), deck.PosLabel(fresh.Pos), false, false},
		{"meaning", cur.English, fresh.English, false, false},
		{"hint", cur.Hint, fresh.Hint, false, false},
		{"forms", formsText(cur), formsText(fresh), false, false},
		{"sentence", cur.Example, fresh.Example, true, true},
		{"translation", cur.ExampleEn, fresh.ExampleEn, false, false},
		{"comment", cur.Comment, fresh.Comment, false, false},
	}
	out := []changeView{}
	for _, f := range fields {
		if f.old == f.new {
			continue
		}
		c := changeView{Label: f.label, RTL: f.rtl}
		c.Old, c.New = diffSpans(f.old, f.new, f.markup)
		out = append(out, c)
	}
	return out
}

func plainSpans(s string, withMarkup bool) []span {
	switch {
	case withMarkup:
		return markup(s, nil)
	case s == "":
		return []span{}
	}
	return []span{{T: s}}
}

func diffSpans(old, fresh string, withMarkup bool) ([]span, []span) {
	if old == "" || fresh == "" {
		return plainSpans(old, withMarkup), plainSpans(fresh, withMarkup)
	}
	if withMarkup {
		po, pf := pieces(old), pieces(fresh)
		keepOld, keepFresh := common(words(po), words(pf))
		return changedPieces(po, keepOld, "del"), changedPieces(pf, keepFresh, "ins")
	}
	wo, wf := strings.Fields(old), strings.Fields(fresh)
	keepOld, keepFresh := common(wo, wf)
	return wordSpans(wo, invert(keepOld), "del"), wordSpans(wf, invert(keepFresh), "ins")
}

func changedPieces(ps []piece, keep []bool, class string) []span {
	out := []span{}
	k := 0
	for _, p := range ps {
		base := ""
		if p.word {
			if !keep[k] {
				base = class
			}
			k++
		}
		out = pieceSpans(out, p, base)
	}
	return out
}

func invert(bs []bool) []bool {
	out := make([]bool, len(bs))
	for i, b := range bs {
		out[i] = !b
	}
	return out
}
