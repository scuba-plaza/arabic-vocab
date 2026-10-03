package deck

import (
	"slices"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

func entry(canonical, pos string, glosses ...string) *lexicon.Entry {
	e := &lexicon.Entry{Title: tashkeel.Skeleton(canonical), Pos: pos, Canonical: canonical}
	for _, g := range glosses {
		e.Senses = append(e.Senses, lexicon.Sense{Gloss: g, MSA: true})
	}
	return e
}

func names(cands []Candidate) []string {
	var out []string
	for _, c := range cands {
		out = append(out, c.Main().Canonical+"/"+c.Main().Pos)
	}
	return out
}

func ranked() []rank.Record {
	return []rank.Record{
		{Rank: 137, ID: "مَاء", CEFR: "A2", Entries: []*lexicon.Entry{entry("مَاء", "noun", "water"), entry("مَاءَ", "verb", "to meow")}},
		{Rank: 5, ID: "عِلْم", Entries: []*lexicon.Entry{entry("عِلْم", "noun", "knowledge")}},
		{Rank: 9, ID: "عَلِمَ", Entries: []*lexicon.Entry{entry("عَلِمَ", "verb", "to know")}},
		{Rank: 40, ID: "أَكَلَ", Entries: []*lexicon.Entry{entry("أَكَلَ", "verb", "to eat")}},
	}
}

func TestFindRankedListsEverySenseOfASpelling(t *testing.T) {
	got := FindRanked("ماء", ranked())
	if want := []string{"مَاء/noun", "مَاءَ/verb"}; !slices.Equal(names(got), want) {
		t.Fatalf("candidates = %v, want %v", names(got), want)
	}
	if !got[0].Primary || got[1].Primary || got[0].Record.Rank != 137 {
		t.Errorf("only the first entry of a record is its main sense: %+v", got)
	}
}

func TestFindRankedOrdersByRankAndNarrowsByVowels(t *testing.T) {
	got := FindRanked("علم", ranked())
	if want := []string{"عِلْم/noun", "عَلِمَ/verb"}; !slices.Equal(names(got), want) {
		t.Fatalf("bare spelling: %v, want %v", names(got), want)
	}
	if got := FindRanked("عِلْم", ranked()); !slices.Equal(names(got), []string{"عِلْم/noun"}) {
		t.Errorf("vowelled: %v", names(got))
	}
	if got := FindRanked("عَلِمَ", ranked()); !slices.Equal(names(got), []string{"عَلِمَ/verb"}) {
		t.Errorf("vowelled verb: %v", names(got))
	}
	if got := FindRanked("عُلِمَ", ranked()); len(got) != 0 {
		t.Errorf("vowels that fit no entry should find nothing: %v", names(got))
	}
}

func TestFindRankedFoldsHamzaOnlyWhenNothingMatchesExactly(t *testing.T) {
	if got := FindRanked("اكل", ranked()); !slices.Equal(names(got), []string{"أَكَلَ/verb"}) {
		t.Fatalf("folded: %v", names(got))
	}
	recs := append(ranked(), rank.Record{Rank: 700, ID: "اكل", Entries: []*lexicon.Entry{entry("اكل", "noun", "x")}})
	if got := FindRanked("اكل", recs); !slices.Equal(names(got), []string{"اكل/noun"}) {
		t.Errorf("an exact spelling should win over a folded one: %v", names(got))
	}
}

func TestFindLeavesOutNamesAndNonMSAEntries(t *testing.T) {
	archaic := entry("مَاء", "adj", "old")
	archaic.Senses[0].MSA = false
	recs := []rank.Record{{Rank: 1, ID: "مَاء", Entries: []*lexicon.Entry{
		entry("مَاء", "name", "a person"), archaic, entry("مَاء", "noun", "water"),
	}}}
	got := FindRanked("ماء", recs)
	if len(got) != 1 || got[0].Main().Pos != "noun" {
		t.Fatalf("candidates = %v", names(got))
	}
	if got[0].Primary {
		t.Errorf("the noun is not the record's first entry, so it must not claim the rank")
	}
}

func TestFindMergesEntriesWithTheSamePartOfSpeech(t *testing.T) {
	recs := []rank.Record{{Rank: 108, ID: "عَيْن", Entries: []*lexicon.Entry{
		entry("عَيْن", "noun", "eye", "spring"), entry("عَيْن", "noun", "the letter ع"),
	}}}
	got := FindRanked("عين", recs)
	if len(got) != 1 || len(got[0].Entries) != 2 || len(got[0].Senses()) != 3 || !got[0].Primary {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestFindEntriesGroupsByVowelledLemmaAndPutsFullerEntriesFirst(t *testing.T) {
	entries := []*lexicon.Entry{
		entry("عَيَّنَ", "verb", "to appoint"),
		entry("عَيْن", "noun", "eye", "spring", "spy"),
		entry("عَيْن", "noun", "the letter ع"),
	}
	got := FindEntries("عين", entries)
	if want := []string{"عَيْن/noun", "عَيَّنَ/verb"}; !slices.Equal(names(got), want) {
		t.Fatalf("candidates = %v, want %v", names(got), want)
	}
	for _, c := range got {
		if c.Record.Rank != 0 || c.claimsRank() {
			t.Errorf("entries from the dump have no rank to claim: %+v", c.Record)
		}
	}
	if len(got[0].Entries) != 2 {
		t.Errorf("the two noun entries should be one candidate: %+v", got[0].Entries)
	}
}

func TestFindAddsSensesOutsideTheRankingWithoutRepeatingRankedOnes(t *testing.T) {
	dump := []*lexicon.Entry{entry("مَاء", "noun", "water"), entry("مَاءَ", "verb", "to meow")}
	got := Find("ماء", ranked(), dump)
	if want := []string{"مَاء/noun", "مَاءَ/verb"}; !slices.Equal(names(got), want) {
		t.Fatalf("a ranked lemma must not be listed again from the dump: %v, want %v", names(got), want)
	}
	for _, c := range got {
		if c.Record.Rank != 137 {
			t.Errorf("ranked senses must come from the ranked record: %+v", c.Record)
		}
	}

	recs := []rank.Record{{Rank: 9, ID: "عَلِمَ", Entries: []*lexicon.Entry{entry("عَلِمَ", "verb", "to know")}}}
	dump = []*lexicon.Entry{entry("عَلِمَ", "verb", "to know"), entry("عُلِمَ", "verb", "to be known")}
	got = Find("علم", recs, dump)
	if want := []string{"عَلِمَ/verb", "عُلِمَ/verb"}; !slices.Equal(names(got), want) {
		t.Fatalf("candidates = %v, want %v", names(got), want)
	}
	if got[0].Record.Rank != 9 || got[1].Record.Rank != 0 {
		t.Errorf("ranks = %d, %d", got[0].Record.Rank, got[1].Record.Rank)
	}
	if got := Find("علم", recs, nil); !slices.Equal(names(got), []string{"عَلِمَ/verb"}) {
		t.Errorf("without a dump only the ranking is searched: %v", names(got))
	}
}

func authored(id string, position int, arabic, pos string) notes.Note {
	return notes.Note{ID: id, Position: position, Arabic: arabic, Pos: pos, English: "x", Example: "y", ExampleEn: "z"}
}

func TestPlannerGivesTheMainSenseOfARankedWordItsRank(t *testing.T) {
	p := NewPlanner([]notes.Note{authored("فِي", 1, "فِي", "prep")})
	c := FindRanked("ماء", ranked())[0]
	pl := p.Add(c)
	n := pl.Note
	if n.ID != "مَاء" || n.Position != 137 || n.CEFR != "A2" || n.Arabic != "مَاء" || n.Pos != "noun" || n.Authored() {
		t.Fatalf("note = %+v", n)
	}
	if pl.Context.ID != "مَاء" || len(pl.Context.Entries) != 1 || !strings.Contains(pl.Feedback, "asked for this entry in particular (noun, مَاء)") {
		t.Errorf("context %+v, feedback %q", pl.Context, pl.Feedback)
	}
	ns, targets := p.Notes()
	if len(ns) != 2 || !slices.Equal(targets, []int{1}) || ns[1].ID != "مَاء" {
		t.Fatalf("notes %+v, targets %v", ns, targets)
	}
}

func TestPlannerKeepsOtherSensesOffTheRankedWordsID(t *testing.T) {
	p := NewPlanner(nil)
	cands := FindRanked("ماء", ranked())
	verb := p.Add(cands[1]).Note
	if verb.ID != "مَاءَ" || verb.Position != UnrankedBase+1 || verb.Pos != "verb" || verb.CEFR != "" {
		t.Fatalf("verb note = %+v", verb)
	}
	noun := p.Add(cands[0]).Note
	if noun.ID != "مَاء" || noun.Position != 137 {
		t.Fatalf("the main sense must still get the lemma's ID and rank: %+v", noun)
	}
	other := p.Add(FindRanked("عِلْم", ranked())[0]).Note
	if other.Position != 5 {
		t.Errorf("another ranked word = %+v", other)
	}
	extra := p.Add(Candidate{Entries: []*lexicon.Entry{entry("كَلْب", "noun", "dog")}, Record: &rank.Record{ID: "كَلْب"}}).Note
	if extra.ID != "كَلْب" || extra.Position != UnrankedBase+2 {
		t.Errorf("word outside the ranking = %+v", extra)
	}
}

func TestPlannerNeverReusesAnIDOrPosition(t *testing.T) {
	existing := []notes.Note{
		authored("مَاء", 137, "مَاءَ", "verb"),
		authored("x", UnrankedBase+7, "x", "noun"),
	}
	p := NewPlanner(existing)
	noun := p.Add(FindRanked("ماء", ranked())[0]).Note
	if noun.ID != "مَاء (noun)" || noun.Position != UnrankedBase+8 {
		t.Fatalf("the ID and rank are taken by another sense, so this one gets its own: %+v", noun)
	}
	again := p.Add(Candidate{Entries: []*lexicon.Entry{entry("مَاء", "adj", "wet")}, Record: &rank.Record{ID: "z"}}).Note
	if again.ID != "مَاء (adj)" {
		t.Errorf("id = %q", again.ID)
	}
	third := p.Add(Candidate{Entries: []*lexicon.Entry{entry("مَاء", "adj", "wetter", "x")}, Record: &rank.Record{ID: "z2"}})
	if !third.Resumed {
		t.Errorf("the same word asked for twice is one note: %+v", third)
	}
	ns, targets := p.Notes()
	ids := map[string]bool{}
	for _, n := range ns {
		ids[n.ID] = true
	}
	if len(ids) != len(ns) || len(targets) != 2 {
		t.Errorf("notes %+v, targets %v", ns, targets)
	}
	if err := notes.Validate(ns); err != nil {
		t.Error(err)
	}
}

func TestPlannerRecognisesWordsAlreadyInTheDeck(t *testing.T) {
	written := authored("مَاء", 137, "مَاء", "noun")
	draft := notes.Note{ID: "مَاءَ", Position: UnrankedBase + 1, Arabic: "مَاءَ", Pos: "verb"}
	p := NewPlanner([]notes.Note{written, draft})
	cands := FindRanked("ماء", ranked())
	if n, ok := p.Existing(cands[0]); !ok || n.Position != 137 {
		t.Fatalf("Existing = %+v, %v", n, ok)
	}
	if pl := p.Add(cands[0]); !pl.Present || pl.Resumed {
		t.Errorf("a written note needs nothing: %+v", pl)
	}
	pl := p.Add(cands[1])
	if !pl.Resumed || pl.Present || pl.Note.ID != "مَاءَ" || pl.Context.ID != "مَاءَ" {
		t.Errorf("an unwritten draft is picked up again: %+v", pl)
	}
	ns, targets := p.Notes()
	if len(ns) != 2 || len(targets) != 1 || ns[targets[0]].ID != "مَاءَ" {
		t.Errorf("notes %+v, targets %v", ns, targets)
	}
}

func addedByNextWords(ns []notes.Note, recs []rank.Record) []string {
	out, targets := NextWords(ns, recs, 10)
	var ids []string
	for _, i := range targets {
		ids = append(ids, out[i].ID)
	}
	return ids
}

func TestNamedWordsDoNotBlockOrDuplicateTheNextRankedWords(t *testing.T) {
	recs := ranked()
	cands := FindRanked("ماء", recs)

	p := NewPlanner(nil)
	p.Add(cands[1])
	ns, _ := p.Notes()
	ns[0].English, ns[0].Example, ns[0].ExampleEn = "to meow", "x", "y"
	if added := addedByNextWords(ns, recs); !slices.Contains(added, "مَاء") {
		t.Errorf("a named secondary sense must not stop rank 137 from being added: %v", added)
	}

	p = NewPlanner(nil)
	p.Add(cands[0])
	ns, _ = p.Notes()
	ns[0].English, ns[0].Example, ns[0].ExampleEn = "water", "x", "y"
	added := addedByNextWords(ns, recs)
	if slices.Contains(added, "مَاء") || len(added) != 3 {
		t.Errorf("a named main sense must not be added a second time: %v", added)
	}
}
