package rank

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
)

type form struct {
	Form   string   `json:"form"`
	Tags   []string `json:"tags"`
	Source string   `json:"source,omitempty"`
}

type sense struct {
	Glosses []string `json:"glosses"`
	Tags    []string `json:"tags,omitempty"`
}

type entry struct {
	Word   string  `json:"word"`
	Pos    string  `json:"pos"`
	Forms  []form  `json:"forms"`
	Senses []sense `json:"senses"`
}

func dump(t *testing.T, entries ...entry) string {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func fixture(t *testing.T) *Index {
	t.Helper()
	jsonl := dump(t,
		entry{Word: "كتاب", Pos: "noun", Forms: []form{
			{Form: "كِتَاب", Tags: []string{"canonical", "masculine"}},
			{Form: "كُتُب", Tags: []string{"plural"}},
			{Form: "الْكِتَاب", Tags: []string{"definite", "singular"}, Source: "declension"},
			{Form: "كِتَابًا", Tags: []string{"indefinite", "accusative"}, Source: "declension"},
			{Form: "الْكُتُب", Tags: []string{"definite", "plural"}, Source: "declension"},
		}, Senses: []sense{{Glosses: []string{"verbal noun of كَتَبَ"}, Tags: []string{"form-of"}}, {Glosses: []string{"book"}}}},
		entry{Word: "كتب", Pos: "verb", Forms: []form{
			{Form: "كَتَبَ", Tags: []string{"canonical", "form-i"}},
			{Form: "يَكْتُبُ", Tags: []string{"non-past"}},
			{Form: "كِتَابَة", Tags: []string{"noun-from-verb"}},
			{Form: "كَتَبْتُ", Tags: []string{"active", "first-person", "past"}, Source: "conjugation"},
			{Form: "يَكْتُبُ", Tags: []string{"active", "indicative", "third-person"}, Source: "conjugation"},
			{Form: "كَاتِب", Tags: []string{"active", "participle"}, Source: "conjugation"},
		}, Senses: []sense{{Glosses: []string{"to write"}}}},
		entry{Word: "ليس", Pos: "verb", Forms: []form{{Form: "لَيْسَ", Tags: []string{"canonical"}}}, Senses: []sense{{Glosses: []string{"not to be"}}}},
		entry{Word: "ليس", Pos: "verb", Forms: []form{{Form: "لَيِسَ", Tags: []string{"canonical", "form-i"}}}, Senses: []sense{{Glosses: []string{"to be valiant"}}}},
		entry{Word: "في", Pos: "prep", Forms: []form{
			{Form: "فِي", Tags: []string{"canonical"}},
			{Form: "فِيهِ", Tags: []string{"third-person"}, Source: "inflection"},
		}, Senses: []sense{{Glosses: []string{"in"}}}},
		entry{Word: "محمد", Pos: "name", Forms: []form{{Form: "مُحَمَّد", Tags: []string{"canonical"}}}, Senses: []sense{{Glosses: []string{"Muhammad"}}}},
		entry{Word: "أبث", Pos: "verb", Forms: []form{{Form: "أَبَثَ", Tags: []string{"canonical"}}}, Senses: []sense{{Glosses: []string{"to slander"}, Tags: []string{"obsolete"}}}},
		entry{Word: "و", Pos: "conj", Forms: []form{{Form: "وَ", Tags: []string{"canonical"}}}, Senses: []sense{{Glosses: []string{"and"}}}},
		entry{Word: "و", Pos: "character", Forms: []form{{Form: "و", Tags: []string{"canonical"}}}, Senses: []sense{{Glosses: []string{"letter waw"}}}},
	)
	entries, err := lexicon.Read(strings.NewReader(jsonl))
	if err != nil {
		t.Fatal(err)
	}
	return NewIndex(lexicon.Group(entries))
}

func ids(ix *Index, lemmas []int) []string {
	var out []string
	for _, i := range lemmas {
		out = append(out, ix.Lemmas[i].ID)
	}
	slices.Sort(out)
	return out
}

func TestLexiconDropsFormOfSensesAndCharacters(t *testing.T) {
	ix := fixture(t)
	i, ok := ix.Lookup("كِتَاب")
	if !ok {
		t.Fatal("كِتَاب should be a lemma")
	}
	main := ix.Lemmas[i].Main()
	if len(main.Senses) != 1 || main.Senses[0].Gloss != "book" {
		t.Errorf("senses = %+v, want only book", main.Senses)
	}
	if !slices.Equal(main.Plurals, []string{"كُتُب"}) || main.Gender != "m" {
		t.Errorf("plurals %v gender %q", main.Plurals, main.Gender)
	}
	v, _ := ix.Lookup("كَتَبَ")
	verb := ix.Lemmas[v].Main()
	if verb.NonPast != "يَكْتُبُ" || verb.VerbForm != "I" || !slices.Equal(verb.VerbalNouns, []string{"كِتَابَة"}) {
		t.Errorf("verb fields %+v", verb)
	}
	w, _ := ix.Lookup("وَ")
	if got := ix.Lemmas[w].Pos(); !slices.Equal(got, []string{"conj"}) {
		t.Errorf("وَ should only keep its conjunction entry, got %v", got)
	}
}

func TestAnalyzeStripsClitics(t *testing.T) {
	ix := fixture(t)
	cases := map[string][]string{
		"الكتاب":  {"كِتَاب"},
		"بالكتاب": {"كِتَاب"},
		"للكتاب":  {"كِتَاب"},
		"وكتبها":  {"كَتَبَ", "كِتَاب"},
		"سيكتب":   {"كَتَبَ"},
		"فيه":     {"فِي"},
		"ليس":     {"لَيْسَ", "لَيِسَ"},
	}
	for word, want := range cases {
		got := ids(ix, ix.Analyze(word))
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("Analyze(%s) = %v, want %v", word, got, want)
		}
	}
	if got := ix.Analyze("بكتبت"); len(got) != 0 {
		t.Errorf("a preposition cannot attach to a verb, got %v", ids(ix, got))
	}
}

func corpora() []*Corpus {
	subs := &Corpus{Name: "subs", Total: 1000, Types: []Count{{"في", 500}, {"ليس", 100}, {"الكتاب", 50}, {"كتب", 30}, {"محمد", 40}, {"أبث", 5}}}
	msa := &Corpus{Name: "msa", Total: 2000, Types: []Count{{"في", 900}, {"ليس", 60}, {"الكتاب", 150}, {"كتب", 90}, {"محمد", 200}, {"أبث", 1}}}
	return []*Corpus{subs, msa}
}

func rankOf(rows []Row, id string) int {
	for _, r := range rows {
		if r.Lemma.ID == id {
			return r.Rank
		}
	}
	return 0
}

func TestRankOrdersByBlendedFrequencyAndFilters(t *testing.T) {
	ix := fixture(t)
	camel := map[string]CamelAnalysis{
		"ليس":    {Lex: "لَيْس", Pos: "verb"},
		"الكتاب": {Lex: "كِتاب", Pos: "noun"},
		"محمد":   {Lex: "مُحَمَّد", Pos: "noun_prop"},
	}
	res, err := Rank(ix, corpora(), camel, nil, nil, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.Rows
	if rankOf(rows, "فِي") != 1 {
		t.Errorf("فِي should rank first, rows: %v", rowIDs(rows))
	}
	if r, v := rankOf(rows, "لَيْسَ"), rankOf(rows, "لَيِسَ"); r == 0 || (v != 0 && v < r) {
		t.Errorf("CAMeL evidence should put لَيْسَ (%d) above لَيِسَ (%d)", r, v)
	}
	for _, gone := range []string{"مُحَمَّد", "أَبَثَ"} {
		if rankOf(rows, gone) != 0 {
			t.Errorf("%s should be filtered out", gone)
		}
	}
	for _, r := range rows {
		if r.Lemma.ID == "كِتَاب" && r.Agree == 0 {
			t.Errorf("كِتَاب should have agreeing evidence from both methods")
		}
	}
}

func TestOverridesAndEssentials(t *testing.T) {
	ix := fixture(t)
	res, err := Rank(ix, corpora(), nil, nil,
		[]Essential{{Arabic: "وَ", MaxRank: 1}, {Arabic: "مَرْحَبًا", MaxRank: 2, Pos: "intj", Gloss: "hello"}},
		[]Override{{Kind: OverrideLemma, From: "لَيِسَ", To: "لَيْسَ"}},
		Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.Rows
	if rankOf(rows, "وَ") != 1 {
		t.Errorf("وَ should be pinned to rank 1, rows: %v", rowIDs(rows))
	}
	if rankOf(rows, "مَرْحَبًا") != 2 {
		t.Errorf("an essential missing from Wiktionary should still be placed, rows: %v", rowIDs(rows))
	}
	if rankOf(rows, "لَيِسَ") != 0 || rankOf(rows, "لَيْسَ") == 0 {
		t.Errorf("override should fold لَيِسَ into لَيْسَ, rows: %v", rowIDs(rows))
	}
	if _, err := Rank(ix, corpora(), nil, nil, nil, []Override{{Kind: OverrideLemma, From: "كَلْب", To: "-"}}, Options{}); err == nil {
		t.Error("an override naming an unknown lemma should fail")
	}
}

func rowIDs(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Lemma.ID)
	}
	return out
}

func TestReadCountsSkeletonizesAndMerges(t *testing.T) {
	c, err := ReadCounts("subs", strings.NewReader("حسناً 10\nحسنا 5\nhello 3\n، 100\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Types) != 1 || c.Types[0].Word != "حسنا" || c.Types[0].Count != 15 {
		t.Errorf("types = %+v", c.Types)
	}
	if c.Total != 118 {
		t.Errorf("total should count every token, got %v", c.Total)
	}
}

func TestExactCitationMatchBeatsLemmatizedBase(t *testing.T) {
	jsonl := dump(t,
		entry{Word: "أيضا", Pos: "adv", Forms: []form{{Form: "أَيْضًا", Tags: []string{"canonical"}}}, Senses: []sense{{Glosses: []string{"also"}}}},
		entry{Word: "أيض", Pos: "noun", Forms: []form{{Form: "أَيْض", Tags: []string{"canonical"}}, {Form: "أَيْضًا", Tags: []string{"indefinite", "accusative"}, Source: "declension"}}, Senses: []sense{{Glosses: []string{"returning"}}}},
	)
	entries, err := lexicon.Read(strings.NewReader(jsonl))
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(lexicon.Group(entries))
	corpus := &Corpus{Name: "msa", Total: 100, Types: []Count{{"أيضا", 50}}}
	res, err := Rank(ix, []*Corpus{corpus}, map[string]CamelAnalysis{"أيضا": {Lex: "أَيْض", Pos: "noun"}}, nil, nil, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(res.Rows); !slices.Equal(got, []string{"أَيْضًا"}) {
		t.Errorf("the adverb entry should take all the mass, got %v", got)
	}
	if res.Decisions[0].Outcome != "wiktionary-exact" {
		t.Errorf("outcome = %s", res.Decisions[0].Outcome)
	}
}

func TestFormOverrideForcesAttribution(t *testing.T) {
	ix := fixture(t)
	res, err := Rank(ix, corpora(), nil, nil, nil, []Override{{Kind: OverrideForm, From: "ليس", To: "لَيْسَ"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(res.Rows, "لَيِسَ") != 0 {
		t.Errorf("the forced form should not credit لَيِسَ, rows: %v", rowIDs(res.Rows))
	}
}

func TestReadOverrides(t *testing.T) {
	got, err := ReadOverrides(strings.NewReader("# comment\nlemma\tلَيِسَ\tلَيْسَ\nform\tكل\tكُلّ\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Override{{OverrideLemma, "لَيِسَ", "لَيْسَ"}, {OverrideForm, "كل", "كُلّ"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
	if _, err := ReadOverrides(strings.NewReader("form\tكل\t-\n")); err == nil {
		t.Error("a form override that drops should be rejected")
	}
}
