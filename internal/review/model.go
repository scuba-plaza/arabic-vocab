package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type state int

const (
	open state = iota
	kept
	edited
	rewritten
)

type entry struct {
	Item
	state    state
	asking   bool
	request  int
	cancel   context.CancelFunc
	since    time.Time
	proposal *notes.Note
	draft    []byte
}

type step struct {
	entry int
	note  notes.Note
	state state
}

type tone int

const (
	info tone = iota
	good
	bad
	claude
)

type editJob struct {
	entry    int
	proposal bool
	path     string
	before   notes.Note
}

type rewriteMsg struct {
	entry   int
	request int
	note    notes.Note
	err     error
}

type editedMsg struct{ err error }

type playedMsg struct {
	id  int
	err error
}

type tickMsg struct{}

type model struct {
	ctx        context.Context
	opts       Options
	ns         []notes.Note
	entries    []*entry
	cur        int
	finished   bool
	width      int
	height     int
	st         styles
	scroll     int
	maxScroll  int
	status     string
	statusTone tone
	playID     int
	playing    string
	stopPlay   context.CancelFunc
	frame      int
	ticking    bool
	history    []step
	editing    *editJob
	err        error
}

func newModel(ctx context.Context, ns []notes.Note, items []Item, opts Options) *model {
	m := &model{ctx: ctx, opts: opts, ns: ns, st: newStyles(true)}
	for _, it := range items {
		m.entries = append(m.entries, &entry{Item: it})
	}
	m.finished = len(m.entries) == 0
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.RequestBackgroundColor
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.st = newStyles(msg.IsDark())
	case tea.KeyPressMsg:
		return m, m.key(keyName(msg))
	case rewriteMsg:
		return m, m.rewritten(msg)
	case editedMsg:
		return m, m.edited(msg)
	case playedMsg:
		if msg.id == m.playID {
			m.playing, m.stopPlay = "", nil
			if msg.err != nil {
				m.say(bad, "Could not play the clip: "+msg.err.Error())
			}
		}
	case tickMsg:
		m.ticking = false
		if m.busy() {
			m.frame++
			return m, m.tick()
		}
	}
	return m, nil
}

func keyName(msg tea.KeyPressMsg) string {
	k := msg.Key()
	if k.BaseCode > 0 && k.BaseCode < utf8.RuneSelf && k.Text != "" && k.Text[0] >= utf8.RuneSelf {
		return string(k.BaseCode)
	}
	return msg.String()
}

func (m *model) key(k string) tea.Cmd {
	switch k {
	case "ctrl+c", "q":
		m.stopAll()
		return tea.Quit
	case "up", "k":
		m.scroll = max(0, min(m.scroll, m.maxScroll)-1)
		return nil
	case "down", "j":
		m.scroll = min(m.maxScroll, m.scroll+1)
		return nil
	case "pgup":
		m.scroll = max(0, min(m.scroll, m.maxScroll)-10)
		return nil
	case "pgdown":
		m.scroll = min(m.maxScroll, m.scroll+10)
		return nil
	case "home":
		m.scroll = 0
		return nil
	case "end":
		m.scroll = m.maxScroll
		return nil
	case "u":
		return m.undo()
	}
	if m.finished {
		switch k {
		case "left", "shift+tab", "backspace":
			if len(m.entries) > 0 {
				m.show(len(m.entries) - 1)
			}
		case "enter":
			if i := m.nextOpen(-1); i >= 0 {
				m.show(i)
			}
		}
		return nil
	}
	e := m.entries[m.cur]
	switch k {
	case "right", "tab", "space", "s":
		if m.cur == len(m.entries)-1 {
			m.stopPlayback()
			m.finished, m.scroll = true, 0
			return nil
		}
		m.show(m.cur + 1)
		return nil
	case "left", "shift+tab", "backspace":
		if m.cur > 0 {
			m.show(m.cur - 1)
		}
		return nil
	case "p":
		return m.play("ExampleAudio", "sentence")
	case "w":
		return m.play("WordAudio", "word")
	}
	if e.proposal != nil {
		switch k {
		case "y":
			return m.keepProposal()
		case "n", "esc":
			e.proposal = nil
			m.say(info, "Kept your version of "+m.word(e))
		case "e":
			return m.edit(true)
		}
		return nil
	}
	switch k {
	case "enter":
		return m.keep()
	case "e":
		return m.edit(false)
	case "c":
		return m.ask()
	case "esc":
		if e.asking {
			e.cancel()
			e.asking = false
			m.say(info, "Stopped asking Claude Code about "+m.word(e))
		}
	}
	return nil
}

func (m *model) word(e *entry) string {
	return m.ns[e.Index].Arabic
}

func (m *model) say(t tone, s string) {
	m.status, m.statusTone = s, t
}

func (m *model) show(i int) {
	if i != m.cur || m.finished {
		m.stopPlayback()
	}
	m.cur, m.finished, m.scroll = i, false, 0
}

func (m *model) nextOpen(from int) int {
	for k := 1; k <= len(m.entries); k++ {
		i := (from + k + len(m.entries)) % len(m.entries)
		if m.entries[i].state == open {
			return i
		}
	}
	return -1
}

func (m *model) advance() {
	if i := m.nextOpen(m.cur); i >= 0 {
		m.show(i)
		return
	}
	m.stopPlayback()
	m.finished, m.scroll = true, 0
}

func (m *model) fail(err error) tea.Cmd {
	m.err = err
	m.stopAll()
	return tea.Quit
}

func (m *model) record(i int, s state, n notes.Note) error {
	e := m.entries[i]
	old := m.ns[e.Index]
	m.ns[e.Index] = n
	if err := m.opts.Save(m.ns); err != nil {
		m.ns[e.Index] = old
		return err
	}
	m.history = append(m.history, step{entry: i, note: old, state: e.state})
	e.state = s
	e.proposal, e.draft = nil, nil
	if e.asking {
		e.cancel()
		e.asking = false
	}
	return nil
}

func (m *model) keep() tea.Cmd {
	e := m.entries[m.cur]
	if e.state != open {
		m.advance()
		return nil
	}
	n := clone(m.ns[e.Index])
	for _, is := range e.Issues {
		if key := deck.IssueKey(is); !slices.Contains(n.Reviewed, key) {
			n.Reviewed = append(n.Reviewed, key)
		}
	}
	for _, a := range e.Audio {
		if !slices.Contains(n.ReviewedAudio, a.File) {
			n.ReviewedAudio = append(n.ReviewedAudio, a.File)
		}
	}
	if err := m.record(m.cur, kept, n); err != nil {
		return m.fail(err)
	}
	m.say(good, "✓ "+n.Arabic+" is right as it is; its flags are gone from the next build")
	m.advance()
	return nil
}

func (m *model) replace(i int, n notes.Note, s state) (bool, error) {
	e := m.entries[i]
	old := m.ns[e.Index]
	if n.ID != old.ID {
		return false, errors.New("the id cannot change: it is the note's identity in Anki")
	}
	m.ns[e.Index] = n
	err := notes.Validate(m.ns)
	m.ns[e.Index] = old
	if err != nil {
		return false, err
	}
	return true, m.record(i, s, n)
}

func (m *model) keepProposal() tea.Cmd {
	e := m.entries[m.cur]
	ok, err := m.replace(m.cur, *e.proposal, rewritten)
	if !ok {
		m.say(bad, "Not saved: "+err.Error())
		return nil
	}
	if err != nil {
		return m.fail(err)
	}
	m.say(good, "✦ Saved Claude Code's version of "+m.word(e)+"; run check again before building")
	m.advance()
	return nil
}

func (m *model) ask() tea.Cmd {
	e := m.entries[m.cur]
	switch {
	case m.opts.Rewrite == nil:
		m.say(bad, "Claude Code was not found; install it and log in with 'claude' to use c")
		return nil
	case e.asking:
		m.say(info, "Claude Code is already working on "+m.word(e))
		return nil
	case e.state == edited || e.state == rewritten:
		m.say(info, "This note changed after the check; run 'arabic-vocab check' before asking Claude Code about it")
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	e.request++
	e.asking, e.cancel, e.since = true, cancel, time.Now()
	i, request, n, feedback, rewrite := m.cur, e.request, clone(m.ns[e.Index]), Feedback(e.Item), m.opts.Rewrite
	m.say(info, "")
	return tea.Batch(func() tea.Msg {
		fresh, err := rewrite(ctx, n, feedback)
		return rewriteMsg{entry: i, request: request, note: fresh, err: err}
	}, m.tick())
}

func (m *model) rewritten(msg rewriteMsg) tea.Cmd {
	e := m.entries[msg.entry]
	if !e.asking || msg.request != e.request {
		return nil
	}
	e.asking = false
	e.cancel()
	word := m.word(e)
	switch {
	case msg.err != nil:
		m.say(bad, "Claude Code could not write a new version of "+word+": "+msg.err.Error())
	case same(msg.note, m.ns[e.Index]):
		m.say(info, "Claude Code would keep "+word+" as it is")
	default:
		fresh := msg.note
		e.proposal = &fresh
		if msg.entry == m.cur && !m.finished {
			m.scroll = 0
			m.say(claude, "✦ Claude Code suggests a new version; the changes are highlighted")
		} else {
			m.say(claude, fmt.Sprintf("✦ Claude Code's version of %s is ready (note %d)", word, msg.entry+1))
		}
	}
	return nil
}

func (m *model) edit(proposal bool) tea.Cmd {
	e := m.entries[m.cur]
	switch {
	case len(m.opts.Editor) == 0:
		m.say(bad, "No editor is set; set $EDITOR to use e")
		return nil
	case e.asking:
		m.say(info, "Claude Code is still working on this note; press esc to stop it first")
		return nil
	}
	src := m.ns[e.Index]
	if proposal {
		src = *e.proposal
	}
	text := e.draft
	if text == nil {
		var err error
		if text, err = encodeNote(src); err != nil {
			m.say(bad, err.Error())
			return nil
		}
	}
	path := filepath.Join(m.opts.ScratchDir, fmt.Sprintf("note-%d.json", src.Position))
	if err := os.WriteFile(path, text, 0o600); err != nil {
		m.say(bad, err.Error())
		return nil
	}
	m.stopPlayback()
	m.editing = &editJob{entry: m.cur, proposal: proposal, path: path, before: src}
	c := exec.Command(m.opts.Editor[0], append(slices.Clone(m.opts.Editor[1:]), path)...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return editedMsg{err: err} })
}

func (m *model) edited(msg editedMsg) tea.Cmd {
	job := m.editing
	m.editing = nil
	if job == nil {
		return nil
	}
	e := m.entries[job.entry]
	raw, err := os.ReadFile(job.path)
	os.Remove(job.path)
	if msg.err != nil {
		m.say(bad, fmt.Sprintf("%s failed: %v", m.opts.Editor[0], msg.err))
		return nil
	}
	if err != nil {
		m.say(bad, err.Error())
		return nil
	}
	n, err := decodeNote(raw)
	if err != nil {
		e.draft = raw
		m.say(bad, "Not saved, the JSON has a mistake: "+err.Error()+". Press e to fix it")
		return nil
	}
	e.draft = nil
	if same(n, job.before) {
		m.say(info, "No changes")
		return nil
	}
	ok, err := m.replace(job.entry, n, edited)
	if !ok {
		e.draft = raw
		m.say(bad, "Not saved: "+err.Error()+". Press e to fix it")
		return nil
	}
	if err != nil {
		return m.fail(err)
	}
	m.say(good, "✎ Saved your changes to "+n.Arabic+"; run check again before building")
	m.advance()
	return nil
}

func (m *model) undo() tea.Cmd {
	if len(m.history) == 0 {
		m.say(info, "Nothing to undo")
		return nil
	}
	last := m.history[len(m.history)-1]
	e := m.entries[last.entry]
	current := m.ns[e.Index]
	m.ns[e.Index] = last.note
	if err := m.opts.Save(m.ns); err != nil {
		m.ns[e.Index] = current
		return m.fail(err)
	}
	m.history = m.history[:len(m.history)-1]
	e.state, e.draft = last.state, nil
	m.show(last.entry)
	m.say(info, "↶ Undid your last change to "+last.note.Arabic)
	return nil
}

func (m *model) play(field, label string) tea.Cmd {
	if m.opts.Clip == nil || m.opts.Play == nil {
		return nil
	}
	e := m.entries[m.cur]
	path := m.opts.Clip(m.ns[e.Index], field)
	if path == "" {
		if e.state == edited || e.state == rewritten {
			m.say(info, "There is no audio for the new version yet; 'arabic-vocab audio' makes it")
		} else {
			m.say(info, "There is no audio for this "+label+" yet; 'arabic-vocab audio' makes it")
		}
		return nil
	}
	m.stopPlayback()
	ctx, cancel := context.WithCancel(m.ctx)
	m.playID++
	id, play := m.playID, m.opts.Play
	m.playing, m.stopPlay = label, cancel
	return tea.Batch(func() tea.Msg {
		err := play(ctx, path)
		if ctx.Err() != nil {
			err = nil
		}
		return playedMsg{id: id, err: err}
	}, m.tick())
}

func (m *model) stopPlayback() {
	if m.stopPlay != nil {
		m.stopPlay()
	}
	m.playID++
	m.playing, m.stopPlay = "", nil
}

func (m *model) stopAll() {
	m.stopPlayback()
	for _, e := range m.entries {
		if e.asking {
			e.cancel()
			e.asking = false
		}
	}
}

func (m *model) busy() bool {
	if m.playing != "" {
		return true
	}
	for _, e := range m.entries {
		if e.asking {
			return true
		}
	}
	return false
}

func (m *model) tick() tea.Cmd {
	if m.ticking {
		return nil
	}
	m.ticking = true
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) summary() Summary {
	s := Summary{Total: len(m.entries)}
	for _, e := range m.entries {
		switch e.state {
		case open:
			s.Open++
		case kept:
			s.Kept++
		case edited:
			s.Edited++
		case rewritten:
			s.Rewritten++
		}
	}
	return s
}

func clone(n notes.Note) notes.Note {
	n.Forms = slices.Clone(n.Forms)
	n.Reviewed = slices.Clone(n.Reviewed)
	n.ReviewedAudio = slices.Clone(n.ReviewedAudio)
	if n.Production != nil {
		p := *n.Production
		n.Production = &p
	}
	return n
}

func encodeNote(n notes.Note) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func decodeNote(raw []byte) (notes.Note, error) {
	var n notes.Note
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&n)
	return n, err
}

func same(a, b notes.Note) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
