package rank

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type candidate struct {
	lemma int
	class lexicon.Class
}

type Index struct {
	Lemmas []*lexicon.Lemma
	byKey  map[string]int
	forms  map[string][]candidate
	loose  map[string][]candidate
}

func fold(s string) string {
	return strings.NewReplacer("أ", "ا", "إ", "ا", "آ", "ا").Replace(s)
}

func NewIndex(lemmas []*lexicon.Lemma) *Index {
	ix := &Index{
		Lemmas: lemmas,
		byKey:  map[string]int{},
		forms:  map[string][]candidate{},
		loose:  map[string][]candidate{},
	}
	type seenKey struct {
		skeleton string
		cand     candidate
	}
	for i, l := range lemmas {
		ix.byKey[l.Key] = i
		seen := map[seenKey]bool{}
		for _, e := range l.Entries {
			for _, f := range e.Forms {
				c := candidate{lemma: i, class: f.Class}
				k := seenKey{skeleton: f.Skeleton, cand: c}
				if seen[k] {
					continue
				}
				seen[k] = true
				ix.forms[f.Skeleton] = append(ix.forms[f.Skeleton], c)
				if k := fold(f.Skeleton); k != f.Skeleton {
					ix.loose[k] = append(ix.loose[k], c)
				}
			}
		}
	}
	return ix
}

func (ix *Index) Lookup(word string) (int, bool) {
	i, ok := ix.byKey[tashkeel.LexKey(word)]
	return i, ok
}

func (ix *Index) lookup(stem string) []candidate {
	if c := ix.forms[stem]; len(c) > 0 {
		return c
	}
	var out []candidate
	out = append(out, ix.loose[fold(stem)]...)
	out = append(out, ix.forms[fold(stem)]...)
	return out
}

var (
	conjunctions = []string{"", "و", "ف"}
	particles    = []string{"", "ب", "ك", "ل", "س"}
	enclitics    = []string{"", "ه", "ها", "هم", "هما", "هن", "ك", "كم", "كما", "كن", "ي", "ني", "نا"}
)

func stemVariants(stem string, enclitic bool) []string {
	out := []string{stem}
	if !enclitic {
		return out
	}
	trimmed, last := trimLast(stem)
	switch last {
	case 'ت':
		out = append(out, trimmed+"ة")
	case 'ي':
		out = append(out, trimmed+"ى")
	case 'و':
		out = append(out, stem+"ا")
	case 'ا':
		out = append(out, trimmed+"ى")
	case 'ئ', 'ؤ':
		out = append(out, trimmed+"ء")
	}
	return out
}

func trimLast(s string) (string, rune) {
	r := []rune(s)
	if len(r) == 0 {
		return s, 0
	}
	return string(r[:len(r)-1]), r[len(r)-1]
}

type Match struct {
	Lemma   int
	Quality float64
}

const (
	cliticPenalty  = 0.25
	variantPenalty = 0.8
	loosePenalty   = 0.3
)

func (ix *Index) Analyze(word string) []int {
	var out []int
	for _, m := range ix.Matches(word) {
		out = append(out, m.Lemma)
	}
	return out
}

func (ix *Index) Matches(word string) []Match {
	best := map[int]float64{}
	for _, c := range conjunctions {
		w1, ok := strings.CutPrefix(word, c)
		if !ok {
			continue
		}
		for _, p := range particles {
			w2, ok := strings.CutPrefix(w1, p)
			if !ok || w2 == "" {
				continue
			}
			type base struct {
				text    string
				quality float64
			}
			bases := []base{{w2, 1}}
			if p == "ل" && strings.HasPrefix(w2, "ل") {
				bases = append(bases, base{"ا" + w2, 1})
			}
			for _, b := range bases {
				for _, enc := range enclitics {
					stem, ok := strings.CutSuffix(b.text, enc)
					if !ok || stem == "" {
						continue
					}
					q := b.quality
					for _, affix := range []string{c, p, enc} {
						if affix != "" {
							q *= cliticPenalty
						}
					}
					for vi, sv := range stemVariants(stem, enc != "") {
						vq := q
						if vi > 0 {
							vq *= variantPenalty
						}
						strict := ix.forms[sv]
						for _, cand := range ix.lookup(sv) {
							if !ix.allowed(cand, c != "" || p != "" || enc != "", p, enc != "", sv) {
								continue
							}
							mq := vq
							if len(strict) == 0 {
								mq *= loosePenalty
							}
							if mq > best[cand.lemma] {
								best[cand.lemma] = mq
							}
						}
					}
				}
			}
		}
	}
	out := make([]Match, 0, len(best))
	for l, q := range best {
		out = append(out, Match{Lemma: l, Quality: q})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Lemma < out[b].Lemma })
	return out
}

func (ix *Index) allowed(c candidate, affixed bool, particle string, enclitic bool, stem string) bool {
	l := ix.Lemmas[c.lemma]
	verb := l.Main().Pos == "verb" || c.class == lexicon.Perfect || c.class == lexicon.Imperfect || c.class == lexicon.Imperative
	if enclitic && c.class == lexicon.Definite {
		return false
	}
	switch particle {
	case "ب", "ك":
		if verb {
			return false
		}
	case "س":
		if c.class != lexicon.Imperfect {
			return false
		}
	case "ل":
		if verb && c.class != lexicon.Imperfect {
			return false
		}
	}
	if affixed && len([]rune(stem)) < 2 && !l.Has("prep") {
		return false
	}
	return true
}

type Options struct {
	Floor      float64
	Iterations int
	Limit      int
}

type Row struct {
	Rank      int
	Lemma     *lexicon.Lemma
	Score     float64
	PPM       map[string]float64
	Agree     float64
	Contested float64
	CEFR      string
	Essential int
}

type Decision struct {
	Surface string
	PPM     float64
	A       int
	B       int
	Outcome string
}

type Result struct {
	Rows      []Row
	Decisions []Decision
}

type attribution struct {
	perCorpus map[string]float64
	agree     float64
	contested float64
	total     float64
}

var particleLike = map[string]bool{"prep": true, "conj": true, "particle": true, "pron": true, "det": true, "intj": true}

func (ix *Index) prior(i int, kelly map[string]KellyWord) float64 {
	l := ix.Lemmas[i]
	main := l.Main()
	p := 1.0 + math.Log1p(float64(len(main.MSASenses())))
	if particleLike[main.Pos] {
		p *= 5
	}
	if !l.MSA() {
		p *= 0.05
	}
	if l.Name() {
		p *= 0.3
	}
	if _, ok := kelly[tashkeel.Skeleton(main.Canonical)]; ok {
		p *= 5
	}
	return p
}

func camelLemma(ix *Index, a CamelAnalysis, cands []int, weights map[int]float64) (int, bool) {
	if a.Lex == "" || a.Pos == "noun_prop" || a.Pos == "punc" || a.Pos == "digit" || a.Pos == "latin" || a.Pos == "foreign" {
		return 0, false
	}
	if i, ok := ix.Lookup(a.Lex); ok {
		return i, true
	}
	skel := tashkeel.Skeleton(a.Lex)
	best, bestW := -1, -1.0
	for _, c := range cands {
		if tashkeel.Skeleton(ix.Lemmas[c].Main().Canonical) != skel {
			continue
		}
		if w := weights[c]; w > bestW {
			best, bestW = c, w
		}
	}
	return best, best >= 0
}

func Rank(ix *Index, corpora []*Corpus, camel map[string]CamelAnalysis, kelly map[string]KellyWord, essentials []Essential, overrides []Override, opts Options) (*Result, error) {
	if opts.Floor == 0 {
		opts.Floor = 0.5
	}
	if opts.Iterations == 0 {
		opts.Iterations = 8
	}

	type typeInfo struct {
		ppm     map[string]float64
		total   float64
		cands   []int
		quality map[int]float64
	}
	types := map[string]*typeInfo{}
	var order []string
	for _, c := range corpora {
		for _, t := range c.Types {
			info, ok := types[t.Word]
			if !ok {
				info = &typeInfo{ppm: map[string]float64{}}
				types[t.Word] = info
				order = append(order, t.Word)
			}
			v := t.Count / c.Total * 1e6
			info.ppm[c.Name] += v
			info.total += v
		}
	}
	for _, w := range order {
		info := types[w]
		info.quality = map[int]float64{}
		for _, m := range ix.Matches(w) {
			info.cands = append(info.cands, m.Lemma)
			info.quality[m.Lemma] = m.Quality
		}
	}

	priors := map[int]float64{}
	totals := map[int]float64{}
	for _, w := range order {
		for _, c := range types[w].cands {
			if _, ok := priors[c]; !ok {
				priors[c] = ix.prior(c, kelly)
				totals[c] = 1
			}
		}
	}
	weightsFor := func(info *typeInfo) map[int]float64 {
		out := make(map[int]float64, len(info.cands))
		z := 0.0
		for _, c := range info.cands {
			out[c] = priors[c] * (totals[c] + 1) * info.quality[c]
			z += out[c]
		}
		for c := range out {
			out[c] /= z
		}
		return out
	}
	for it := 0; it < opts.Iterations; it++ {
		next := map[int]float64{}
		for _, w := range order {
			info := types[w]
			for c, x := range weightsFor(info) {
				next[c] += info.total * x
			}
		}
		totals = next
	}

	skeletons := make([]string, len(ix.Lemmas))
	for i, l := range ix.Lemmas {
		skeletons[i] = fold(tashkeel.Skeleton(l.Main().Canonical))
	}

	attr := map[int]*attribution{}
	credit := func(i int, info *typeInfo, share float64, agree, contested bool) {
		a, ok := attr[i]
		if !ok {
			a = &attribution{perCorpus: map[string]float64{}}
			attr[i] = a
		}
		for name, v := range info.ppm {
			a.perCorpus[name] += v * share
		}
		mass := info.total * share
		a.total += mass
		if agree {
			a.agree += mass
		}
		if contested {
			a.contested += mass
		}
	}
	forced := map[string]int{}
	for _, o := range overrides {
		if o.Kind != OverrideForm {
			continue
		}
		to, ok := ix.Lookup(o.To)
		if !ok {
			return nil, fmt.Errorf("override: %s is not a Wiktionary lemma", o.To)
		}
		forced[tashkeel.Skeleton(o.From)] = to
	}
	var decisions []Decision
	for _, w := range order {
		info := types[w]
		if to, ok := forced[w]; ok {
			credit(to, info, 1, true, false)
			continue
		}
		weights := weightsFor(info)
		b, bExact := -1, false
		bW := -1.0
		for _, c := range info.cands {
			exact := skeletons[c] == fold(w)
			if (exact && !bExact) || (exact == bExact && weights[c] > bW) {
				b, bW, bExact = c, weights[c], exact
			}
		}
		an, hasCamel := camel[w]
		a, aOK := -1, false
		if hasCamel {
			a, aOK = camelLemma(ix, an, info.cands, weights)
		}
		aExact := aOK && skeletons[a] == fold(w)
		if !aOK {
			a = -1
		}
		d := Decision{Surface: w, PPM: info.total, A: a, B: b}
		switch {
		case aOK && b >= 0 && a == b:
			d.Outcome = "agree"
			credit(a, info, 1, true, false)
		case aOK && b >= 0 && aExact && !bExact:
			d.Outcome = "camel-exact"
			credit(a, info, 1, false, false)
		case aOK && b >= 0 && bExact && !aExact:
			d.Outcome = "wiktionary-exact"
			credit(b, info, 1, false, false)
		case aOK && b >= 0:
			d.Outcome = "contested"
			credit(a, info, 0.5, false, true)
			credit(b, info, 0.5, false, true)
		case aOK:
			d.Outcome = "camel-only"
			credit(a, info, 1, false, false)
		case hasCamel && an.Pos == "noun_prop" && b >= 0:
			d.Outcome = "proper-noun"
			credit(b, info, 0.5, false, true)
		case b >= 0:
			d.Outcome = "wiktionary-only"
			for c, x := range weights {
				credit(c, info, x, false, false)
			}
		default:
			d.Outcome = "unknown"
		}
		decisions = append(decisions, d)
	}

	for _, o := range overrides {
		if o.Kind != OverrideLemma {
			continue
		}
		from, ok := ix.Lookup(o.From)
		if !ok {
			return nil, fmt.Errorf("override: %s is not a Wiktionary lemma", o.From)
		}
		a := attr[from]
		delete(attr, from)
		if o.To == "-" || a == nil {
			attr[from] = &attribution{perCorpus: map[string]float64{}, total: -1}
			continue
		}
		to, ok := ix.Lookup(o.To)
		if !ok {
			return nil, fmt.Errorf("override: %s is not a Wiktionary lemma", o.To)
		}
		b, ok := attr[to]
		if !ok {
			b = &attribution{perCorpus: map[string]float64{}}
			attr[to] = b
		}
		for k, v := range a.perCorpus {
			b.perCorpus[k] += v
		}
		b.total += a.total
		b.agree += a.agree
		b.contested += a.contested
	}

	var rows []Row
	for i, a := range attr {
		l := ix.Lemmas[i]
		if a.total < 0 || l.Name() || !l.MSA() {
			continue
		}
		score := 1.0
		for _, c := range corpora {
			score *= math.Max(a.perCorpus[c.Name], opts.Floor)
		}
		score = math.Pow(score, 1/float64(len(corpora)))
		row := Row{Lemma: l, Score: score, PPM: a.perCorpus}
		if a.total > 0 {
			row.Agree = a.agree / a.total
			row.Contested = a.contested / a.total
		}
		if k, ok := kelly[tashkeel.Skeleton(l.Main().Canonical)]; ok {
			row.CEFR = k.CEFR
			if k.CEFR == "A1" || k.CEFR == "A2" {
				row.Score *= 1.2
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].Score != rows[b].Score {
			return rows[a].Score > rows[b].Score
		}
		return rows[a].Lemma.ID < rows[b].Lemma.ID
	})

	rows, err := placeEssentials(ix, rows, essentials)
	if err != nil {
		return nil, err
	}
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	for i := range rows {
		rows[i].Rank = i + 1
		preferCamelPos(rows[i].Lemma, camel[tashkeel.Skeleton(rows[i].Lemma.Main().Canonical)])
	}
	sort.SliceStable(decisions, func(a, b int) bool { return decisions[a].PPM > decisions[b].PPM })
	return &Result{Rows: rows, Decisions: decisions}, nil
}

var camelPos = map[string][]string{
	"noun": {"noun", "num"}, "noun_num": {"num", "noun"}, "noun_quant": {"noun"},
	"adj": {"adj"}, "adj_comp": {"adj"}, "adj_num": {"adj", "num"},
	"verb": {"verb"}, "verb_pseudo": {"particle", "conj"},
	"adv": {"adv"}, "adv_interrog": {"adv", "pron"}, "adv_rel": {"adv", "conj"},
	"pron": {"pron"}, "pron_dem": {"pron", "det"}, "pron_exclam": {"pron"}, "pron_interrog": {"pron", "adv"}, "pron_rel": {"pron"},
	"prep": {"prep"}, "conj": {"conj"}, "conj_sub": {"conj"},
	"part": {"particle"}, "part_det": {"article", "particle"}, "part_focus": {"particle"}, "part_fut": {"particle"},
	"part_interrog": {"particle"}, "part_neg": {"particle"}, "part_restrict": {"particle"}, "part_verb": {"particle"},
	"part_voc": {"particle"}, "interj": {"intj", "adv"},
}

func preferCamelPos(l *lexicon.Lemma, a CamelAnalysis) {
	if len(l.Entries) < 2 || tashkeel.LexKey(a.Lex) != l.Key {
		return
	}
	for _, pos := range camelPos[a.Pos] {
		i := slices.IndexFunc(l.Entries, func(e *lexicon.Entry) bool { return e.Pos == pos && e.MSA() })
		if i > 0 {
			e := l.Entries[i]
			l.Entries = slices.Insert(slices.Delete(l.Entries, i, i+1), 0, e)
			l.ID = e.Canonical
		}
		if i >= 0 {
			return
		}
	}
}

func placeEssentials(ix *Index, rows []Row, essentials []Essential) ([]Row, error) {
	type pending struct {
		row     Row
		maxRank int
		order   int
	}
	var moves []pending
	for n, e := range essentials {
		var lemma *lexicon.Lemma
		if i, ok := ix.Lookup(e.Arabic); ok {
			lemma = ix.Lemmas[i]
		} else {
			if e.Pos == "" || e.Gloss == "" {
				return nil, fmt.Errorf("essential %s is not in Wiktionary; give it a pos and gloss", e.Arabic)
			}
			lemma = &lexicon.Lemma{ID: e.Arabic, Key: tashkeel.LexKey(e.Arabic), Entries: []*lexicon.Entry{{
				Title: tashkeel.Skeleton(e.Arabic), Pos: e.Pos, Canonical: e.Arabic,
				Senses: []lexicon.Sense{{Gloss: e.Gloss, MSA: true}},
			}}}
		}
		pos := slices.IndexFunc(rows, func(r Row) bool { return r.Lemma.Key == lemma.Key })
		var row Row
		if pos >= 0 {
			if pos < e.MaxRank {
				rows[pos].Essential = e.MaxRank
				continue
			}
			row = rows[pos]
			rows = slices.Delete(rows, pos, pos+1)
		} else {
			row = Row{Lemma: lemma, PPM: map[string]float64{}}
		}
		row.Essential = e.MaxRank
		moves = append(moves, pending{row: row, maxRank: e.MaxRank, order: n})
	}
	sort.SliceStable(moves, func(a, b int) bool {
		if moves[a].maxRank != moves[b].maxRank {
			return moves[a].maxRank < moves[b].maxRank
		}
		return moves[a].order < moves[b].order
	})
	for start := 0; start < len(moves); {
		end := start
		for end < len(moves) && moves[end].maxRank == moves[start].maxRank {
			end++
		}
		group := moves[start:end]
		for j, m := range group {
			at := min(max(m.maxRank-len(group)+j, 0), len(rows))
			rows = slices.Insert(rows, at, m.row)
		}
		start = end
	}
	return rows, nil
}
