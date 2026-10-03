package deck

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

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
	s = strings.Map(func(r rune) rune {
		if r == tashkeel.Tatweel || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
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

func (q query) fits(canonical string, loose bool) bool {
	skeleton := tashkeel.Skeleton(canonical)
	if loose {
		return tashkeel.Fold(skeleton) == q.loose && tashkeel.CompatibleCitation(tashkeel.Fold(canonical), tashkeel.Fold(q.word))
	}
	return skeleton == q.skeleton && tashkeel.CompatibleCitation(canonical, q.word)
}

func (q query) fitsTitle(e *lexicon.Entry, loose bool) bool {
	skeleton := tashkeel.Skeleton(e.Title)
	if loose {
		skeleton = tashkeel.Fold(skeleton)
	}
	want := q.skeleton
	if loose {
		want = q.loose
	}
	if e.Title == "" || skeleton != want {
		return false
	}
	if !strings.ContainsFunc(q.word, tashkeel.IsMark) {
		return true
	}
	for _, w := range strings.Fields(e.Canonical) {
		if q.fits(w, loose) || q.fits(withoutArticle(w), loose) {
			return true
		}
	}
	return false
}

func withoutArticle(word string) string {
	letters := tashkeel.Letters(word)
	if len(letters) > 3 && tashkeel.Skeleton(letters[0]+letters[1]) == "ال" {
		return strings.Join(letters[2:], "")
	}
	return word
}

type pass struct {
	loose bool
	title bool
}

var passes = []pass{{false, false}, {true, false}, {false, true}, {true, true}}

func (q query) matches(e *lexicon.Entry, p pass) bool {
	if p.title {
		return q.fitsTitle(e, p.loose)
	}
	return q.fits(e.Canonical, p.loose)
}

func find(word string, recs []*rank.Record) []Candidate {
	q := newQuery(word)
	if q.skeleton == "" {
		return nil
	}
	for _, p := range passes {
		var out []Candidate
		for _, rec := range recs {
			out = append(out, candidatesOf(q, rec, p)...)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func candidatesOf(q query, rec *rank.Record, p pass) []Candidate {
	var out []Candidate
	for _, e := range rec.Entries {
		if e.Pos == "name" || !e.MSA() || !q.matches(e, p) {
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
	return fmt.Sprintf("The learner asked for this entry in particular (%s, %s), the only Wiktionary entry listed for this card. Write the card for it: keep its headword, part of speech and forms, and do not switch to another entry of the same spelling.", e.Pos, e.Canonical)
}

var functionWords = []string{"adv", "conj", "det", "intj", "num", "particle", "phrase", "prep", "pron"}

func posClass(pos string) string {
	if slices.Contains(functionWords, pos) {
		return "function"
	}
	return pos
}

func CheckEntry(draft notes.Note, pos, arabic string) error {
	if posClass(pos) != posClass(draft.Pos) {
		return fmt.Errorf("the learner asked for the %s %s, but the card is for a %s; write the card for the %s", draft.Pos, draft.Arabic, pos, draft.Pos)
	}
	if !tashkeel.CompatibleCitation(arabic, draft.Arabic) {
		return fmt.Errorf("the learner asked for the headword %s, but the card's headword is %s; keep %s", draft.Arabic, arabic, draft.Arabic)
	}
	return nil
}

type Placement struct {
	Note     notes.Note
	Context  *rank.Record
	Feedback string
	Present  bool
	Resumed  bool
}

type Planner struct {
	notes    []notes.Note
	ids      map[string]bool
	reserved map[string]bool
	taken    map[int]bool
	last     int
	targets  map[string]bool
}

func NewPlanner(existing []notes.Note, records []rank.Record) *Planner {
	p := &Planner{
		notes:    slices.Clone(existing),
		ids:      map[string]bool{},
		reserved: map[string]bool{},
		taken:    map[int]bool{},
		last:     UnrankedBase,
		targets:  map[string]bool{},
	}
	for _, rec := range records {
		p.reserved[rec.ID] = true
	}
	for _, n := range existing {
		p.ids[n.ID] = true
		p.taken[n.Position] = true
		p.last = max(p.last, n.Position)
	}
	return p
}

func (p *Planner) Existing(c Candidate) (notes.Note, bool) {
	if c.claimsRank() {
		if i := slices.IndexFunc(p.notes, func(n notes.Note) bool { return n.Position == c.Record.Rank }); i >= 0 {
			return p.notes[i], true
		}
	}
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

func (p *Planner) Holding(word string) []notes.Note {
	q := newQuery(word)
	if q.skeleton == "" {
		return nil
	}
	for _, loose := range []bool{false, true} {
		var out []notes.Note
		for _, n := range p.notes {
			if n.Authored() && q.fits(n.Arabic, loose) {
				out = append(out, n)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
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
	free := func(id string) bool { return !p.ids[id] && !p.reserved[id] }
	id := e.Canonical
	if !free(id) {
		id = fmt.Sprintf("%s (%s)", e.Canonical, e.Pos)
	}
	for n := 2; !free(id); n++ {
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
