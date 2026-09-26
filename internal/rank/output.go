package rank

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
)

type Record struct {
	Rank    int              `json:"rank"`
	ID      string           `json:"id"`
	CEFR    string           `json:"cefr,omitempty"`
	Entries []*lexicon.Entry `json:"entries"`
}

func Records(rows []Row, maxSenses int) []Record {
	out := make([]Record, 0, len(rows))
	for _, r := range rows {
		rec := Record{Rank: r.Rank, ID: r.Lemma.ID, CEFR: r.CEFR}
		for _, e := range r.Lemma.Entries {
			if e.Pos == "name" {
				continue
			}
			c := *e
			if maxSenses > 0 && len(c.Senses) > maxSenses {
				c.Senses = c.Senses[:maxSenses]
			}
			rec.Entries = append(rec.Entries, &c)
		}
		out = append(out, rec)
	}
	return out
}

func WriteTSV(w io.Writer, rows []Row, corpora []string) error {
	bw := bufio.NewWriter(w)
	header := []string{"rank", "id", "pos", "score"}
	for _, c := range corpora {
		header = append(header, c+"_ppm")
	}
	header = append(header, "agree", "contested", "cefr", "essential", "gloss")
	bw.WriteString(strings.Join(header, "\t") + "\n")
	for _, r := range rows {
		cols := []string{
			strconv.Itoa(r.Rank),
			r.Lemma.ID,
			strings.Join(r.Lemma.Pos(), ","),
			fmt.Sprintf("%.2f", r.Score),
		}
		for _, c := range corpora {
			cols = append(cols, fmt.Sprintf("%.2f", r.PPM[c]))
		}
		essential := ""
		if r.Essential > 0 {
			essential = strconv.Itoa(r.Essential)
		}
		gloss := strings.NewReplacer("\t", " ", "\n", " ").Replace(r.Lemma.Gloss())
		if len([]rune(gloss)) > 80 {
			gloss = string([]rune(gloss)[:80]) + "…"
		}
		cols = append(cols,
			fmt.Sprintf("%.2f", r.Agree),
			fmt.Sprintf("%.2f", r.Contested),
			r.CEFR,
			essential,
			gloss,
		)
		bw.WriteString(strings.Join(cols, "\t") + "\n")
	}
	return bw.Flush()
}

func WriteDecisions(w io.Writer, ix *Index, decisions []Decision, limit int, outcomes map[string]bool) error {
	bw := bufio.NewWriter(w)
	bw.WriteString("surface\tppm\toutcome\tcamel\twiktionary\tcandidates\n")
	name := func(i int) string {
		if i < 0 {
			return ""
		}
		return ix.Lemmas[i].ID
	}
	n := 0
	for _, d := range decisions {
		if len(outcomes) > 0 && !outcomes[d.Outcome] {
			continue
		}
		if limit > 0 && n >= limit {
			break
		}
		n++
		var cands []string
		for _, c := range ix.Analyze(d.Surface) {
			cands = append(cands, ix.Lemmas[c].ID+"/"+ix.Lemmas[c].Main().Pos)
		}
		fmt.Fprintf(bw, "%s\t%.1f\t%s\t%s\t%s\t%s\n", d.Surface, d.PPM, d.Outcome, name(d.A), name(d.B), strings.Join(cands, " "))
	}
	return bw.Flush()
}
