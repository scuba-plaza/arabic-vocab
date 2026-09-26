package review

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

const maxWidth = 100

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type styles struct {
	text   lipgloss.Style
	bold   lipgloss.Style
	accent lipgloss.Style
	dim    lipgloss.Style
	faint  lipgloss.Style
	good   lipgloss.Style
	warn   lipgloss.Style
	bad    lipgloss.Style
	claude lipgloss.Style
	audio  lipgloss.Style
	target lipgloss.Style
	title  lipgloss.Style
	key    lipgloss.Style
	panel  lipgloss.Style
}

func newStyles(dark bool) styles {
	pick := lipgloss.LightDark(dark)
	fg := func(light, dark string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(pick(lipgloss.Color(light), lipgloss.Color(dark)))
	}
	s := styles{
		text:   lipgloss.NewStyle(),
		bold:   lipgloss.NewStyle().Bold(true),
		accent: fg("#2E5BD8", "#7AA2F7"),
		dim:    fg("#5F6368", "#8C92AC"),
		faint:  fg("#B0B4BC", "#4B5068"),
		good:   fg("#2F7D32", "#9ECE6A"),
		warn:   fg("#A15C00", "#E0AF68"),
		bad:    fg("#C62828", "#F7768E"),
		claude: fg("#B4532A", "#E3956C"),
		audio:  fg("#00739E", "#7DCFFF"),
	}
	s.target = fg("#9A6700", "#FFD479")
	s.title = s.accent.Bold(true)
	s.key = s.accent.Bold(true)
	s.panel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pick(lipgloss.Color("#B0B4BC"), lipgloss.Color("#4B5068"))).
		Padding(0, 1)
	return s
}

func (s styles) tone(t tone) lipgloss.Style {
	switch t {
	case good:
		return s.good
	case bad:
		return s.bad
	case claude:
		return s.claude
	}
	return s.dim
}

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "arabic-vocab review"
	return v
}

func (m *model) render() string {
	if m.width < 40 || m.height < 12 {
		return "The terminal is too small for the review.\nMake it larger, or press q to quit."
	}
	w := min(m.width-2, maxWidth)
	header, footer := m.header(w), m.footer(w)
	var body string
	if m.finished {
		body = m.doneView(w)
	} else {
		body = m.entryView(w)
	}
	lines := strings.Split(body, "\n")
	room := max(1, m.height-lipgloss.Height(header)-lipgloss.Height(footer)-1)
	m.maxScroll = 0
	if len(lines) > room {
		shown := room - 1
		m.maxScroll = len(lines) - shown
		top := min(m.scroll, m.maxScroll)
		lines = append(lines[top:top+shown:top+shown], m.st.dim.Render(fmt.Sprintf("↑↓ scroll · lines %d–%d of %d", top+1, top+shown, len(lines))))
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	page := header + "\n\n" + strings.Join(lines, "\n") + "\n" + footer
	return lipgloss.NewStyle().MarginLeft((m.width - w) / 2).Render(page)
}

func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	switch {
	case gap >= 2:
		return left + strings.Repeat(" ", gap) + right
	case w-lipgloss.Width(left)-2 >= 8:
		return left + "  " + ansi.Truncate(right, w-lipgloss.Width(left)-2, "…")
	}
	return ansi.Truncate(left, w, "…")
}

func hang(prefix, text string, w int) string {
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	lines := strings.Split(lipgloss.Wrap(text, max(10, w-lipgloss.Width(prefix)), ""), "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
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

func elapsed(since time.Time) string {
	d := time.Since(since).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (m *model) header(w int) string {
	s := m.summary()
	where := "summary"
	if !m.finished {
		where = fmt.Sprintf("note %d of %d", m.cur+1, s.Total)
	}
	parts := []string{m.st.dim.Render(fmt.Sprintf("%d open", s.Open))}
	if s.Kept > 0 {
		parts = append(parts, m.st.good.Render(fmt.Sprintf("✓ %d right", s.Kept)))
	}
	if s.Edited > 0 {
		parts = append(parts, m.st.good.Render(fmt.Sprintf("✎ %d edited", s.Edited)))
	}
	if s.Rewritten > 0 {
		parts = append(parts, m.st.claude.Render(fmt.Sprintf("✦ %d rewritten", s.Rewritten)))
	}
	top := spread(m.st.title.Render("arabic-vocab review")+"  "+m.st.dim.Render(where), strings.Join(parts, m.st.faint.Render(" · ")), w)
	done := w
	if s.Total > 0 {
		done = (s.Total - s.Open) * w / s.Total
	}
	return top + "\n" + m.st.good.Render(strings.Repeat("━", done)) + m.st.faint.Render(strings.Repeat("━", w-done))
}

func (m *model) footer(w int) string {
	return m.st.faint.Render(strings.Repeat("─", w)) + "\n" + m.statusLine(w) + "\n" + m.keyLine(w)
}

func (m *model) statusLine(w int) string {
	spin := spinner[m.frame%len(spinner)]
	var activity []string
	if m.playing != "" {
		activity = append(activity, m.st.audio.Render("▶ playing the "+m.playing))
	}
	others := 0
	for i, e := range m.entries {
		switch {
		case !e.asking:
		case i == m.cur && !m.finished:
			activity = append(activity, m.st.claude.Render(spin+" Claude Code is writing a new version · "+elapsed(e.since)))
		default:
			others++
		}
	}
	if others > 0 {
		activity = append(activity, m.st.claude.Render(spin+" Claude Code is working on "+plural(others, "other note", "other notes")))
	}
	return spread(m.st.tone(m.statusTone).Render(m.status), strings.Join(activity, "   "), w)
}

type binding struct {
	key  string
	desc string
}

func (m *model) bindings() []binding {
	if m.finished {
		b := []binding{{"←", "back to the notes"}}
		if m.summary().Open > 0 {
			b = append(b, binding{"enter", "first open note"})
		}
		if len(m.history) > 0 {
			b = append(b, binding{"u", "undo"})
		}
		return append(b, binding{"q", "quit"})
	}
	e := m.entries[m.cur]
	n := m.ns[e.Index]
	var b []binding
	switch {
	case e.proposal != nil:
		b = append(b, binding{"y", "use this version"}, binding{"n", "keep yours"}, binding{"e", "edit it first"})
	case e.state == open:
		b = append(b, binding{"enter", "card is right"}, binding{"e", "edit"})
	default:
		b = append(b, binding{"enter", "next open note"}, binding{"e", "edit"})
	}
	if e.proposal == nil && (e.state == open || e.state == kept) {
		switch {
		case e.asking:
			b = append(b, binding{"esc", "stop Claude Code"})
		case m.opts.Rewrite != nil:
			b = append(b, binding{"c", "ask Claude"})
		}
	}
	if m.hasClip(n, "ExampleAudio") {
		b = append(b, binding{"p", "play sentence"})
	}
	if m.hasClip(n, "WordAudio") {
		b = append(b, binding{"w", "play word"})
	}
	b = append(b, binding{"←→", "other notes"})
	if len(m.history) > 0 {
		b = append(b, binding{"u", "undo"})
	}
	return append(b, binding{"q", "quit"})
}

func (m *model) hasClip(n notes.Note, field string) bool {
	return m.opts.Clip != nil && m.opts.Play != nil && m.opts.Clip(n, field) != ""
}

func (m *model) keyLine(w int) string {
	var lines []string
	line := ""
	for _, b := range m.bindings() {
		item := m.st.key.Render(b.key) + " " + m.st.dim.Render(b.desc)
		switch {
		case line == "":
			line = item
		case lipgloss.Width(line)+2+lipgloss.Width(item) > w:
			lines = append(lines, line)
			line = item
		default:
			line += "  " + item
		}
	}
	return strings.Join(append(lines, line), "\n")
}

func (m *model) entryView(w int) string {
	e := m.entries[m.cur]
	n := m.ns[e.Index]
	if e.proposal != nil {
		return m.card(n, e, w) + "\n\n" + m.proposalView(n, *e.proposal, w)
	}
	return m.card(n, e, w) + "\n\n" + m.flagsView(n, e, w)
}

func (m *model) flagged(e *entry) map[string]map[string]lipgloss.Style {
	out := map[string]map[string]lipgloss.Style{}
	if e.state != open {
		return out
	}
	for _, is := range e.Issues {
		if is.Word == "" {
			continue
		}
		if out[is.Field] == nil {
			out[is.Field] = map[string]lipgloss.Style{}
		}
		if _, seen := out[is.Field][is.Word]; seen && is.Severity != notes.Major {
			continue
		}
		st := m.st.warn
		if is.Severity == notes.Major {
			st = m.st.bad
		}
		out[is.Field][is.Word] = st.Underline(true)
	}
	return out
}

func (m *model) markup(s string, flagged map[string]lipgloss.Style, base, target lipgloss.Style) string {
	var b strings.Builder
	for _, p := range pieces(s) {
		st := base
		if f, ok := flagged[p.String()]; ok && p.word {
			st = f.Inherit(base)
		}
		b.WriteString(renderPiece(p, st, target))
	}
	return b.String()
}

func renderPiece(p piece, st, target lipgloss.Style) string {
	var b strings.Builder
	start := 0
	for i := 1; i <= len(p.runes); i++ {
		if i < len(p.runes) && p.bold[i] == p.bold[start] {
			continue
		}
		run := st
		if p.bold[start] {
			run = st.Inherit(target)
		}
		b.WriteString(run.Render(string(p.runes[start:i])))
		start = i
	}
	return b.String()
}

func (m *model) meta(n notes.Note) string {
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
	parts = append(parts, fmt.Sprintf("#%d", n.Position))
	out := m.st.dim.Render(strings.Join(parts, " · "))
	if n.Source != "" {
		out += m.st.faint.Render(" · ") + m.st.dim.Hyperlink(n.Source).Render("Wiktionary ↗")
	}
	return out
}

func (m *model) card(n notes.Note, e *entry, w int) string {
	inner := w - 4
	flags := m.flagged(e)
	head := m.markup(n.Arabic, flags["arabic"], m.st.accent, m.st.accent)
	lines := []string{spread(head, m.meta(n), inner), n.English}
	if n.Hint != "" {
		lines = append(lines, m.st.dim.Render("hint  ")+n.Hint)
	}
	if len(n.Forms) > 0 {
		var forms []string
		for _, f := range n.Forms {
			forms = append(forms, m.st.dim.Render(f.Label)+" "+m.markup(f.Arabic, flags["forms"], m.st.text, m.st.text))
		}
		lines = append(lines, strings.Join(forms, "    "))
	}
	lines = append(lines, "", m.markup(n.Example, flags["example"], m.st.text, m.st.target), m.st.dim.Render(n.ExampleEn))
	if n.Comment != "" {
		lines = append(lines, "", m.st.dim.Italic(true).Render(n.Comment))
	}
	return m.st.panel.Width(w).Render(strings.Join(lines, "\n"))
}

func (m *model) flagsView(n notes.Note, e *entry, w int) string {
	var head string
	switch e.state {
	case open:
		head = m.st.bold.Render(plural(e.Len(), "flag", "flags") + " to look at")
	case kept:
		head = m.st.good.Render("✓ You marked this card as right")
	case edited:
		head = m.st.good.Render("✎ You edited this note") + m.st.dim.Render(" · 'arabic-vocab check' will check it again")
	case rewritten:
		head = m.st.claude.Render("✦ You kept Claude Code's version") + m.st.dim.Render(" · 'arabic-vocab check' will check it again")
	}
	faded := e.state != open
	blocks := []string{head}
	for _, is := range e.Issues {
		blocks = append(blocks, m.issue(is, faded, w))
	}
	for _, a := range e.Audio {
		blocks = append(blocks, m.audioFlag(a, faded, w))
	}
	return strings.Join(blocks, "\n\n")
}

type row struct {
	label string
	text  string
}

func (m *model) block(mark lipgloss.Style, symbol, title, where string, rows []row, explain string, faded bool, w int) string {
	titleStyle := m.st.bold
	if faded {
		mark, titleStyle = m.st.dim, m.st.dim
	}
	lines := []string{spread(mark.Render(symbol)+" "+titleStyle.Render(title), m.st.dim.Render(where), w)}
	for _, r := range rows {
		lines = append(lines, hang("    "+m.st.dim.Render(fmt.Sprintf("%-8s", r.label)), r.text, w))
	}
	if explain != "" && !faded {
		lines = append(lines, hang("    ", m.st.dim.Render(explain), w))
	}
	return strings.Join(lines, "\n")
}

func (m *model) issue(is notes.Issue, faded bool, w int) string {
	mark, symbol := m.st.bad, "✗"
	if is.Severity == notes.Minor {
		mark, symbol = m.st.warn, "!"
	}
	hl := mark
	if faded {
		hl = m.st.text
	}
	title, rows, explain := m.describe(is, hl)
	return m.block(mark, symbol, title, fieldName(is.Field)+" · "+string(is.Severity), rows, explain, faded, w)
}

func (m *model) describe(is notes.Issue, hl lipgloss.Style) (string, []row, string) {
	switch is.Kind {
	case "diacritics":
		if is.CATT == "" {
			return "CATT reads this word with other vowels", []row{{"card", is.Word}}, is.Detail
		}
		catt := tashkeel.Differences(is.Word, is.CATT)
		camel := tashkeel.Differences(is.Word, is.CAMeL)
		either := make([]bool, len(tashkeel.Letters(is.Word)))
		for i := range either {
			either[i] = i < len(catt) && catt[i] || is.Severity == notes.Major && i < len(camel) && camel[i]
		}
		rows := []row{{"card", letters(is.Word, either, hl)}, {"CATT", letters(is.CATT, catt, hl)}}
		switch {
		case is.CAMeL == "":
			rows = append(rows, row{"CAMeL", m.st.dim.Render("no reading")})
			return "CATT reads this word with other vowels", rows, "CAMeL had no reading for it, so check the vowels yourself."
		case is.Severity == notes.Minor:
			rows = append(rows, row{"CAMeL", is.CAMeL + m.st.dim.Render("  agrees with the card")})
			return "Only CATT reads this word with other vowels", rows, "CAMeL agrees with the card, so the card is most likely right."
		}
		rows = append(rows, row{"CAMeL", letters(is.CAMeL, camel, hl)})
		return "CATT and CAMeL both read this word with other vowels", rows, "Check the vowels. If the card is right, the sentence can probably be read another way without vowel marks; c asks Claude Code for a clearer one."
	case "invalid":
		rows := []row{{"card", is.Word}}
		if len(is.Known) > 0 {
			rows = append(rows, row{"known", strings.Join(is.Known, "، ")})
			return "CAMeL does not know these vowels for this word", rows, "CAMeL's dictionary has the word, but not with these vowels. Compare with Wiktionary."
		}
		return "CAMeL does not know these vowels for this word", rows, is.Detail
	case "unmarked":
		return "Some letters have no vowel mark", []row{{"card", letters(is.Word, unmarked(is), hl)}}, sentence(is.Detail)
	case "unknown":
		return "CAMeL does not know this word", []row{{"card", is.Word}}, "Only Wiktionary vouches for its vowels, which is usually fine for less common words."
	case "unchecked":
		return "CATT could not be compared with this sentence", nil, "CATT split the sentence into different words, so its vowels were not compared. Read the sentence yourself."
	}
	return is.Kind, []row{{"card", is.Word}}, is.Detail
}

func sentence(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func letters(word string, marked []bool, hl lipgloss.Style) string {
	ls := tashkeel.Letters(word)
	if len(marked) != len(ls) {
		return word
	}
	var b strings.Builder
	for i, l := range ls {
		if marked[i] {
			b.WriteString(hl.Render(l))
		} else {
			b.WriteString(l)
		}
	}
	return b.String()
}

func unmarked(is notes.Issue) []bool {
	out := make([]bool, len(tashkeel.Letters(is.Word)))
	for _, mm := range tashkeel.UnmarkedLetters(is.Word, is.Field != "example") {
		if mm.Index < len(out) {
			out[mm.Index] = true
		}
	}
	return out
}

func (m *model) audioFlag(a notes.AudioCheck, faded bool, w int) string {
	hl := m.st.audio
	if faded {
		hl = m.st.text
	}
	want, wantMissing, heard, heardExtra := spokenDiff(arabic.StripTashkeel(a.Text), a.Transcript)
	heardText := joinMarked(heard, heardExtra, hl)
	if a.Transcript == "" {
		heardText = m.st.dim.Render("nothing")
	}
	rows := []row{{"text", joinMarked(want, wantMissing, hl)}, {"heard", heardText}}
	return m.block(m.st.audio, "♪", "Speech recognition heard something else", fieldName(a.Field)+" · audio", rows,
		"Press p to listen. If it sounds right, the card is right.", faded, w)
}

func joinMarked(ws []string, marked []bool, hl lipgloss.Style) string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w
		if marked[i] {
			out[i] = hl.Underline(true).Render(w)
		}
	}
	return strings.Join(out, " ")
}

func (m *model) proposalView(cur, fresh notes.Note, w int) string {
	lines := []string{m.st.claude.Bold(true).Render("✦ Claude Code suggests this version")}
	fields := []struct {
		label, old, new string
		markup          bool
	}{
		{"word", cur.Arabic, fresh.Arabic, false},
		{"type", deck.PosLabel(cur.Pos), deck.PosLabel(fresh.Pos), false},
		{"meaning", cur.English, fresh.English, false},
		{"hint", cur.Hint, fresh.Hint, false},
		{"forms", formsText(cur), formsText(fresh), false},
		{"sentence", cur.Example, fresh.Example, true},
		{"translation", cur.ExampleEn, fresh.ExampleEn, false},
		{"comment", cur.Comment, fresh.Comment, false},
	}
	for _, f := range fields {
		if f.old == f.new {
			continue
		}
		before, after := m.changed(f.old, f.new, f.markup)
		lines = append(lines, "", m.st.bold.Render(f.label), hang("  "+m.st.bad.Render("−")+" ", before, w), hang("  "+m.st.good.Render("+")+" ", after, w))
	}
	return strings.Join(append(lines, "", m.st.dim.Render("Fields that did not change are not shown.")), "\n")
}

func formsText(n notes.Note) string {
	var parts []string
	for _, f := range n.Forms {
		parts = append(parts, f.Label+" "+f.Arabic)
	}
	return strings.Join(parts, "   ")
}

func (m *model) changed(old, fresh string, markup bool) (string, string) {
	if old == "" {
		return m.st.dim.Render("(none)"), fresh
	}
	if fresh == "" {
		return old, m.st.dim.Render("(none)")
	}
	if markup {
		po, pf := pieces(old), pieces(fresh)
		keepOld, keepFresh := common(words(po), words(pf))
		return m.markChanged(po, keepOld, m.st.bad), m.markChanged(pf, keepFresh, m.st.good)
	}
	wo, wf := strings.Fields(old), strings.Fields(fresh)
	keepOld, keepFresh := common(wo, wf)
	return joinMarked(wo, invert(keepOld), m.st.bad), joinMarked(wf, invert(keepFresh), m.st.good)
}

func (m *model) markChanged(ps []piece, keep []bool, hl lipgloss.Style) string {
	var b strings.Builder
	k := 0
	for _, p := range ps {
		st := m.st.text
		if p.word {
			if !keep[k] {
				st = hl.Underline(true)
			}
			k++
		}
		b.WriteString(renderPiece(p, st, m.st.target))
	}
	return b.String()
}

func invert(bs []bool) []bool {
	out := make([]bool, len(bs))
	for i, b := range bs {
		out[i] = !b
	}
	return out
}

func (m *model) doneView(w int) string {
	s := m.summary()
	title := m.st.good.Bold(true).Render(fmt.Sprintf("✓ All %d flagged notes are done", s.Total))
	if s.Open > 0 {
		title = m.st.bold.Render(fmt.Sprintf("%d of %d flagged notes are done", s.Total-s.Open, s.Total))
	}
	lines := []string{title, ""}
	add := func(n int, st lipgloss.Style, symbol, text string) {
		if n > 0 {
			lines = append(lines, st.Render(fmt.Sprintf("%s %3d", symbol, n))+"  "+text)
		}
	}
	add(s.Kept, m.st.good, "✓", "right as they were")
	add(s.Edited, m.st.good, "✎", "edited by you")
	add(s.Rewritten, m.st.claude, "✦", "rewritten by Claude Code")
	add(s.Open, m.st.dim, "○", "still open")
	if next := NextSteps(s); next != "" {
		lines = append(lines, "", next)
	}
	return m.st.panel.Width(w).Render(strings.Join(lines, "\n"))
}

func NextSteps(s Summary) string {
	switch {
	case s.Changed() > 0:
		return "Next: 'arabic-vocab check' checks the changed notes, then 'arabic-vocab audio' and 'arabic-vocab build'."
	case s.Kept > 0:
		return "Next: 'arabic-vocab build' leaves the flags you cleared out of the deck."
	}
	return ""
}
