package review

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type Item struct {
	Index  int
	Issues []notes.Issue
	Stale  bool
}

func (it Item) Len() int {
	return len(it.Issues)
}

func (it Item) Major() bool {
	for _, is := range it.Issues {
		if is.Severity == notes.Major {
			return true
		}
	}
	return false
}

type Filter struct {
	Minor bool
	All   bool
}

func Items(ns []notes.Note, checks, audio []notes.Check, filter Filter) ([]Item, int) {
	byID := map[string]notes.Check{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	audioByID := map[string]notes.Check{}
	for _, c := range audio {
		audioByID[c.ID] = c
	}
	var items []Item
	stale := 0
	for i, n := range ns {
		if !n.Authored() {
			continue
		}
		it := Item{Index: i}
		c, ok := byID[n.ID]
		if deck.Current(n, c, ok) {
			for _, is := range deck.OpenIssues(n, c) {
				if filter.Minor || is.Severity == notes.Major {
					it.Issues = append(it.Issues, is)
				}
			}
		} else {
			it.Stale = true
			stale++
		}
		if ac, ok := audioByID[n.ID]; ok {
			it.Issues = append(it.Issues, deck.OpenAudioIssues(n, ac)...)
		}
		if it.Len() > 0 || filter.All {
			items = append(items, it)
		}
	}
	return items, stale
}

func Feedback(it Item) string {
	var b strings.Builder
	issues := slices.DeleteFunc(slices.Clone(it.Issues), func(is notes.Issue) bool { return is.Kind == deck.KindSilent })
	if len(issues) == 0 {
		b.WriteString("Nothing was flagged on this card; it is being gone over anyway.\n")
	} else {
		b.WriteString("An automatic check disagreed with this card:\n")
	}
	for _, is := range issues {
		if is.Word != "" {
			fmt.Fprintf(&b, "- %s in the %s: %s\n", is.Word, is.Field, is.Detail)
		} else {
			fmt.Fprintf(&b, "- the %s: %s\n", is.Field, is.Detail)
		}
	}
	b.WriteString("If the card is wrong, correct it. If it is right but the example can be read another way without the vowel marks, rewrite the example so it reads only one way.")
	return b.String()
}

type VoiceOption struct {
	Name   string `json:"name"`
	Tier   string `json:"tier,omitempty"`
	Gender string `json:"gender,omitempty"`
}

type Options struct {
	Save     func([]notes.Note) error
	Rewrite  func(ctx context.Context, n notes.Note, feedback string) (notes.Note, error)
	Clip     func(n notes.Note, field string) string
	Remake   func(ctx context.Context, n notes.Note, field string) (*notes.Issue, error)
	Remove   func(n notes.Note, field string) error
	Flags    func(ns []notes.Note) map[string][]notes.Issue
	Voice    string
	Voices   func(ctx context.Context) ([]VoiceOption, error)
	SetVoice func(name string) error
	All      bool
	FontPath string
}

type Summary struct {
	Total     int `json:"total"`
	Kept      int `json:"kept"`
	Edited    int `json:"edited"`
	Rewritten int `json:"rewritten"`
	Open      int `json:"open"`
}

func (s Summary) Changed() int {
	return s.Edited + s.Rewritten
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
