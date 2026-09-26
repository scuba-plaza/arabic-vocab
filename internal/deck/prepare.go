package deck

import (
	"slices"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func DefaultNote(rec rank.Record) notes.Note {
	e := rec.Entries[0]
	n := notes.Note{
		ID:       rec.ID,
		Position: rec.Rank,
		Arabic:   e.Canonical,
		Pos:      e.Pos,
		Gender:   e.Gender,
		VerbForm: e.VerbForm,
		Root:     e.Root,
		CEFR:     rec.CEFR,
		Source:   e.URL(),
	}
	add := func(label, arabic string) {
		if arabic != "" {
			n.Forms = append(n.Forms, notes.Form{Label: label, Arabic: arabic})
		}
	}
	switch e.Pos {
	case "noun":
		for i, p := range e.Plurals {
			if i == 2 {
				break
			}
			add("pl.", p)
		}
	case "verb":
		add("pres.", e.NonPast)
		if len(e.VerbalNouns) > 0 {
			add("masdar", e.VerbalNouns[0])
		}
	case "adj":
		add("f.", e.Feminine)
		if len(e.Plurals) > 0 {
			add("pl.", e.Plurals[0])
		}
	case "pron", "det":
		add("f.", e.Feminine)
	}
	return n
}

func NextWords(existing []notes.Note, records []rank.Record, n int) ([]notes.Note, []int) {
	have := map[string]bool{}
	taken := map[int]bool{}
	var ids []string
	for _, x := range existing {
		have[x.ID] = true
		taken[x.Position] = true
		if !x.Authored() && len(ids) < n {
			ids = append(ids, x.ID)
		}
	}
	out := slices.Clone(existing)
	ranked := slices.Clone(records)
	slices.SortStableFunc(ranked, func(a, b rank.Record) int { return a.Rank - b.Rank })
	for _, rec := range ranked {
		if len(ids) >= n {
			break
		}
		if have[rec.ID] || taken[rec.Rank] || len(rec.Entries) == 0 {
			continue
		}
		out = append(out, DefaultNote(rec))
		taken[rec.Rank] = true
		ids = append(ids, rec.ID)
	}
	notes.Sort(out)
	var targets []int
	for i, x := range out {
		if slices.Contains(ids, x.ID) {
			targets = append(targets, i)
		}
	}
	return out, targets
}
