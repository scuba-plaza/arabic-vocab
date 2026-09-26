package deck

import (
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

func TestCardsNeverUseABoldWeight(t *testing.T) {
	css := NoteType().CSS
	if !strings.Contains(css, ".card b, .card strong {\n  font-weight: normal;") || strings.Contains(css, "bold") {
		t.Error("the bundled font has no bold face, so <b> must not change the weight")
	}
}
