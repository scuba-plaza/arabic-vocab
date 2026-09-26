package deck

import (
	"fmt"
	"io"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
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

func Prepare(existing []notes.Note, records []rank.Record, from, to int) ([]notes.Note, int) {
	have := map[string]bool{}
	taken := map[int]bool{}
	for _, n := range existing {
		have[n.ID] = true
		taken[n.Position] = true
	}
	out := append([]notes.Note(nil), existing...)
	added := 0
	for _, rec := range records {
		if rec.Rank < from || rec.Rank > to || have[rec.ID] || taken[rec.Rank] || len(rec.Entries) == 0 {
			continue
		}
		out = append(out, DefaultNote(rec))
		added++
	}
	notes.Sort(out)
	return out, added
}

func WriteWorksheet(w io.Writer, records []rank.Record, from, to int) {
	for _, rec := range records {
		if rec.Rank < from || rec.Rank > to {
			continue
		}
		fmt.Fprintf(w, "## %d  %s", rec.Rank, rec.ID)
		if rec.CEFR != "" {
			fmt.Fprintf(w, "  [%s]", rec.CEFR)
		}
		fmt.Fprintln(w)
		for _, e := range rec.Entries {
			fmt.Fprintf(w, "  %s %s%s\n", e.Pos, e.Canonical, describe(e))
			for i, s := range e.Senses {
				if i == 8 {
					fmt.Fprintf(w, "    … %d more\n", len(e.Senses)-8)
					break
				}
				marker := " "
				if !s.MSA {
					marker = "x"
				}
				tags := ""
				if len(s.Tags) > 0 {
					tags = " {" + strings.Join(s.Tags, ",") + "}"
				}
				fmt.Fprintf(w, "    %s %s%s\n", marker, s.Gloss, tags)
			}
		}
	}
}

func describe(e *lexicon.Entry) string {
	var parts []string
	if e.Gender != "" {
		parts = append(parts, e.Gender)
	}
	if e.VerbForm != "" {
		parts = append(parts, "form "+e.VerbForm)
	}
	if e.NonPast != "" {
		parts = append(parts, "pres. "+e.NonPast)
	}
	if len(e.VerbalNouns) > 0 {
		parts = append(parts, "masdar "+strings.Join(e.VerbalNouns, "/"))
	}
	if e.Feminine != "" {
		parts = append(parts, "f. "+e.Feminine)
	}
	if len(e.Plurals) > 0 {
		parts = append(parts, "pl. "+strings.Join(e.Plurals, "/"))
	}
	if e.Root != "" {
		parts = append(parts, "root "+e.Root)
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, "; ") + ")"
}
