package deck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
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

func missingDetail(missing []tashkeel.MissingMark, catt, bert string) string {
	var letters []string
	ending := false
	for _, m := range missing {
		if m.Ending {
			ending = true
		} else {
			letters = append(letters, m.Letter)
		}
	}
	detail := "no vowel mark on " + strings.Join(letters, "، ")
	switch {
	case len(letters) == 0:
		detail = "the ending has no vowel mark"
	case ending:
		detail += " and the ending"
	}
	if catt != "" {
		detail += "; CATT reads " + catt
	}
	if bert != "" {
		detail += "; CAMeL reads " + bert
	}
	return detail
}

func restates(token, reading string, missing []tashkeel.MissingMark) bool {
	diff := tashkeel.Differences(token, reading)
	if len(diff) == 0 || len(missing) == 0 {
		return false
	}
	for i, d := range diff {
		if d && !slices.ContainsFunc(missing, func(m tashkeel.MissingMark) bool { return m.Index == i }) {
			return false
		}
	}
	return true
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
			var analyses []string
			if t < len(res.Analyses) {
				analyses = res.Analyses[t]
			}
			known := len(analyses) > 0
			valid := known && anyCompatible(token, analyses, item.Citation)
			catt, bert := "", ""
			if item.Context && aligned {
				catt = res.CATT[t]
			}
			if item.Context && t < len(res.BERT) {
				bert = res.BERT[t]
			}
			whole := catt != "" && tashkeel.Compatible(token, catt)
			stem := known && anyCompatible(token, analyses, true) || catt != "" && tashkeel.CompatibleCitation(token, catt)
			missing := slices.DeleteFunc(tashkeel.UnmarkedLetters(token, item.Citation), func(m tashkeel.MissingMark) bool {
				return whole || stem && !m.Ending
			})
			if len(missing) > 0 {
				is := add("unmarked", notes.Major, token, missingDetail(missing, catt, bert))
				is.CATT, is.CAMeL = catt, bert
				for _, m := range missing {
					is.Missing = append(is.Missing, m.Index)
				}
			}
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
			if catt == "" || whole || restates(token, catt, missing) {
				continue
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
