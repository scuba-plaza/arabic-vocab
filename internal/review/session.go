package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

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

func (s state) String() string {
	return [...]string{"open", "kept", "edited", "rewritten"}[s]
}

type entry struct {
	Item
	state      state
	asking     bool
	working    bool
	request    int
	cancel     context.CancelFunc
	since      time.Time
	proposal   *notes.Note
	notice     string
	noticeTone string
}

type step struct {
	entry int
	note  notes.Note
	state state
}

type userError struct{ error }

func userErrorf(format string, args ...any) error {
	return userError{fmt.Errorf(format, args...)}
}

type result struct {
	Message string `json:"message,omitempty"`
	Tone    string `json:"tone,omitempty"`
	Show    int    `json:"show"`
}

type session struct {
	mu      sync.Mutex
	ctx     context.Context
	opts    Options
	voice   string
	ns      []notes.Note
	entries []*entry
	history []step
}

func newSession(ctx context.Context, ns []notes.Note, items []Item, opts Options) *session {
	s := &session{ctx: ctx, opts: opts, voice: opts.Voice, ns: ns}
	for _, it := range items {
		s.entries = append(s.entries, &entry{Item: it})
	}
	return s
}

func (s *session) entry(i int) (*entry, error) {
	if i < 0 || i >= len(s.entries) {
		return nil, userErrorf("there is no flagged note %d", i+1)
	}
	return s.entries[i], nil
}

func (s *session) nextOpen(from int) int {
	for k := 1; k <= len(s.entries); k++ {
		i := (from + k + len(s.entries)) % len(s.entries)
		if s.entries[i].state == open {
			return i
		}
	}
	return -1
}

func (s *session) record(i int, st state, n notes.Note) error {
	e := s.entries[i]
	old := s.ns[e.Index]
	if !same(n, old) {
		s.ns[e.Index] = n
		if err := s.opts.Save(s.ns); err != nil {
			s.ns[e.Index] = old
			return fmt.Errorf("saving notes.jsonl failed, so nothing changed: %w", err)
		}
	}
	s.history = append(s.history, step{entry: i, note: old, state: e.state})
	e.state = st
	e.proposal, e.notice, e.noticeTone = nil, "", ""
	if e.asking {
		e.cancel()
		e.asking = false
	}
	return nil
}

func (s *session) replace(i int, n notes.Note, st state) error {
	e := s.entries[i]
	old := s.ns[e.Index]
	if n.ID != old.ID {
		return userErrorf("the id cannot change: it is the note's identity in Anki")
	}
	s.ns[e.Index] = n
	err := notes.Validate(s.ns)
	s.ns[e.Index] = old
	if err != nil {
		return userError{err}
	}
	return s.record(i, st, n)
}

func (s *session) keep(i int) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	if e.state != open {
		return result{Show: s.nextOpen(i)}, nil
	}
	n := clone(s.ns[e.Index])
	acknowledged, silent := 0, 0
	for _, is := range e.Issues {
		if is.Kind == deck.KindSilent {
			silent++
			continue
		}
		acknowledged++
		if key := deck.IssueKey(is); !slices.Contains(n.Reviewed, key) {
			n.Reviewed = append(n.Reviewed, key)
		}
	}
	if err := s.record(i, kept, n); err != nil {
		return result{}, err
	}
	message := "✓ " + n.Arabic + " is right as it is"
	switch {
	case acknowledged > 0 && silent > 0:
		message += "; its other flags stay out of the next build, but a clip without sound stays flagged until it is made again"
	case acknowledged > 0:
		message += "; its flags stay out of the next build"
	case silent > 0:
		message += "; a clip without sound stays flagged until it is made again, so Remake it or edit the text"
	}
	return result{Message: message, Tone: "good", Show: s.nextOpen(i)}, nil
}

func (s *session) save(i int, n notes.Note) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	if same(n, s.ns[e.Index]) {
		return result{Message: "No changes", Show: i}, nil
	}
	if err := s.replace(i, n, edited); err != nil {
		return result{}, err
	}
	return result{Message: "✎ Saved your changes to " + n.Arabic + "; run check again before building", Tone: "good", Show: s.nextOpen(i)}, nil
}

func (s *session) use(i int) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	if e.proposal == nil {
		return result{}, userErrorf("there is no suggestion from Claude Code for this note")
	}
	n := *e.proposal
	if err := s.replace(i, n, rewritten); err != nil {
		return result{}, err
	}
	return result{Message: "✦ Saved Claude Code's version of " + n.Arabic + "; run check again before building", Tone: "claude", Show: s.nextOpen(i)}, nil
}

func (s *session) drop(i int) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	e.proposal = nil
	return result{Message: "Kept your version of " + s.ns[e.Index].Arabic, Show: i}, nil
}

func (s *session) ending(i int, source string) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	var fix *endingFix
	if e.state == open {
		for _, f := range endingFixes(s.ns[e.Index], e.Issues) {
			if f.Source == source {
				fix = &f
			}
		}
	}
	if fix == nil {
		return result{}, userErrorf("there is no ending to add to this note from %s", source)
	}
	n := clone(s.ns[e.Index])
	n.Example = fix.Example
	if err := s.replace(i, n, edited); err != nil {
		return result{}, err
	}
	whose := map[string]string{"catt": "CATT's", "camel": "CAMeL's", "both": "the"}[source]
	what := " ending: "
	if len(fix.Words) > 1 {
		what = " endings: "
	}
	return result{Message: "✎ Added " + whose + what + strings.Join(fix.Words, " ") + "; run check again before building", Tone: "good", Show: s.nextOpen(i)}, nil
}

func (s *session) remake(i int, field string) (result, error) {
	s.mu.Lock()
	e, err := s.entry(i)
	if err == nil && s.opts.Remake == nil {
		err = userErrorf("this review cannot make audio; run 'arabic-vocab audio' instead")
	}
	if err != nil {
		s.mu.Unlock()
		return result{}, err
	}
	if e.working {
		s.mu.Unlock()
		return result{Show: i}, nil
	}
	e.working = true
	n, remake := clone(s.ns[e.Index]), s.opts.Remake
	s.mu.Unlock()
	problem, err := remake(s.ctx, n, field)
	s.mu.Lock()
	defer s.mu.Unlock()
	e.working = false
	if err != nil {
		return result{}, err
	}
	made := "♪ Made the " + fieldName(field) + " audio of " + n.Arabic + " again"
	e.Issues = withoutClipIssue(e.Issues, field)
	if problem != nil {
		e.Issues = append(e.Issues, *problem)
	}
	s.refreshFlags()
	if problem != nil {
		return result{Message: made + ", but it came back without any sound; the card has no audio for it yet", Tone: "bad", Show: i}, nil
	}
	return result{Message: made + "; listen to it", Tone: "audio", Show: i}, nil
}

func withoutClipIssue(issues []notes.Issue, field string) []notes.Issue {
	return slices.DeleteFunc(slices.Clone(issues), func(is notes.Issue) bool { return is.Field == field && is.Kind == deck.KindSilent })
}

func (s *session) refreshFlags() {
	if s.opts.Flags == nil {
		return
	}
	fresh := s.opts.Flags(s.ns)
	for _, e := range s.entries {
		kept := slices.DeleteFunc(slices.Clone(e.Issues), func(is notes.Issue) bool { return is.Kind == deck.KindSilent })
		for _, is := range fresh[s.ns[e.Index].ID] {
			if is.Kind == deck.KindSilent {
				kept = append(kept, is)
			}
		}
		e.Issues = kept
	}
}

func (s *session) setVoice(i int, name string) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.SetVoice == nil {
		return result{}, userErrorf("this review cannot change the voice; use 'arabic-vocab audio --voice'")
	}
	if name == "" {
		return result{}, userErrorf("no voice was given")
	}
	if name == s.voice {
		return result{Show: i}, nil
	}
	if err := s.opts.SetVoice(name); err != nil {
		return result{}, err
	}
	s.voice = name
	return result{
		Message: "♪ " + name + " speaks every clip made from now on, here and in 'arabic-vocab audio'; clips already made keep their voice until you remake them",
		Tone:    "audio", Show: i,
	}, nil
}

func (s *session) voices(ctx context.Context) (voicesView, error) {
	s.mu.Lock()
	list, current := s.opts.Voices, s.voice
	s.mu.Unlock()
	v := voicesView{Voice: current, Voices: []VoiceOption{}}
	if list == nil {
		return v, nil
	}
	voices, err := list(ctx)
	if err != nil {
		return v, err
	}
	v.Voices = voices
	return v, nil
}

func (s *session) removeClip(i int, field string) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err == nil && s.opts.Remove == nil {
		err = userErrorf("this review cannot remove audio files")
	}
	if err != nil {
		return result{}, err
	}
	n := s.ns[e.Index]
	if err := s.opts.Remove(n, field); err != nil {
		return result{}, err
	}
	e.Issues = withoutClipIssue(e.Issues, field)
	s.refreshFlags()
	return result{Message: "♪ Removed the " + fieldName(field) + " audio of " + n.Arabic + "; 'Remake' or 'arabic-vocab audio' makes it again", Show: i}, nil
}

func (s *session) ask(i int) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	switch {
	case s.opts.Rewrite == nil:
		return result{}, userErrorf("Claude Code was not found; install it and log in with 'claude'")
	case e.asking:
		return result{Show: i}, nil
	case e.state == edited || e.state == rewritten:
		return result{}, userErrorf("this note changed after the check; run 'arabic-vocab check' before asking Claude Code about it")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	e.request++
	e.asking, e.cancel, e.since = true, cancel, time.Now()
	e.proposal, e.notice, e.noticeTone = nil, "", ""
	request, n, feedback, rewrite := e.request, clone(s.ns[e.Index]), Feedback(e.Item), s.opts.Rewrite
	go func() {
		fresh, err := rewrite(ctx, n, feedback)
		s.mu.Lock()
		defer s.mu.Unlock()
		if !e.asking || e.request != request {
			return
		}
		e.asking = false
		cancel()
		switch {
		case err != nil:
			e.notice, e.noticeTone = "Claude Code could not write a new version: "+err.Error(), "bad"
		case same(fresh, s.ns[e.Index]):
			e.notice, e.noticeTone = "Claude Code would keep this note as it is", "info"
		default:
			e.proposal = &fresh
		}
	}()
	return result{Show: i}, nil
}

func (s *session) stop(i int) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(i)
	if err != nil {
		return result{}, err
	}
	if e.asking {
		e.cancel()
		e.asking = false
	}
	return result{Message: "Stopped asking Claude Code about " + s.ns[e.Index].Arabic, Show: i}, nil
}

func (s *session) undo() (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) == 0 {
		return result{}, userErrorf("there is nothing to undo")
	}
	last := s.history[len(s.history)-1]
	e := s.entries[last.entry]
	current := s.ns[e.Index]
	s.ns[e.Index] = last.note
	if err := s.opts.Save(s.ns); err != nil {
		s.ns[e.Index] = current
		return result{}, fmt.Errorf("saving notes.jsonl failed, so nothing changed: %w", err)
	}
	s.history = s.history[:len(s.history)-1]
	e.state = last.state
	return result{Message: "↶ Undid your last change to " + last.note.Arabic, Show: last.entry}, nil
}

func (s *session) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.asking {
			e.cancel()
			e.asking = false
		}
	}
}

func (s *session) summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count()
}

func (s *session) count() Summary {
	sum := Summary{Total: len(s.entries)}
	for _, e := range s.entries {
		switch e.state {
		case open:
			sum.Open++
		case kept:
			sum.Kept++
		case edited:
			sum.Edited++
		case rewritten:
			sum.Rewritten++
		}
	}
	return sum
}

func clone(n notes.Note) notes.Note {
	n.Forms = slices.Clone(n.Forms)
	n.Reviewed = slices.Clone(n.Reviewed)
	if n.Production != nil {
		p := *n.Production
		n.Production = &p
	}
	return n
}

func same(a, b notes.Note) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

func isUserError(err error) bool {
	var u userError
	return errors.As(err, &u)
}
