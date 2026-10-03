package deck

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func note(id string, pos int) notes.Note {
	return notes.Note{
		ID: id, Position: pos, Arabic: id, Pos: "noun", Gender: "m",
		English: "book", Example: "قَرَأْتُ <b>كِتَابًا</b>.", ExampleEn: "I read a book.",
	}
}

func tagsOf(t *testing.T, ns []notes.Note, checks []notes.Check) map[string][]string {
	t.Helper()
	pkg, _, err := BuildPackage(ns, checks, nil, BuildOptions{ProductionLimit: 1, MediaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for i, n := range pkg.Notes {
		out[ns[i].ID] = n.Tags
	}
	return out
}

func TestBuildTagsBySeverityAndStaleness(t *testing.T) {
	a, b, c, d := note("كِتَاب", 1), note("قَلَم", 2), note("بَيْت", 3), note("بَاب", 4)
	checks := []notes.Check{
		{ID: a.ID, Version: CheckVersion, Digest: a.Digest(), Issues: []notes.Issue{{Kind: "diacritics", Severity: notes.Major, Word: "كِتَابًا"}}},
		{ID: b.ID, Version: CheckVersion, Digest: b.Digest(), Issues: []notes.Issue{{Kind: "diacritics", Severity: notes.Minor, Word: "قَرَأْتُ"}}},
		{ID: c.ID, Version: CheckVersion, Digest: "stale"},
	}
	tags := tagsOf(t, []notes.Note{a, b, c, d}, checks)
	want := map[string]string{a.ID: "check::diacritics", b.ID: "check::diacritics-minor", c.ID: "check::unverified", d.ID: "check::unverified"}
	for id, tag := range want {
		if !slices.Contains(tags[id], tag) {
			t.Errorf("%s tags %v, want %s", id, tags[id], tag)
		}
	}
	b.Reviewed = []string{"قَرَأْتُ"}
	if tags := tagsOf(t, []notes.Note{b}, checks[1:2]); slices.ContainsFunc(tags[b.ID], func(s string) bool { return strings.HasPrefix(s, "check::") }) {
		t.Errorf("a reviewed word should not keep its tag: %v", tags[b.ID])
	}
}

func TestBuildAddsProductionCardsUpToTheLimit(t *testing.T) {
	no := false
	first, second, third := note("كِتَاب", 1), note("قَلَم", 2), note("بَيْت", 1)
	third.ID, third.Position, third.Production = "بَاب", 3, &no
	pkg, summary, err := BuildPackage([]notes.Note{first, second, third}, nil, nil, BuildOptions{ProductionLimit: 1, ProductionDelay: 5, MediaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Production != 1 || len(pkg.Notes[0].Cards) != 2 || len(pkg.Notes[1].Cards) != 1 {
		t.Fatalf("summary %+v", summary)
	}
	if pkg.Notes[0].Cards[1].Due != 6 {
		t.Errorf("production card due = %d, want 6", pkg.Notes[0].Cards[1].Due)
	}
	if !slices.Contains(pkg.Notes[0].Tags, "rank::0001-0500") || !slices.Contains(pkg.Notes[0].Tags, "pos::noun") {
		t.Errorf("tags %v", pkg.Notes[0].Tags)
	}
}

func TestBuildTagsWordsOutsideTheRankingAsUnranked(t *testing.T) {
	ranked, extra := note("كِتَاب", 1), note("قَلَم", 2)
	ranked.Position, extra.Position = 500, UnrankedBase+1
	extra.ID = "قَلَم"
	pkg, _, err := BuildPackage([]notes.Note{ranked, extra}, nil, nil, BuildOptions{MediaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(pkg.Notes[0].Tags, "rank::0001-0500") {
		t.Errorf("ranked tags %v", pkg.Notes[0].Tags)
	}
	if !slices.Contains(pkg.Notes[1].Tags, "rank::unranked") || slices.ContainsFunc(pkg.Notes[1].Tags, func(s string) bool { return strings.HasPrefix(s, "rank::1") }) {
		t.Errorf("a word added by name outside the ranking should be tagged rank::unranked, got %v", pkg.Notes[1].Tags)
	}
}

func TestMarkArabicWrapsRuns(t *testing.T) {
	got := markArabic("(+ أَنْ + verb) & more")
	if !strings.Contains(got, `<span class="ar" lang="ar" dir="rtl">أَنْ</span>`) || !strings.Contains(got, "&amp;") {
		t.Errorf("markArabic = %q", got)
	}
}

func TestEvaluateSeverity(t *testing.T) {
	n := notes.Note{ID: "x", Arabic: "كِتَاب", English: "book", Example: "هٰذَا <b>كِتَابٌ</b>.", ExampleEn: "This is a book."}
	items := CheckItems([]notes.Note{n})
	results := []CheckResult{
		{ID: "x", Field: "arabic", Analyses: [][]string{{"كِتابٌ", "كُتّابٌ"}}},
		{ID: "x", Field: "example", Analyses: [][]string{{"هٰذا"}, {"كِتابٌ", "كِتابُ"}}, CATT: []string{"هَذَا", "كِتَابُ"}, BERT: []string{"هٰذا", "كِتابٌ"}},
	}
	checks := Evaluate([]notes.Note{n}, items, results)
	if len(checks[0].Issues) != 1 || checks[0].Issues[0].Severity != notes.Minor {
		t.Fatalf("issues %+v", checks[0].Issues)
	}
	if checks[0].Version != CheckVersion {
		t.Errorf("version = %d", checks[0].Version)
	}
	if is := checks[0].Issues[0]; is.CATT != "كِتَابُ" || is.CAMeL != "كِتابٌ" {
		t.Errorf("readings not kept: %+v", is)
	}
	results[1].BERT = []string{"هٰذا", "كِتابُ"}
	checks = Evaluate([]notes.Note{n}, items, results)
	if len(checks[0].Issues) != 1 || checks[0].Issues[0].Severity != notes.Major {
		t.Fatalf("issues %+v", checks[0].Issues)
	}
	results[1].Analyses[1] = []string{"كَتَبَ"}
	checks = Evaluate([]notes.Note{n}, items, results)
	if !slices.ContainsFunc(checks[0].Issues, func(i notes.Issue) bool {
		return i.Kind == "invalid" && slices.Equal(i.Known, []string{"كَتَبَ"})
	}) {
		t.Errorf("a vowelling CAMeL does not know should be invalid and list what it knows: %+v", checks[0].Issues)
	}
}

func TestEvaluateOnlyFlagsLettersThatNeedAMark(t *testing.T) {
	a := notes.Note{ID: "a", Arabic: "مَكتَب", Forms: []notes.Form{{Label: "pl.", Arabic: "مَكاتِب"}}, Example: "هٰذَا <b>مَكتَبٌ</b> جَمِيل جِدّاً."}
	b := notes.Note{ID: "b", Arabic: "إِنْ", Forms: []notes.Form{{Label: "x", Arabic: "مَكتَبَة"}}, Example: "<b>إِن</b> كتَبَ الْوَلَدُ نَجَحَ."}
	ns := []notes.Note{a, b}
	results := []CheckResult{
		{Analyses: [][]string{{"مَكْتَب"}}},
		{Analyses: [][]string{{"مَكَاتِب"}}},
		{Analyses: [][]string{{"هٰذا"}, {"مَكْتَبٌ"}, {"جَمِيلٌ", "جَمِيل"}, {"جِدًّا"}}, CATT: []string{"هَذَا", "مَكْتَبٌ", "جَمِيلٌ", "جِدًّا"}, BERT: []string{"هٰذا", "مَكْتَبٌ", "جَمِيلٌ", "جِدًّا"}},
		{Analyses: [][]string{{"إِنْ"}}},
		{Analyses: [][]string{nil}},
		{Analyses: [][]string{{"إِنْ"}, {"كَتَبَ"}, {"الْوَلَدُ"}, {"نَجَحَ"}}, CATT: []string{"إِنْ", "كَتَبَ", "الْوَلَدُ", "نَجَحَ"}, BERT: []string{"إِنْ", "كَتَبَ", "الْوَلَدُ", "نَجَحَ"}},
	}
	checks := Evaluate(ns, CheckItems(ns), results)
	flags := func(c notes.Check) []string {
		var out []string
		for _, is := range c.Issues {
			out = append(out, is.Kind+" "+is.Word)
		}
		return out
	}
	if got := flags(checks[0]); !slices.Equal(got, []string{"unmarked جَمِيل"}) {
		t.Fatalf("a missing sukun, a fatha before alif and tanween on the alif need no flag, a missing ending does: %q", got)
	}
	ending := checks[0].Issues[0]
	if !slices.Equal(ending.Missing, []int{3}) || ending.CATT != "جَمِيلٌ" || ending.Detail != "the ending has no vowel mark; CATT reads جَمِيلٌ; CAMeL reads جَمِيلٌ" {
		t.Errorf("ending issue %+v", ending)
	}
	if got := flags(checks[1]); !slices.Equal(got, []string{"unmarked مَكتَبَة", "unknown مَكتَبَة", "unmarked كتَبَ", "invalid كتَبَ"}) {
		t.Fatalf("a bare letter no reading explains stays flagged, without a diacritics flag that says the same: %q", got)
	}
	if vowel := checks[1].Issues[2]; !slices.Equal(vowel.Missing, []int{0}) || !strings.HasPrefix(vowel.Detail, "no vowel mark on ك; CATT reads كَتَبَ") {
		t.Errorf("missing vowel issue %+v", vowel)
	}
}

func TestCardsNeverUseABoldWeight(t *testing.T) {
	css := NoteType().CSS
	if !strings.Contains(css, ".card b, .card strong {\n  font-weight: normal;") || strings.Contains(css, "bold") {
		t.Error("the bundled font has no bold face, so <b> must not change the weight")
	}
}

func audioCheck(n notes.Note, issues ...notes.Issue) notes.Check {
	return notes.Check{ID: n.ID, Version: AudioCheckVersion, Digest: n.Digest(), Issues: issues}
}

func packageWith(t *testing.T, ns []notes.Note, checks, audio []notes.Check) (map[string]struct {
	Tags  []string
	Check string
}, BuildSummary) {
	t.Helper()
	pkg, summary, err := BuildPackage(ns, checks, audio, BuildOptions{MediaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]struct {
		Tags  []string
		Check string
	}{}
	for i, n := range pkg.Notes {
		out[ns[i].ID] = struct {
			Tags  []string
			Check string
		}{n.Tags, n.Fields[fieldIndex("Check")]}
	}
	return out, summary
}

func TestBuildTagsClipsThatHaveNoSound(t *testing.T) {
	a, b, c, d, e := note("كِتَاب", 1), note("قَلَم", 2), note("بَيْت", 3), note("بَاب", 4), note("شَجَرَة", 5)
	b.Reviewed = []string{"ExampleAudio:silent"}
	audio := []notes.Check{
		audioCheck(a, SilentIssue("ExampleAudio", 4)),
		audioCheck(b, SilentIssue("ExampleAudio", 4)),
		{ID: c.ID, Version: AudioCheckVersion, Digest: "old text", Issues: []notes.Issue{SilentIssue("ExampleAudio", 4)}},
		audioCheck(e, SilentIssue("WordAudio", 4), SilentIssue("ExampleAudio", 4)),
	}
	checks := []notes.Check{{ID: d.ID, Version: CheckVersion, Digest: d.Digest(), Issues: []notes.Issue{{Kind: "diacritics", Severity: notes.Major, Word: "كِتَابًا", Detail: "x"}}}}
	audio = append(audio, audioCheck(d, SilentIssue("WordAudio", 4)))

	got, summary := packageWith(t, []notes.Note{a, b, c, d, e}, checks, audio)
	if !slices.Contains(got[a.ID].Tags, "check::audio") || !strings.Contains(got[a.ID].Check, "The sentence audio has no sound") {
		t.Errorf("a: %+v", got[a.ID])
	}
	if !slices.Contains(got[b.ID].Tags, "check::audio") || !strings.Contains(got[b.ID].Check, "no sound") {
		t.Errorf("a silent clip cannot be reviewed away, only made again: %+v", got[b.ID])
	}
	if slices.Contains(got[c.ID].Tags, "check::audio") {
		t.Errorf("a flag about text that changed should not count: %+v", got[c.ID])
	}
	if !slices.Contains(got[d.ID].Tags, "check::audio") || !slices.Contains(got[d.ID].Tags, "check::diacritics") || !strings.Contains(got[d.ID].Check, "The word audio has no sound") || !strings.Contains(got[d.ID].Check, "x") {
		t.Errorf("a note with both kinds of flag shows both: %+v", got[d.ID])
	}
	if n := strings.Count(strings.Join(got[e.ID].Tags, " "), "check::audio"); n != 1 {
		t.Errorf("two silent clips still make one tag, got %d: %v", n, got[e.ID].Tags)
	}
	if strings.Count(got[e.ID].Check, "no sound") != 2 {
		t.Errorf("each silent clip gets its own line: %q", got[e.ID].Check)
	}
	if summary.Tagged["check::audio"] != 4 {
		t.Errorf("tagged = %v", summary.Tagged)
	}
}

func TestBuildLeavesOutTheSoundOfAClipThatIsMissing(t *testing.T) {
	a := note("كِتَاب", 1)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ar-word.mp3"), []byte("x"), 0o644)
	pkg, summary, err := BuildPackage([]notes.Note{a}, nil, []notes.Check{audioCheck(a, SilentIssue("ExampleAudio", 4))}, BuildOptions{
		MediaDir: dir,
		Audio:    map[string]string{AudioTexts(a)[0].Text: "ar-word.mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := pkg.Notes[0].Fields
	if !strings.HasPrefix(fields[fieldIndex("WordAudio")], "[sound:") || fields[fieldIndex("ExampleAudio")] != "" {
		t.Errorf("word %q, example %q", fields[fieldIndex("WordAudio")], fields[fieldIndex("ExampleAudio")])
	}
	if summary.AudioFiles != 1 || summary.MissingAudio != 1 {
		t.Errorf("summary = %+v", summary)
	}
}
