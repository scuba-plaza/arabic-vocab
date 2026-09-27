package review

import (
	"slices"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type endingFix struct {
	Source  string   `json:"source"`
	Words   []string `json:"words"`
	Example string   `json:"-"`
}

func bareEnding(is notes.Issue) bool {
	return is.Kind == "unmarked" && is.Field == "example" && slices.Equal(is.Missing, []int{tashkeel.EndingIndex(is.Word)})
}

func markupOf(ps []piece) string {
	var b strings.Builder
	bold := false
	for _, p := range ps {
		for k, r := range p.runes {
			if p.bold[k] != bold {
				bold = p.bold[k]
				if bold {
					b.WriteString("<b>")
				} else {
					b.WriteString("</b>")
				}
			}
			b.WriteRune(r)
		}
	}
	if bold {
		b.WriteString("</b>")
	}
	return b.String()
}

func withEndings(example string, issues []notes.Issue, reading func(notes.Issue) string) (string, []string, bool) {
	ps := pieces(example)
	if markupOf(ps) != example {
		return "", nil, false
	}
	var words []string
	next := 0
	for _, is := range issues {
		if !bareEnding(is) {
			continue
		}
		at, marks, ok := tashkeel.EndingMarks(is.Word, reading(is))
		if !ok {
			return "", nil, false
		}
		k := next
		for k < len(ps) && !(ps[k].word && ps[k].String() == is.Word) {
			k++
		}
		if k == len(ps) {
			return "", nil, false
		}
		p := &ps[k]
		flag := p.bold[max(at-1, 0)]
		p.runes = slices.Insert(p.runes, at, marks...)
		p.bold = slices.Insert(p.bold, at, slices.Repeat([]bool{flag}, len(marks))...)
		words = append(words, p.String())
		next = k + 1
	}
	if len(words) == 0 {
		return "", nil, false
	}
	return markupOf(ps), words, true
}

func endingFixes(n notes.Note, issues []notes.Issue) []endingFix {
	var out []endingFix
	for _, source := range []string{"catt", "camel"} {
		example, words, ok := withEndings(n.Example, issues, func(is notes.Issue) string {
			if source == "catt" {
				return is.CATT
			}
			return is.CAMeL
		})
		if !ok {
			continue
		}
		if len(out) == 1 && out[0].Example == example {
			out[0].Source = "both"
			continue
		}
		out = append(out, endingFix{Source: source, Words: words, Example: example})
	}
	return out
}
