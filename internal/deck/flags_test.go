package deck

import (
	"slices"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func TestOpenIssuesHonoursReviewedKeys(t *testing.T) {
	n := note("كِتَاب", 1)
	c := notes.Check{ID: n.ID, Version: CheckVersion, Digest: n.Digest(), Issues: []notes.Issue{
		{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "كِتَابًا"},
		{Field: "example", Kind: "unchecked", Severity: notes.Major},
	}}
	if got := OpenIssues(n, c); len(got) != 2 {
		t.Fatalf("open = %+v", got)
	}
	n.Reviewed = []string{"كِتَابًا", "example:unchecked"}
	if got := OpenIssues(n, c); len(got) != 0 {
		t.Fatalf("reviewed issues still open: %+v", got)
	}
	tags := tagsOf(t, []notes.Note{n}, []notes.Check{c})
	if slices.ContainsFunc(tags[n.ID], func(s string) bool { return s == "check::diacritics" }) {
		t.Errorf("tags = %v", tags[n.ID])
	}
}

func TestOpenAudioIgnoresChecksForOldText(t *testing.T) {
	n := note("كِتَاب", 1)
	text := AudioTexts(n)[1].Text
	index := map[string]string{text: "ar-new.mp3", "قَرَأْتُ كِتَابًا قَدِيمًا.": "ar-old.mp3"}
	checks := []notes.AudioCheck{
		{ID: n.ID, Field: "ExampleAudio", Text: "قَرَأْتُ كِتَابًا قَدِيمًا.", File: "ar-old.mp3", Transcript: "x"},
		{ID: n.ID, Field: "ExampleAudio", Text: text, File: "ar-new.mp3", Transcript: "y"},
		{ID: n.ID, Field: "ExampleAudio", Text: text, File: "ar-older.mp3", Transcript: "z"},
		{ID: "other", Field: "ExampleAudio", Text: text, File: "ar-new.mp3", Transcript: "w"},
	}
	got := OpenAudio(n, checks, index)
	if len(got) != 1 || got[0].File != "ar-new.mp3" {
		t.Fatalf("open audio = %+v", got)
	}
	n.ReviewedAudio = []string{"ar-new.mp3"}
	if got := OpenAudio(n, checks, index); len(got) != 0 {
		t.Fatalf("reviewed clip still open: %+v", got)
	}
}

func TestResultsFromAnOlderCheckNeedAFreshCheck(t *testing.T) {
	n := note("كِتَاب", 1)
	old := notes.Check{ID: n.ID, Digest: n.Digest(), Issues: []notes.Issue{{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "كِتَابًا"}}}
	if Current(n, old, true) {
		t.Fatal("a check without the current version should not count")
	}
	fresh := old
	fresh.Version = CheckVersion
	if !Current(n, fresh, true) {
		t.Fatal("a check with the current version and digest should count")
	}
	if tags := tagsOf(t, []notes.Note{n}, []notes.Check{old}); !slices.Contains(tags[n.ID], "check::unverified") || slices.Contains(tags[n.ID], "check::diacritics") {
		t.Errorf("tags for an old check = %v", tags[n.ID])
	}
}
