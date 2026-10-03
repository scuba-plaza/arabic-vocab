package deck

import (
	"fmt"
	"slices"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

const (
	UnrankedBase = 100000
	maxSenses    = 12
)

type Candidate struct {
	Entries []*lexicon.Entry
	Record  *rank.Record
	Primary bool
}

func (c Candidate) Main() *lexicon.Entry {
	return c.Entries[0]
}

func (c Candidate) Senses() []lexicon.Sense {
	var out []lexicon.Sense
	for _, e := range c.Entries {
		out = append(out, e.MSASenses()...)
	}
	return out
}

func (c Candidate) claimsRank() bool {
	return c.Primary && c.Record != nil && c.Record.Rank > 0
}

func NormalizeWord(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, string(tashkeel.Tatweel), "")), " ")
}

func Find(word string, records []rank.Record, entries []*lexicon.Entry) []Candidate {
	out := FindRanked(word, records)
	if entries == nil {
		return out
	}
	covered := map[string]bool{}
	for _, c := range out {
		covered[tashkeel.LexKey(c.Record.ID)] = true
	}
	for _, c := range FindEntries(word, entries) {
		if !covered[tashkeel.LexKey(c.Record.ID)] {
			out = append(out, c)
		}
	}
	return out
}

func FindRanked(word string, records []rank.Record) []Candidate {
	recs := make([]*rank.Record, len(records))
	for i := range records {
		recs[i] = &records[i]
	}
	slices.SortStableFunc(recs, func(a, b *rank.Record) int { return a.Rank - b.Rank })
	return find(word, recs)
}

func FindEntries(word string, entries []*lexicon.Entry) []Candidate {
	lemmas := lexicon.Group(entries)
	rows := make([]rank.Row, len(lemmas))
	for i, l := range lemmas {
		rows[i] = rank.Row{Lemma: l}
	}
	records := rank.Records(rows, maxSenses)
	recs := make([]*rank.Record, 0, len(records))
	for i := range records {
		if len(records[i].Entries) > 0 {
			recs = append(recs, &records[i])
		}
	}
	slices.SortStableFunc(recs, func(a, b *rank.Record) int {
		return len(b.Entries[0].MSASenses()) - len(a.Entries[0].MSASenses())
	})
	return find(word, recs)
}

type query struct {
	word     string
	skeleton string
	loose    string
}

func newQuery(word string) query {
	word = NormalizeWord(word)
	skeleton := tashkeel.Skeleton(word)
	return query{word: word, skeleton: skeleton, loose: tashkeel.Fold(skeleton)}
}

func (q query) matches(e *lexicon.Entry, loose bool) bool {
	skeleton := tashkeel.Skeleton(e.Canonical)
	if loose {
		return tashkeel.Fold(skeleton) == q.loose && tashkeel.CompatibleCitation(tashkeel.Fold(e.Canonical), tashkeel.Fold(q.word))
	}
	return skeleton == q.skeleton && tashkeel.CompatibleCitation(e.Canonical, q.word)
}

func find(word string, recs []*rank.Record) []Candidate {
	q := newQuery(word)
	if q.skeleton == "" {
		return nil
	}
	for _, loose := range []bool{false, true} {
		var out []Candidate
		for _, rec := range recs {
			out = append(out, candidatesOf(q, rec, loose)...)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func candidatesOf(q query, rec *rank.Record, loose bool) []Candidate {
	var out []Candidate
	for _, e := range rec.Entries {
		if e.Pos == "name" || !e.MSA() || !q.matches(e, loose) {
			continue
		}
		if i := slices.IndexFunc(out, func(c Candidate) bool { return c.Main().Pos == e.Pos }); i >= 0 {
			out[i].Entries = append(out[i].Entries, e)
			continue
		}
		out = append(out, Candidate{Entries: []*lexicon.Entry{e}, Record: rec, Primary: e == rec.Entries[0]})
	}
	return out
}

func SenseFeedback(c Candidate) string {
	e := c.Main()
	return fmt.Sprintf("The learner asked for this entry in particular (%s, %s). Write the card for it: keep its headword, part of speech and forms, and do not switch to another entry of the same spelling.", e.Pos, e.Canonical)
}

type Placement struct {
	Note     notes.Note
	Context  *rank.Record
	Feedback string
	Present  bool
	Resumed  bool
}

type Planner struct {
	notes   []notes.Note
	ids     map[string]bool
	taken   map[int]bool
	last    int
	targets map[string]bool
}

func NewPlanner(existing []notes.Note) *Planner {
	p := &Planner{
		notes:   slices.Clone(existing),
		ids:     map[string]bool{},
		taken:   map[int]bool{},
		last:    UnrankedBase,
		targets: map[string]bool{},
	}
	for _, n := range existing {
		p.ids[n.ID] = true
		p.taken[n.Position] = true
		p.last = max(p.last, n.Position)
	}
	return p
}

func (p *Planner) Existing(c Candidate) (notes.Note, bool) {
	e := c.Main()
	key := tashkeel.LexKey(e.Canonical)
	i := slices.IndexFunc(p.notes, func(n notes.Note) bool {
		return n.Pos == e.Pos && tashkeel.LexKey(n.Arabic) == key
	})
	if i < 0 {
		return notes.Note{}, false
	}
	return p.notes[i], true
}

func (p *Planner) Add(c Candidate) Placement {
	if n, ok := p.Existing(c); ok {
		if n.Authored() {
			return Placement{Note: n, Present: true}
		}
		p.targets[n.ID] = true
		return Placement{Note: n, Context: &rank.Record{Rank: n.Position, ID: n.ID, CEFR: n.CEFR, Entries: c.Entries}, Feedback: SenseFeedback(c), Resumed: true}
	}
	id, position, cefr := p.place(c)
	rec := rank.Record{Rank: position, ID: id, CEFR: cefr, Entries: c.Entries}
	n := DefaultNote(rec)
	p.notes = append(p.notes, n)
	p.ids[id] = true
	p.taken[position] = true
	p.targets[id] = true
	return Placement{Note: n, Context: &rec, Feedback: SenseFeedback(c)}
}

func (p *Planner) place(c Candidate) (string, int, string) {
	if c.claimsRank() && !p.ids[c.Record.ID] && !p.taken[c.Record.Rank] {
		return c.Record.ID, c.Record.Rank, c.Record.CEFR
	}
	e := c.Main()
	id := e.Canonical
	if p.ids[id] {
		id = fmt.Sprintf("%s (%s)", e.Canonical, e.Pos)
	}
	for n := 2; p.ids[id]; n++ {
		id = fmt.Sprintf("%s (%s %d)", e.Canonical, e.Pos, n)
	}
	p.last++
	return id, p.last, ""
}

func (p *Planner) Notes() ([]notes.Note, []int) {
	out := slices.Clone(p.notes)
	notes.Sort(out)
	var targets []int
	for i, n := range out {
		if p.targets[n.ID] {
			targets = append(targets, i)
		}
	}
	return out, targets
}
