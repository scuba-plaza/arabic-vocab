package deck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type CheckItem struct {
	ID       string   `json:"id"`
	Field    string   `json:"field"`
	Tokens   []string `json:"tokens"`
	Context  bool     `json:"context"`
	Citation bool     `json:"-"`
}

type CheckResult struct {
	ID       string     `json:"id"`
	Field    string     `json:"field"`
	Analyses [][]string `json:"analyses"`
	CATT     []string   `json:"catt"`
	BERT     []string   `json:"bert"`
}

var tagPattern = regexp.MustCompile(`<[^>]*>`)

func PlainText(s string) string {
	return tagPattern.ReplaceAllString(s, "")
}

func CheckItems(ns []notes.Note) []CheckItem {
	var items []CheckItem
	for _, n := range ns {
		items = append(items, CheckItem{ID: n.ID, Field: "arabic", Tokens: tashkeel.Words(n.Arabic), Citation: true})
		if len(n.Forms) > 0 {
			items = append(items, CheckItem{ID: n.ID, Field: "forms", Tokens: tashkeel.Words(n.FormsText()), Citation: true})
		}
		if n.Example != "" {
			items = append(items, CheckItem{ID: n.ID, Field: "example", Tokens: tashkeel.Words(PlainText(n.Example)), Context: true})
		}
	}
	return items
}

func RunChecker(ctx context.Context, python, script string, items []CheckItem, stderr io.Writer) ([]CheckResult, error) {
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	enc.SetEscapeHTML(false)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, python, script)
	cmd.Stdin = &in
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running %s %s: %w", python, script, err)
	}
	results, err := notes.DecodeJSONL[CheckResult](bytes.NewReader(out), script)
	if err != nil {
		return nil, err
	}
	if len(results) != len(items) {
		return nil, fmt.Errorf("%s returned %d results for %d items", script, len(results), len(items))
	}
	return results, nil
}

func anyCompatible(token string, readings []string, citation bool) bool {
	for _, r := range readings {
		if citation && tashkeel.CompatibleCitation(token, r) || !citation && tashkeel.Compatible(token, r) {
			return true
		}
	}
	return false
}

func sample(analyses []string, n int) string {
	if len(analyses) > n {
		analyses = analyses[:n]
	}
	return strings.Join(analyses, "، ")
}

func Evaluate(ns []notes.Note, items []CheckItem, results []CheckResult) []notes.Check {
	byID := map[string]*notes.Check{}
	var order []string
	for _, n := range ns {
		byID[n.ID] = &notes.Check{ID: n.ID, Version: CheckVersion, Digest: n.Digest()}
		order = append(order, n.ID)
	}
	for i, item := range items {
		res := results[i]
		check := byID[item.ID]
		add := func(kind string, sev notes.Severity, word, detail string) *notes.Issue {
			check.Issues = append(check.Issues, notes.Issue{Field: item.Field, Kind: kind, Severity: sev, Word: word, Detail: detail})
			return &check.Issues[len(check.Issues)-1]
		}
		aligned := len(res.CATT) == len(item.Tokens)
		if item.Context && !aligned {
			add("unchecked", notes.Major, "", "CATT's reading could not be aligned with the sentence")
		}
		for t, token := range item.Tokens {
			if missing := tashkeel.UnmarkedLetters(token, item.Citation); len(missing) > 0 {
				var letters []string
				for _, m := range missing {
					letters = append(letters, m.Letter)
				}
				add("unmarked", notes.Major, token, "no vowel mark on "+strings.Join(letters, "، "))
			}
			var analyses []string
			if t < len(res.Analyses) {
				analyses = res.Analyses[t]
			}
			known := len(analyses) > 0
			valid := known && anyCompatible(token, analyses, item.Citation)
			if known && !valid {
				is := add("invalid", notes.Major, token, "CAMeL does not allow these vowels; it knows "+sample(analyses, 4))
				is.Known = analyses[:min(len(analyses), 6)]
			}
			if !item.Context {
				if !known {
					add("unknown", notes.Minor, token, "CAMeL does not know this word, so only Wiktionary vouches for it")
				}
				continue
			}
			if !aligned {
				continue
			}
			catt := res.CATT[t]
			if tashkeel.Compatible(token, catt) {
				continue
			}
			bert := ""
			if t < len(res.BERT) {
				bert = res.BERT[t]
			}
			var is *notes.Issue
			switch {
			case bert != "" && tashkeel.Compatible(token, bert) && valid:
				is = add("diacritics", notes.Minor, token, "CATT reads "+catt+"; CAMeL agrees with the card")
			case bert != "":
				is = add("diacritics", notes.Major, token, "CATT reads "+catt+"; CAMeL reads "+bert)
			default:
				is = add("diacritics", notes.Major, token, "CATT reads "+catt)
			}
			is.CATT, is.CAMeL = catt, bert
		}
	}
	out := make([]notes.Check, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}
