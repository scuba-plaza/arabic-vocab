package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/curate"
	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

const (
	dumpEye     = `{"word":"عين","pos":"noun","forms":[{"form":"عَيْن","tags":["canonical","feminine"]},{"form":"عُيُون","tags":["plural"]}],"senses":[{"glosses":["eye (organ)"]}]}`
	dumpAppoint = `{"word":"عين","pos":"verb","forms":[{"form":"عَيَّنَ","tags":["canonical","form-ii"]},{"form":"يُعَيِّنُ","tags":["non-past"]},{"form":"تَعْيِين","tags":["noun-from-verb"]}],"senses":[{"glosses":["to appoint"]}]}`
	dumpDog     = `{"word":"كلب","pos":"noun","forms":[{"form":"كَلْب","tags":["canonical","masculine"]},{"form":"كِلَاب","tags":["plural"]}],"senses":[{"glosses":["dog"]}]}`
	dumpBook    = `{"word":"كتاب","pos":"noun","forms":[{"form":"كِتَاب","tags":["canonical","masculine"]}],"senses":[{"glosses":["book"]}]}`
)

func TestMain(m *testing.M) {
	if log := os.Getenv("FAKE_CLAUDE_LOG"); log != "" {
		fakeClaude(log)
	}
	os.Exit(m.Run())
}

func fakeClaude(log string) {
	prompt, _ := io.ReadAll(os.Stdin)
	for path, text := range map[string]string{log: string(prompt), log + ".args": strings.Join(os.Args[1:], "\n")} {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.WriteString(text + "\n=====\n")
			f.Close()
		}
	}
	var cards []curate.Card
	lines := strings.Split(string(prompt), "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "Draft generated from Wiktionary") || i+1 >= len(lines) {
			continue
		}
		var c curate.Card
		if err := json.Unmarshal([]byte(lines[i+1]), &c); err != nil {
			continue
		}
		c.English = "stub " + c.Pos
		c.Example = "<b>هٰذَا</b> كِتَابٌ جَدِيدٌ."
		c.ExampleEn = "This is a new book."
		c.Comment = ""
		cards = append(cards, c)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"type":              "result",
		"subtype":           "success",
		"is_error":          false,
		"structured_output": map[string]any{"cards": cards},
		"usage":             map[string]int{"input_tokens": 1, "output_tokens": 1},
		"modelUsage":        map[string]any{"stub": map[string]any{}},
	})
	os.Exit(0)
}

type fixture struct {
	t     *testing.T
	paths *deck.Paths
	exe   string
	log   string
}

func newFixture(t *testing.T, existing ...notes.Note) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{
		t:     t,
		paths: &deck.Paths{Deck: filepath.Join(dir, "deck"), Cache: filepath.Join(dir, "cache")},
		log:   filepath.Join(dir, "claude.log"),
	}
	var err error
	if f.exe, err = os.Executable(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_LOG", f.log)
	entry := func(canonical, pos, gloss string) []*lexicon.Entry {
		return []*lexicon.Entry{{Title: strings.Map(func(r rune) rune {
			if r >= 0x064B && r <= 0x0652 {
				return -1
			}
			return r
		}, canonical), Pos: pos, Canonical: canonical, Senses: []lexicon.Sense{{Gloss: gloss, MSA: true}}}}
	}
	records := []rank.Record{
		{Rank: 1, ID: "كِتَاب", Entries: entry("كِتَاب", "noun", "book")},
		{Rank: 3, ID: "عَيْن", Entries: entry("عَيْن", "noun", "eye (organ)")},
	}
	if err := notes.WriteJSONL(f.paths.Lexicon(), records); err != nil {
		t.Fatal(err)
	}
	if len(existing) > 0 {
		if err := notes.WriteJSONL(f.paths.Notes(), existing); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) dump(lines ...string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.paths.Kaikki()), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.Kaikki(), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) add(stdin string, args ...string) (string, error) {
	f.t.Helper()
	root := newRootCommand()
	var out, errs bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"add", "-q", "--deck-dir", f.paths.Deck, "--cache", f.paths.Cache, "--claude", f.exe, "--concurrency", "1"}, args...))
	err := root.Execute()
	return errs.String(), err
}

func (f *fixture) notes() []notes.Note {
	f.t.Helper()
	ns, err := notes.ReadJSONL[notes.Note](f.paths.Notes())
	if err != nil {
		f.t.Fatal(err)
	}
	return ns
}

func (f *fixture) prompts() string {
	return f.read(f.log)
}

func (f *fixture) args() string {
	return f.read(f.log + ".args")
}

func (f *fixture) read(path string) string {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return string(raw)
}

func (f *fixture) byID(id string) notes.Note {
	f.t.Helper()
	for _, n := range f.notes() {
		if n.ID == id {
			return n
		}
	}
	f.t.Fatalf("no note %q in %+v", id, f.notes())
	return notes.Note{}
}

func TestAddAsksWhichSenseAndWritesTheChosenOne(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	stderr, err := f.add("2\n", "عين")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"عين has 2 senses; which one do you mean?", "1  عَيْن  noun  eye (organ)", "2  عَيَّنَ  verb, form II  to appoint", "choose 1-2"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the question lacks %q:\n%s", want, stderr)
		}
	}
	ns := f.notes()
	if len(ns) != 1 {
		t.Fatalf("notes = %+v", ns)
	}
	n := ns[0]
	if n.ID != "عَيَّنَ" || n.Position != deck.UnrankedBase+1 || n.Pos != "verb" || n.VerbForm != "II" || !n.Authored() || n.English != "stub verb" {
		t.Errorf("note = %+v", n)
	}
	if len(n.Forms) != 2 || n.Forms[0].Label != "pres." || n.Forms[0].Arabic != "يُعَيِّنُ" || n.Forms[1].Arabic != "تَعْيِين" {
		t.Errorf("the draft's forms should come from Wiktionary like any other word's: %+v", n.Forms)
	}
	prompt := f.prompts()
	if !strings.Contains(prompt, "asked for this entry in particular (verb, عَيَّنَ)") || !strings.Contains(prompt, `"gloss":"to appoint"`) {
		t.Errorf("Claude was not told which entry to write:\n%s", prompt)
	}
	if strings.Contains(prompt, "eye (organ)") {
		t.Errorf("the other sense must not be offered to Claude:\n%s", prompt)
	}
	if args := f.args(); !strings.Contains(args, "You write flashcards") || !strings.Contains(args, "the learner asked for a particular entry") {
		t.Errorf("Claude should get the usual guide, including the rule about chosen entries:\n%s", args)
	}
}

func TestAddWithYesTakesTheLikeliestSenseAndKeepsItsRank(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	stderr, err := f.add("", "--yes", "عين")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "which one") {
		t.Errorf("--yes must not ask:\n%s", stderr)
	}
	n := f.byID("عَيْن")
	if n.Position != 3 || n.Pos != "noun" || n.English != "stub noun" {
		t.Errorf("note = %+v", n)
	}
	if len(f.notes()) != 1 {
		t.Errorf("notes = %+v", f.notes())
	}
}

func TestAddDoesNotAskWhenOnlyOneSenseFits(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpBook)
	stderr, err := f.add("", "كتاب")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "which one") {
		t.Errorf("a single match needs no question:\n%s", stderr)
	}
	if n := f.byID("كِتَاب"); n.Position != 1 || !n.Authored() {
		t.Errorf("note = %+v", n)
	}
}

func TestAddCanTakeSeveralSensesOfOneSpelling(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	if _, err := f.add("a\n", "عين"); err != nil {
		t.Fatal(err)
	}
	ns := f.notes()
	if len(ns) != 2 || ns[0].ID != "عَيْن" || ns[0].Position != 3 || ns[1].ID != "عَيَّنَ" || ns[1].Position != deck.UnrankedBase+1 {
		t.Fatalf("notes = %+v", ns)
	}
}

func TestAddReasksOnABadAnswer(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	stderr, err := f.add("7\nzzz\n1\n", "عين")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, `"7" is not a number from 1 to 2`) || !strings.Contains(stderr, `"zzz" is not a number`) {
		t.Errorf("stderr:\n%s", stderr)
	}
	if len(f.notes()) != 1 || f.notes()[0].ID != "عَيْن" {
		t.Errorf("notes = %+v", f.notes())
	}
}

func TestAddQuitAndSkipAddNothing(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	for _, answer := range []string{"q\n", "s\n"} {
		if _, err := f.add(answer, "عين"); err != nil {
			t.Fatalf("answer %q: %v", answer, err)
		}
		if len(f.notes()) != 0 || f.prompts() != "" {
			t.Errorf("answer %q: notes %+v, claude was asked: %v", answer, f.notes(), f.prompts() != "")
		}
	}
}

func TestAddFailsWhenNobodyCanAnswer(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	_, err := f.add("", "عين")
	if !errors.Is(err, errNoAnswer) {
		t.Fatalf("err = %v", err)
	}
	if len(f.notes()) != 0 {
		t.Errorf("notes = %+v", f.notes())
	}
}

func TestAddReadsAListOfWordsFromAFile(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint, dumpDog)
	list := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(list, []byte("# new words\nكلب\nعين\nضضضضض\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, err := f.add("1\n", "--file", list)
	if err == nil || !strings.Contains(err.Error(), "1 word could not be found and was not added: ضضضضض") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stderr, "ضضضضض: no Modern Standard Arabic entry in Wiktionary") {
		t.Errorf("stderr:\n%s", stderr)
	}
	ns := f.notes()
	if len(ns) != 2 || ns[0].ID != "عَيْن" || ns[0].Position != 3 || ns[1].ID != "كَلْب" || ns[1].Position != deck.UnrankedBase+1 {
		t.Fatalf("the found words are still added: %+v", ns)
	}
	if strings.Count(f.prompts(), "## Position") != 2 {
		t.Errorf("both words belong in one request:\n%s", f.prompts())
	}
}

func TestAddWithoutTheDumpOffersTheDownloadAndFallsBackToTheRanking(t *testing.T) {
	f := newFixture(t)
	stderr, err := f.add("n\n", "كتاب", "كلب")
	if err == nil || !strings.Contains(err.Error(), "1 word could not be found and was not added: كلب") {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"Wiktionary's Arabic dump", "about 500 MB", "download it now? [y/N]", "using the ranked list only", "كلب: not in the ranked list, and the Wiktionary dump was not downloaded"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if _, statErr := os.Stat(f.paths.Kaikki()); statErr == nil {
		t.Error("nothing should be downloaded when the answer is no")
	}
	if ns := f.notes(); len(ns) != 1 || ns[0].ID != "كِتَاب" {
		t.Errorf("the ranked word should still be added: %+v", ns)
	}
}

func TestAddReportsWordsAlreadyInTheDeck(t *testing.T) {
	existing := notes.Note{ID: "كِتَاب", Position: 1, Arabic: "كِتَاب", Pos: "noun", English: "book", Example: "<b>كِتَابٌ</b>.", ExampleEn: "A book."}
	f := newFixture(t, existing)
	f.dump(dumpBook)
	if _, err := f.add("", "--yes", "كتاب"); err != nil {
		t.Fatal(err)
	}
	if f.prompts() != "" || len(f.notes()) != 1 {
		t.Errorf("a word that is in the deck needs no work: notes %+v", f.notes())
	}
}

func TestAddWithoutWordsStillAddsTheNextRankedWords(t *testing.T) {
	f := newFixture(t)
	if _, err := f.add("", "-n", "2"); err != nil {
		t.Fatal(err)
	}
	ns := f.notes()
	if len(ns) != 2 || ns[0].ID != "كِتَاب" || ns[1].ID != "عَيْن" || !ns[0].Authored() || !ns[1].Authored() {
		t.Fatalf("notes = %+v", ns)
	}
	if strings.Contains(f.prompts(), "asked for this entry") {
		t.Errorf("ranked words are not tied to an entry:\n%s", f.prompts())
	}
}

func TestAddRejectsBadArguments(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpBook)
	comments := filepath.Join(t.TempDir(), "comments.txt")
	if err := os.WriteFile(comments, []byte("# nothing here\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"count with words":   {"-n", "5", "كتاب"},
		"not arabic":         {"book"},
		"missing file":       {"--file", filepath.Join(t.TempDir(), "none.txt")},
		"file without words": {"--file", comments},
	}
	for name, args := range cases {
		if _, err := f.add("", args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	var usage usageError
	for _, name := range []string{"count with words", "not arabic", "file without words"} {
		if _, err := f.add("", cases[name]...); !errors.As(err, &usage) {
			t.Errorf("%s should be a usage error: %v", name, err)
		}
	}
	if len(f.notes()) != 0 || f.prompts() != "" {
		t.Errorf("nothing should happen: %+v", f.notes())
	}
}
