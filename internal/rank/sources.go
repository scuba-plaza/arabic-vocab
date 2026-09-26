package rank

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type Count struct {
	Word  string
	Count float64
}

type Corpus struct {
	Name  string
	Types []Count
	Total float64
}

func ReadCounts(name string, r io.Reader, limit int) (*Corpus, error) {
	c := &Corpus{Name: name}
	index := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.FieldsFunc(sc.Text(), func(r rune) bool { return r == '\t' || r == ' ' })
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		c.Total += n
		w := tashkeel.Skeleton(fields[0])
		if !isArabicWord(w) {
			continue
		}
		if i, ok := index[w]; ok {
			c.Types[i].Count += n
			continue
		}
		if limit > 0 && len(c.Types) >= limit {
			continue
		}
		index[w] = len(c.Types)
		c.Types = append(c.Types, Count{Word: w, Count: n})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s frequencies: %w", name, err)
	}
	if len(c.Types) == 0 {
		return nil, fmt.Errorf("%s frequency list has no Arabic words", name)
	}
	return c, nil
}

func isArabicWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !arabic.IsArabicLetter(r) {
			return false
		}
	}
	return true
}

type CamelAnalysis struct {
	Lex   string
	Pos   string
	Gloss string
}

func ReadCamel(r io.Reader) (map[string]CamelAnalysis, error) {
	out := map[string]CamelAnalysis{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		key := tashkeel.Skeleton(fields[0])
		if _, ok := out[key]; ok {
			continue
		}
		a := CamelAnalysis{Lex: fields[1], Pos: fields[2]}
		if len(fields) > 3 {
			a.Gloss = fields[3]
		}
		out[key] = a
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading CAMeL analyses: %w", err)
	}
	return out, nil
}

type KellyWord struct {
	Word string
	CEFR string
	Pos  string
}

type kellyFile struct {
	FullList []struct {
		Word string `json:"word"`
		CEFR string `json:"cefr"`
		Pos  string `json:"pos"`
	} `json:"full_list"`
}

func ReadKelly(r io.Reader) (map[string]KellyWord, error) {
	var f kellyFile
	if err := json.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("reading Kelly list: %w", err)
	}
	out := map[string]KellyWord{}
	for _, w := range f.FullList {
		if w.CEFR == "" {
			continue
		}
		k := tashkeel.Skeleton(w.Word)
		if prev, ok := out[k]; ok && prev.CEFR <= w.CEFR {
			continue
		}
		out[k] = KellyWord{Word: w.Word, CEFR: w.CEFR, Pos: w.Pos}
	}
	return out, nil
}

type Essential struct {
	Arabic  string
	MaxRank int
	Pos     string
	Gloss   string
}

func ReadEssentials(r io.Reader) ([]Essential, error) {
	var out []Essential
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) < 2 {
			return nil, fmt.Errorf("essentials line %d: want arabic<TAB>max_rank[<TAB>pos<TAB>gloss]", line)
		}
		n, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("essentials line %d: bad max rank %q", line, fields[1])
		}
		e := Essential{Arabic: strings.TrimSpace(fields[0]), MaxRank: n}
		if len(fields) > 2 {
			e.Pos = strings.TrimSpace(fields[2])
		}
		if len(fields) > 3 {
			e.Gloss = strings.TrimSpace(fields[3])
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

type OverrideKind string

const (
	OverrideLemma OverrideKind = "lemma"
	OverrideForm  OverrideKind = "form"
)

type Override struct {
	Kind OverrideKind
	From string
	To   string
}

func ReadOverrides(r io.Reader) ([]Override, error) {
	var out []Override
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) < 3 {
			return nil, fmt.Errorf("overrides line %d: want lemma<TAB>from<TAB>to or form<TAB>surface<TAB>to", line)
		}
		kind := OverrideKind(strings.TrimSpace(fields[0]))
		if kind != OverrideLemma && kind != OverrideForm {
			return nil, fmt.Errorf("overrides line %d: unknown kind %q", line, fields[0])
		}
		o := Override{Kind: kind, From: strings.TrimSpace(fields[1]), To: strings.TrimSpace(fields[2])}
		if kind == OverrideForm && o.To == "-" {
			return nil, fmt.Errorf("overrides line %d: a form override needs a target lemma", line)
		}
		out = append(out, o)
	}
	return out, sc.Err()
}
