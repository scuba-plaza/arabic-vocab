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
	p := NewPlanner([]notes.Note{authored("فِي", 1, "فِي", "prep")}, ranked())
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
	p := NewPlanner(nil, ranked())
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
		authored("مَاء", UnrankedBase+3, "مَاءَ", "verb"),
		authored("x", UnrankedBase+7, "x", "noun"),
	}
	p := NewPlanner(existing, ranked())
	noun := p.Add(FindRanked("ماء", ranked())[0]).Note
	if noun.ID != "مَاء (noun)" || noun.Position != UnrankedBase+8 {
		t.Fatalf("the ID is taken by another sense, so this one gets its own: %+v", noun)
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
	p := NewPlanner([]notes.Note{written, draft}, ranked())
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

	p := NewPlanner(nil, recs)
	p.Add(cands[1])
	ns, _ := p.Notes()
	ns[0].English, ns[0].Example, ns[0].ExampleEn = "to meow", "x", "y"
	if added := addedByNextWords(ns, recs); !slices.Contains(added, "مَاء") {
		t.Errorf("a named secondary sense must not stop rank 137 from being added: %v", added)
	}

	p = NewPlanner(nil, recs)
	p.Add(cands[0])
	ns, _ = p.Notes()
	ns[0].English, ns[0].Example, ns[0].ExampleEn = "water", "x", "y"
	added := addedByNextWords(ns, recs)
	if slices.Contains(added, "مَاء") || len(added) != 3 {
		t.Errorf("a named main sense must not be added a second time: %v", added)
	}
}

func titled(title, canonical, pos, gloss string) *lexicon.Entry {
	e := entry(canonical, pos, gloss)
	e.Title = title
	return e
}

func sensesOfMa() []rank.Record {
	return []rank.Record{{Rank: 7, ID: "مَا", Entries: []*lexicon.Entry{entry("مَا", "pron", "what"), entry("مَا", "adv", "not")}}}
}

func TestPlannerNeverHandsARankedIDToAnotherSense(t *testing.T) {
	recs := sensesOfMa()
	cands := FindRanked("ما", recs)
	if len(cands) != 2 || !cands[0].Primary || cands[1].Primary {
		t.Fatalf("candidates = %v", names(cands))
	}

	p := NewPlanner(nil, recs)
	adv := p.Add(cands[1])
	if adv.Note.ID != "مَا (adv)" || adv.Note.Position != UnrankedBase+1 {
		t.Fatalf("a secondary sense with the headword of a ranked word must not take its ID: %+v", adv.Note)
	}
	ns, _ := p.Notes()
	ns[0].English, ns[0].Example, ns[0].ExampleEn = "not", "x", "y"
	out, targets := NextWords(ns, recs, 10)
	if len(targets) != 1 || out[targets[0]].ID != "مَا" || out[targets[0]].Position != 7 {
		t.Errorf("the main sense must still be added later, with its rank: %+v", out)
	}

	p = NewPlanner(nil, recs)
	other := p.Add(Candidate{Entries: []*lexicon.Entry{entry("مَا", "particle", "that")}, Record: &rank.Record{ID: "zz"}})
	if other.Note.ID != "مَا (particle)" {
		t.Errorf("an entry from the dump with a ranked headword: %+v", other.Note)
	}
	main := p.Add(cands[0]).Note
	if main.ID != "مَا" || main.Position != 7 {
		t.Errorf("the main sense = %+v", main)
	}
}

func TestPlannerGivesEveryRealRankedHeadwordToItsMainSenseOnly(t *testing.T) {
	records, err := notes.ReadJSONL[rank.Record]("../../decks/msa-core/lexicon.jsonl")
	if err != nil || len(records) == 0 {
		t.Skip("the ranked lexicon is not available")
	}
	ids := map[string]bool{}
	for _, r := range records {
		ids[r.ID] = true
	}
	checked := 0
	for _, rec := range records {
		for _, e := range rec.Entries[min(1, len(rec.Entries)):] {
			if e.Canonical != rec.ID || !e.MSA() || e.Pos == "name" {
				continue
			}
			for _, c := range FindRanked(rec.ID, records) {
				if c.Primary {
					continue
				}
				checked++
				id := NewPlanner(nil, records).Add(c).Note.ID
				if ids[id] {
					t.Fatalf("%s as %s took the ID of a ranked word: %q", c.Record.ID, c.Main().Pos, id)
				}
			}
			break
		}
	}
	if checked == 0 {
		t.Fatal("no secondary sense shares its ranked word's headword; the check did not run")
	}
}

func TestPlannerRecognisesTheNoteOfARankedWordWhateverCurationDidToIt(t *testing.T) {
	recs := []rank.Record{
		{Rank: 112, ID: "لَنْ", Entries: []*lexicon.Entry{entry("لَنْ", "adv", "will not")}},
		{Rank: 38, ID: "أَمْكَنَ", Entries: []*lexicon.Entry{entry("أَمْكَنَ", "verb", "to be possible")}},
		{Rank: 69, ID: "أَجْل", Entries: []*lexicon.Entry{entry("أَجْل", "noun", "cause")}},
		{Rank: 137, ID: "مَاء", Entries: []*lexicon.Entry{entry("مَاء", "noun", "water"), entry("مَاءَ", "verb", "to meow")}},
	}
	existing := []notes.Note{
		authored("لَنْ", 112, "لَنْ", "particle"),
		authored("يُمْكِنُ", 38, "يُمْكِنُ", "verb"),
		authored("مِنْ أَجْلِ", 69, "مِنْ أَجْلِ", "phrase"),
		authored("مَاء", 137, "مَاء", "noun"),
	}
	p := NewPlanner(existing, recs)
	for _, word := range []string{"لن", "أمكن", "أجل", "ماء"} {
		c := FindRanked(word, recs)[0]
		pl := p.Add(c)
		if !pl.Present {
			t.Errorf("%s: the deck already has it, but %+v", word, pl.Note)
		}
	}
	verb := FindRanked("ماء", recs)[1]
	if n, ok := p.Existing(verb); ok {
		t.Errorf("another sense of the spelling is not the main sense's note: %+v", n)
	}
	if pl := p.Add(verb); pl.Present || pl.Resumed {
		t.Errorf("the verb is new: %+v", pl)
	}
}

func TestPlannerHoldingFindsNotesByTheirOwnText(t *testing.T) {
	p := NewPlanner([]notes.Note{
		authored("يُمْكِنُ", 38, "يُمْكِنُ", "verb"),
		authored("مِنْ أَجْلِ", 69, "مِنْ أَجْلِ", "phrase"),
		{ID: "كَلْب", Position: UnrankedBase + 1, Arabic: "كَلْب", Pos: "noun"},
	}, nil)
	for word, want := range map[string]string{"يمكن": "يُمْكِنُ", "يُمْكِنُ": "يُمْكِنُ", "من أجل": "مِنْ أَجْلِ", "من اجل": "مِنْ أَجْلِ"} {
		if got := p.Holding(word); len(got) != 1 || got[0].Arabic != want {
			t.Errorf("Holding(%q) = %+v, want %s", word, got, want)
		}
	}
	for _, word := range []string{"مكن", "يَمْكُنُ", "كلب", "", "book"} {
		if got := p.Holding(word); len(got) != 0 {
			t.Errorf("Holding(%q) = %+v, want nothing: an unwritten draft is not in the deck", word, got)
		}
	}
}

func TestCheckEntryHoldsACardToTheAskedForEntry(t *testing.T) {
	noun := notes.Note{Arabic: "مَاء", Pos: "noun"}
	adv := notes.Note{Arabic: "لَنْ", Pos: "adv"}
	verb := notes.Note{Arabic: "أَمْكَنَ", Pos: "verb"}
	cases := []struct {
		name   string
		draft  notes.Note
		pos    string
		arabic string
		bad    string
	}{
		{"same entry", noun, "noun", "مَاء", ""},
		{"vowels added to a bare headword", notes.Note{Arabic: "ماء", Pos: "noun"}, "noun", "مَاء", ""},
		{"case ending on the headword", noun, "noun", "مَاءٌ", ""},
		{"function word labelled differently", adv, "particle", "لَنْ", ""},
		{"noun turned into a verb", noun, "verb", "مَاءَ", "the learner asked for the noun مَاء, but the card is for a verb"},
		{"verb turned into a noun", verb, "noun", "أَمْكَنَ", "the card is for a noun"},
		{"noun turned into an adjective", noun, "adj", "مَاء", "the card is for a adj"},
		{"another headword", noun, "noun", "مِيَاه", "the card's headword is مِيَاه"},
		{"other inner vowels", notes.Note{Arabic: "عِلْم", Pos: "noun"}, "noun", "عَلَم", "the card's headword is عَلَم"},
	}
	for _, tc := range cases {
		err := CheckEntry(tc.draft, tc.pos, tc.arabic)
		switch {
		case tc.bad == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)):
			t.Errorf("%s: err = %v, want it to say %q", tc.name, err, tc.bad)
		}
	}
}

func TestFindMatchesTheTitleAsWellAsTheCanonicalForm(t *testing.T) {
	recs := []rank.Record{
		{Rank: 1, ID: "جَامِعَة", Entries: []*lexicon.Entry{titled("جامعة", "الجَامِعَة", "noun", "university")}},
		{Rank: 2, ID: "خَرِفَ", Entries: []*lexicon.Entry{titled("خرف", "خَرِفَ خَرَفَ", "verb", "to be senile")}},
		{Rank: 3, ID: "كَيْفَ حَالُكَ", Entries: []*lexicon.Entry{titled("كيف حالك", "كَيْفَ حَالُكَ؟", "phrase", "how are you?")}},
		{Rank: 4, ID: "مَعْفُوج", Entries: []*lexicon.Entry{titled("عفج", "مَعْفُوج", "noun", "x")}},
	}
	cases := map[string]string{
		"جامعة":      "الجَامِعَة/noun",
		"جَامِعَة":   "الجَامِعَة/noun",
		"الجامعة":    "الجَامِعَة/noun",
		"خرف":        "خَرِفَ خَرَفَ/verb",
		"خَرِفَ":     "خَرِفَ خَرَفَ/verb",
		"خَرَفَ":     "خَرِفَ خَرَفَ/verb",
		"كيف حالك":   "كَيْفَ حَالُكَ؟/phrase",
		"معفوج":      "مَعْفُوج/noun",
		"عفج":        "مَعْفُوج/noun",
		"إِكْلِيل":   "",
		"جُمِعَة":    "",
		"خَرُفَ":     "",
		"كيف حالكما": "",
	}
	for word, want := range cases {
		got := strings.Join(names(FindRanked(word, recs)), " ")
		if got != want {
			t.Errorf("FindRanked(%q) = %q, want %q", word, got, want)
		}
	}
}

func invisible(codes ...rune) string {
	return string(codes)
}

func TestFindUsesTheTitleOnlyWhenNoCanonicalFormMatches(t *testing.T) {
	recs := []rank.Record{
		{Rank: 654, ID: "جَامِعَة", Entries: []*lexicon.Entry{titled("جامعة", "جَامِعَة", "noun", "university")}},
		{Rank: 1182, ID: "الجَامِعَة", Entries: []*lexicon.Entry{titled("جامعة", "الجَامِعَة", "noun", "the league")}},
	}
	if got := names(FindRanked("جامعة", recs)); !slices.Equal(got, []string{"جَامِعَة/noun"}) {
		t.Errorf("a canonical match must not be joined by title matches, which would only add questions: %v", got)
	}
	if got := names(FindRanked("الجامعة", recs)); !slices.Equal(got, []string{"الجَامِعَة/noun"}) {
		t.Errorf("the definite spelling: %v", got)
	}
	alone := recs[1:]
	if got := names(FindRanked("جامعة", alone)); !slices.Equal(got, []string{"الجَامِعَة/noun"}) {
		t.Errorf("an entry whose canonical form differs from its title is still found by the title: %v", got)
	}
	folded := []rank.Record{{Rank: 1, ID: "x", Entries: []*lexicon.Entry{titled("أكل", "الأَكْل", "noun", "eating")}}}
	if got := names(FindRanked("اكل", folded)); !slices.Equal(got, []string{"الأَكْل/noun"}) {
		t.Errorf("the title is matched with hamza folded too: %v", got)
	}
}

func TestNormalizeWordDropsInvisibleCharacters(t *testing.T) {
	const bom, lrm, rlm, rle, pdf, rli, pdi, alm, zwnj, nbsp = 0xFEFF, 0x200E, 0x200F, 0x202B, 0x202C, 0x2067, 0x2069, 0x061C, 0x200C, 0x00A0
	cases := map[string]string{
		invisible(bom) + "كتاب":                  "كتاب",
		invisible(rlm) + "كتاب" + invisible(lrm): "كتاب",
		invisible(rle) + "كتاب" + invisible(pdf): "كتاب",
		invisible(rli) + "كتاب" + invisible(pdi): "كتاب",
		invisible(alm) + "كتاب":                  "كتاب",
		"ك" + invisible(zwnj) + "تاب":            "كتاب",
		"كتــاب":                                 "كتاب",
		"  كيف" + invisible(nbsp, rlm) + "حالك ": "كيف حالك",
		"":                  "",
		invisible(bom, rlm): "",
	}
	for in, want := range cases {
		if got := NormalizeWord(in); got != want {
			t.Errorf("NormalizeWord(%q) = %q, want %q", in, got, want)
		}
	}
	if got := FindRanked(invisible(bom)+"ماء"+invisible(rlm), ranked()); len(got) != 2 {
		t.Errorf("a word wrapped in marks is still found: %v", names(got))
	}
}
