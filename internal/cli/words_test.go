package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
)

func TestWordListSplitsDeduplicatesAndReadsFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(file, []byte("# my words\nكَلْب\r\n\nماء، كتاب # two on one line\nعين\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := wordList([]string{"عين, بيت", "إِنْ  شَاءَ اللّٰه", "بيت"}, file)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"عين", "بيت", "إِنْ شَاءَ اللّٰه", "كَلْب", "ماء", "كتاب"}
	if !slices.Equal(got, want) {
		t.Fatalf("words = %q, want %q", got, want)
	}
	if got, err := wordList(nil, ""); err != nil || len(got) != 0 {
		t.Errorf("no words: %q, %v", got, err)
	}
}

func TestWordListStripsByteOrderMarksAndDirectionMarks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(file, []byte(marks(0xFEFF)+"كتاب\n"+marks(0x200F)+"عين"+marks(0x200E)+"\n"+marks(0x200F)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := wordList([]string{marks(0x202B) + "ماء" + marks(0x202C), marks(0x200F)}, file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ماء", "كتاب", "عين"}; !slices.Equal(got, want) {
		t.Fatalf("words = %q, want %q", got, want)
	}
}

func TestWordListRejectsTextThatIsNotArabic(t *testing.T) {
	_, err := wordList([]string{"كتاب", "book"}, "")
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), `"book" is not Arabic`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := wordList(nil, filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Error("a missing file should be an error")
	}
}

func TestParseAnswer(t *testing.T) {
	cases := []struct {
		in    string
		picks []int
		v     verdict
		bad   bool
	}{
		{"", []int{0}, pick, false},
		{"2", []int{1}, pick, false},
		{"3, 1", []int{2, 0}, pick, false},
		{"1،2 2", []int{0, 1}, pick, false},
		{"٢", []int{1}, pick, false},
		{"a", []int{0, 1, 2}, pick, false},
		{"ALL", []int{0, 1, 2}, pick, false},
		{"s", nil, skip, false},
		{"q", nil, quit, false},
		{"0", nil, pick, true},
		{"4", nil, pick, true},
		{"x", nil, pick, true},
		{",", nil, pick, true},
	}
	for _, tc := range cases {
		picks, v, err := parseAnswer(tc.in, 3)
		if (err != nil) != tc.bad || !slices.Equal(picks, tc.picks) || (!tc.bad && v != tc.v) {
			t.Errorf("parseAnswer(%q) = %v, %v, %v", tc.in, picks, v, err)
		}
	}
}

func TestDescribeShowsPartOfSpeechAndShortensNestedGlosses(t *testing.T) {
	c := deck.Candidate{Entries: []*lexicon.Entry{{
		Canonical: "عَيْن", Pos: "noun", Gender: "f",
		Senses: []lexicon.Sense{
			{Gloss: "eye (organ)", MSA: true},
			{Gloss: "eye (organ) › envy, the evil eye", MSA: true},
			{Gloss: "old", MSA: false},
			{Gloss: strings.Repeat("long ", 30), MSA: true},
			{Gloss: "never shown", MSA: true},
		},
	}}}
	got := describe(c)
	if !strings.HasPrefix(got, "عَيْن  noun, f  eye (organ); envy, the evil eye; long long") || strings.Contains(got, "old") || strings.Contains(got, "never shown") || !strings.HasSuffix(got, "…") {
		t.Errorf("describe = %q", got)
	}
	verb := deck.Candidate{Entries: []*lexicon.Entry{{Canonical: "عَيَّنَ", Pos: "verb", VerbForm: "II", Senses: []lexicon.Sense{{Gloss: "to appoint", MSA: true}}}}}
	if got := describe(verb); got != "عَيَّنَ  verb, form II  to appoint" {
		t.Errorf("describe = %q", got)
	}
}

func TestAskGivesUpWhenInterrupted(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	a := &asker{ctx: ctx, in: bufio.NewReader(pr), out: io.Discard}
	done := make(chan error, 1)
	go func() {
		_, err := a.ask("? ")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+C must not wait for the user to press Enter")
	}
}

func TestAskTreatsClosedInputAsNoAnswer(t *testing.T) {
	var out strings.Builder
	ctx := context.Background()
	a := &asker{ctx: ctx, in: bufio.NewReader(strings.NewReader("")), out: &out}
	if _, err := a.ask("? "); !errors.Is(err, errNoAnswer) {
		t.Errorf("err = %v", err)
	}
	a = &asker{ctx: ctx, in: bufio.NewReader(strings.NewReader("2")), out: &out}
	if s, err := a.ask("? "); err != nil || s != "2" {
		t.Errorf("a last line without a newline still counts: %q, %v", s, err)
	}
	if yes, err := (&asker{ctx: ctx, in: bufio.NewReader(strings.NewReader("Y\n")), out: &out}).confirm("? "); err != nil || !yes {
		t.Errorf("confirm(Y) = %v, %v", yes, err)
	}
	if yes, err := (&asker{ctx: ctx, in: bufio.NewReader(strings.NewReader("\n")), out: &out}).confirm("? "); err != nil || yes {
		t.Errorf("confirm(enter) = %v, %v", yes, err)
	}
}
