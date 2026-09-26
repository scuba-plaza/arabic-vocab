package review

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type Item struct {
	Index  int
	Issues []notes.Issue
	Audio  []notes.AudioCheck
}

func (it Item) Len() int {
	return len(it.Issues) + len(it.Audio)
}

func (it Item) Major() bool {
	for _, is := range it.Issues {
		if is.Severity == notes.Major {
			return true
		}
	}
	return len(it.Audio) > 0
}

func Items(ns []notes.Note, checks []notes.Check, audio []notes.AudioCheck, index map[string]string, minor bool) ([]Item, int) {
	byID := map[string]notes.Check{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	audioByID := map[string][]notes.AudioCheck{}
	for _, a := range audio {
		audioByID[a.ID] = append(audioByID[a.ID], a)
	}
	var items []Item
	stale := 0
	for i, n := range ns {
		if !n.Authored() {
			continue
		}
		it := Item{Index: i, Audio: deck.OpenAudio(n, audioByID[n.ID], index)}
		c, ok := byID[n.ID]
		if deck.Current(n, c, ok) {
			for _, is := range deck.OpenIssues(n, c) {
				if minor || is.Severity == notes.Major {
					it.Issues = append(it.Issues, is)
				}
			}
		} else {
			stale++
		}
		if it.Len() > 0 {
			items = append(items, it)
		}
	}
	return items, stale
}

func Feedback(it Item) string {
	var b strings.Builder
	b.WriteString("An automatic check disagreed with this card:\n")
	for _, is := range it.Issues {
		if is.Word != "" {
			fmt.Fprintf(&b, "- %s in the %s: %s\n", is.Word, is.Field, is.Detail)
		} else {
			fmt.Fprintf(&b, "- the %s: %s\n", is.Field, is.Detail)
		}
	}
	for _, a := range it.Audio {
		fmt.Fprintf(&b, "- speech recognition heard %q when the %s audio was played back\n", a.Transcript, strings.ToLower(strings.TrimSuffix(a.Field, "Audio")))
	}
	b.WriteString("If the card is wrong, correct it. If it is right but the example can be read another way without the vowel marks, rewrite the example so it reads only one way.")
	return b.String()
}

type Options struct {
	Save       func([]notes.Note) error
	Rewrite    func(ctx context.Context, n notes.Note, feedback string) (notes.Note, error)
	Clip       func(n notes.Note, field string) string
	Play       func(ctx context.Context, path string) error
	Editor     []string
	ScratchDir string
}

type Summary struct {
	Total     int
	Kept      int
	Edited    int
	Rewritten int
	Open      int
}

func (s Summary) Changed() int {
	return s.Edited + s.Rewritten
}

func Run(ctx context.Context, ns []notes.Note, items []Item, opts Options) (Summary, error) {
	m := newModel(ctx, ns, items, opts)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	m.stopAll()
	if m.err != nil {
		return m.summary(), m.err
	}
	if errors.Is(err, tea.ErrInterrupted) || (errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil) {
		err = nil
	}
	return m.summary(), err
}
