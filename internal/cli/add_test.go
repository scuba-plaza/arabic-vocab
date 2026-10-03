package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
	earlier := 0
	if raw, err := os.ReadFile(log); err == nil {
		earlier = strings.Count(string(raw), "\n=====\n")
	}
	for path, text := range map[string]string{log: string(prompt), log + ".args": strings.Join(os.Args[1:], "\n")} {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.WriteString(text + "\n=====\n")
			f.Close()
		}
	}
	if os.Getenv("FAKE_CLAUDE_DIE") != "" {
		fmt.Fprintln(os.Stderr, "usage limit reached")
		os.Exit(1)
	}
	skip := strings.Split(os.Getenv("FAKE_CLAUDE_SKIP"), ",")
	switchPos := os.Getenv("FAKE_CLAUDE_SWITCH")
	switchCalls, err := strconv.Atoi(os.Getenv("FAKE_CLAUDE_SWITCH_CALLS"))
	if err != nil {
		switchCalls = 1 << 30
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
		if slices.Contains(skip, c.Arabic) {
			continue
		}
		if switchPos != "" && earlier < switchCalls {
			c.Pos = switchPos
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
	t      *testing.T
	paths  *deck.Paths
	exe    string
	log    string
	loud   bool
	stdout string
	info   string
}

func lexEntries(canonical, pos, gloss string) []*lexicon.Entry {
	return []*lexicon.Entry{{Title: strings.Map(func(r rune) rune {
		if r >= 0x064B && r <= 0x0652 {
			return -1
		}
		return r
	}, canonical), Pos: pos, Canonical: canonical, Senses: []lexicon.Sense{{Gloss: gloss, MSA: true}}}}
}

func serveDump(t *testing.T, status int, lines ...string) *atomic.Int32 {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if status != http.StatusOK {
			http.Error(w, "the dump is down", status)
			return
		}
		io.WriteString(w, strings.Join(lines, "\n")+"\n")
	}))
	t.Cleanup(srv.Close)
	local := kaikki
	kaikki = func(p deck.Paths) deck.Source {
		src := local(p)
		src.Name, src.URL = "Test dump", srv.URL+"/dump.jsonl"
		return src
	}
	return hits
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
	local := kaikki
	kaikki = func(p deck.Paths) deck.Source {
		src := local(p)
		src.URL = "http://127.0.0.1:1/unreachable"
		return src
	}
	t.Cleanup(func() { kaikki = local })
	records := []rank.Record{
		{Rank: 1, ID: "كِتَاب", Entries: lexEntries("كِتَاب", "noun", "book")},
		{Rank: 3, ID: "عَيْن", Entries: lexEntries("عَيْن", "noun", "eye (organ)")},
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

func (f *fixture) rank(extra ...rank.Record) {
	f.t.Helper()
	records, err := notes.ReadJSONL[rank.Record](f.paths.Lexicon())
	if err != nil {
		f.t.Fatal(err)
	}
	if err := notes.WriteJSONL(f.paths.Lexicon(), append(records, extra...)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) add(stdin string, args ...string) (string, error) {
	f.t.Helper()
	return f.addFrom(strings.NewReader(stdin), args...)
}

func (f *fixture) addFrom(stdin io.Reader, args ...string) (string, error) {
	f.t.Helper()
	root := newRootCommand()
	var out, errs bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetIn(stdin)
	base := []string{"add", "--deck-dir", f.paths.Deck, "--cache", f.paths.Cache, "--claude", f.exe, "--concurrency", "1"}
	if !f.loud {
		base = append(base, "-q")
	}
	root.SetArgs(append(base, args...))
	var info bytes.Buffer
	if f.loud {
		saved := progress
		progress = &info
		defer func() { progress = saved }()
	}
	err := root.Execute()
	f.stdout, f.info = out.String(), info.String()
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

const (
	dumpUniversity = `{"word":"جامعة","pos":"noun","forms":[{"form":"الجَامِعَة","tags":["canonical","feminine"]}],"senses":[{"glosses":["university"]}]}`
	dumpOdd        = `{"word":"عفج","pos":"noun","forms":[{"form":"مَعْفُوج","tags":["canonical","masculine"]}],"senses":[{"glosses":["odd"]}]}`
)

func marks(codes ...rune) string {
	return string(codes)
}

func written(id string, position int, arabic, pos string) notes.Note {
	return notes.Note{ID: id, Position: position, Arabic: arabic, Pos: pos, English: "x", Example: "<b>كِتَابٌ</b>.", ExampleEn: "A book."}
}

func TestAddNeverLetsASecondarySenseTakeARankedWordsID(t *testing.T) {
	f := newFixture(t)
	f.rank(rank.Record{Rank: 7, ID: "مَا", Entries: append(lexEntries("مَا", "pron", "what"), lexEntries("مَا", "adv", "not")...)})
	f.dump(dumpBook)
	if _, err := f.add("2\n", "ما"); err != nil {
		t.Fatal(err)
	}
	ns := f.notes()
	if len(ns) != 1 || ns[0].ID != "مَا (adv)" || ns[0].Pos != "adv" || ns[0].Position != deck.UnrankedBase+1 {
		t.Fatalf("notes = %+v", ns)
	}
	if _, err := f.add("", "-n", "3"); err != nil {
		t.Fatal(err)
	}
	main := f.byID("مَا")
	if main.Position != 7 || main.Pos != "pron" || !main.Authored() {
		t.Errorf("the main sense must still be added later with its rank: %+v", main)
	}
	if len(f.notes()) != 4 {
		t.Errorf("notes = %+v", f.notes())
	}
}

func TestAddNamesTheCommandThatWritesTheWordsItCouldNotWrite(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpBook)
	t.Setenv("FAKE_CLAUDE_SKIP", "عَيْن")
	_, err := f.add("", "كتاب", "عين")
	if err == nil || !strings.Contains(err.Error(), "1 word could not be written; they are listed above, and running 'arabic-vocab add عين' tries them again") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.stdout, "عَيْن") {
		t.Errorf("the failed word should be listed:\n%s", f.stdout)
	}
	if ns := f.notes(); len(ns) != 1 || ns[0].ID != "كِتَاب" {
		t.Fatalf("only the finished word is saved: %+v", ns)
	}

	t.Setenv("FAKE_CLAUDE_SKIP", "")
	if _, err := f.add("", "عين"); err != nil {
		t.Fatalf("the printed command should work: %v", err)
	}
	if ns := f.notes(); len(ns) != 2 || !ns[1].Authored() {
		t.Errorf("notes = %+v", ns)
	}
}

func TestAddNamesTheCommandWhenClaudeStopsEarly(t *testing.T) {
	f := newFixture(t)
	f.loud = true
	f.dump(dumpBook)
	t.Setenv("FAKE_CLAUDE_DIE", "1")
	_, err := f.add("", "كتاب", "عين")
	if err == nil || !strings.Contains(err.Error(), "usage limit reached") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.info, "to write the rest, run: arabic-vocab add كتاب عين") {
		t.Errorf("named words are never saved before they are written, so the hint must list them:\n%s", f.info)
	}
	if len(f.notes()) != 0 {
		t.Errorf("notes = %+v", f.notes())
	}
}

func TestResumeCommandListsTheUnwrittenWordsOnce(t *testing.T) {
	ns := []notes.Note{
		written("w", 1, "عَيْن", "noun"),
		{ID: "a", Position: 2}, {ID: "b", Position: 3}, {ID: "c", Position: 4},
	}
	job := addJob{notes: ns, targets: []int{0, 1, 2, 3}, typed: map[string]string{"w": "عين", "a": "كتاب", "b": "كيف حالك", "c": "كتاب"}}
	if got := job.resume(); got != `arabic-vocab add كتاب "كيف حالك"` {
		t.Errorf("resume = %q", got)
	}
	if got := (addJob{notes: ns, targets: []int{1}}).resume(); got != "arabic-vocab add" {
		t.Errorf("ranked words resume with plain add, got %q", got)
	}
}

type editingReader struct {
	r    io.Reader
	edit func()
	done bool
}

func (e *editingReader) Read(p []byte) (int, error) {
	if !e.done {
		e.done = true
		e.edit()
	}
	return e.r.Read(p)
}

func TestAddKeepsEditsMadeToTheDeckWhileItWaitsForAnAnswer(t *testing.T) {
	book := written("كِتَاب", 1, "كِتَاب", "noun")
	f := newFixture(t, book)
	f.dump(dumpEye, dumpAppoint)
	in := &editingReader{r: strings.NewReader("1\n"), edit: func() {
		ns := f.notes()
		ns[0].English = "volume"
		ns = append(ns, written("قَلَم", 2, "قَلَم", "noun"))
		if err := notes.WriteJSONL(f.paths.Notes(), ns); err != nil {
			t.Error(err)
		}
	}}
	if _, err := f.addFrom(in, "عين"); err != nil {
		t.Fatal(err)
	}
	if got := f.byID("كِتَاب").English; got != "volume" {
		t.Errorf("an edit made while add was waiting was overwritten: English = %q", got)
	}
	f.byID("قَلَم")
	if eye := f.byID("عَيْن"); !eye.Authored() || eye.Position != 3 {
		t.Errorf("eye = %+v", eye)
	}
	if len(f.notes()) != 3 {
		t.Errorf("notes = %+v", f.notes())
	}
}

func TestAddRefusesToSaveOverANoteAddedMeanwhileAtTheSamePosition(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint)
	in := &editingReader{r: strings.NewReader("1\n"), edit: func() {
		if err := notes.WriteJSONL(f.paths.Notes(), []notes.Note{written("قَلَم", 3, "قَلَم", "noun")}); err != nil {
			t.Error(err)
		}
	}}
	_, err := f.addFrom(in, "عين")
	if err == nil || !strings.Contains(err.Error(), "share position 3") {
		t.Fatalf("err = %v", err)
	}
	if ns := f.notes(); len(ns) != 1 || ns[0].ID != "قَلَم" {
		t.Errorf("the note that was added meanwhile must survive: %+v", ns)
	}
}

func TestAddRejectsACardThatSwitchesTheEntry(t *testing.T) {
	t.Run("a model that keeps switching", func(t *testing.T) {
		f := newFixture(t)
		f.dump(dumpEye, dumpAppoint)
		t.Setenv("FAKE_CLAUDE_SWITCH", "verb")
		_, err := f.add("1\n", "عين")
		if err == nil || !strings.Contains(err.Error(), "1 word could not be written") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(f.stdout, "the learner asked for the noun عَيْن, but the card is for a verb") {
			t.Errorf("the failure should say why:\n%s", f.stdout)
		}
		if len(f.notes()) != 0 {
			t.Errorf("a card for another entry must not be saved: %+v", f.notes())
		}
		if got := strings.Count(f.prompts(), "## Position"); got != 2 {
			t.Errorf("the draft should be asked for twice, got %d", got)
		}
	})
	t.Run("a model that corrects itself", func(t *testing.T) {
		f := newFixture(t)
		f.dump(dumpEye, dumpAppoint)
		t.Setenv("FAKE_CLAUDE_SWITCH", "verb")
		t.Setenv("FAKE_CLAUDE_SWITCH_CALLS", "1")
		if _, err := f.add("1\n", "عين"); err != nil {
			t.Fatal(err)
		}
		if n := f.byID("عَيْن"); n.Pos != "noun" || n.English != "stub noun" {
			t.Errorf("note = %+v", n)
		}
		prompts := f.prompts()
		if !strings.Contains(prompts, "A previous answer for this card was rejected: the learner asked for the noun عَيْن, but the card is for a verb") {
			t.Errorf("the second request must say what was wrong:\n%s", prompts)
		}
	})
	t.Run("ranked words may still be relabelled", func(t *testing.T) {
		f := newFixture(t)
		t.Setenv("FAKE_CLAUDE_SWITCH", "particle")
		if _, err := f.add("", "-n", "1"); err != nil {
			t.Fatal(err)
		}
		if n := f.byID("كِتَاب"); n.Pos != "particle" {
			t.Errorf("curation may fix the part of speech of a ranked word: %+v", n)
		}
	})
}

func TestAddRecognisesAWordWhoseNoteWasRelabelledOrRenamed(t *testing.T) {
	t.Run("part of speech changed", func(t *testing.T) {
		f := newFixture(t, written("عَيْن", 3, "عَيْن", "adj"))
		if _, err := f.add("", "--yes", "عين"); err != nil {
			t.Fatal(err)
		}
		if f.prompts() != "" || len(f.notes()) != 1 {
			t.Errorf("a second note was added: %+v", f.notes())
		}
	})
	t.Run("headword changed", func(t *testing.T) {
		f := newFixture(t, written("يُمْكِنُ", 50, "يُمْكِنُ", "verb"))
		f.dump(dumpBook)
		if _, err := f.add("", "يمكن"); err != nil {
			t.Fatalf("a word the deck has is not an error: %v", err)
		}
		if f.prompts() != "" || len(f.notes()) != 1 {
			t.Errorf("notes = %+v", f.notes())
		}
	})
}

func TestAddFindsEntriesWhoseTitleAndCanonicalFormDiffer(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpUniversity, dumpOdd)
	if _, err := f.add("", "جامعة", "معفوج"); err != nil {
		t.Fatal(err)
	}
	ns := f.notes()
	if len(ns) != 2 || ns[0].ID != "الجَامِعَة" || ns[0].Position != deck.UnrankedBase+1 || ns[1].ID != "مَعْفُوج" || ns[1].Position != deck.UnrankedBase+2 {
		t.Fatalf("notes = %+v", ns)
	}
}

func TestAddIgnoresInvisibleCharactersInWords(t *testing.T) {
	f := newFixture(t)
	f.dump(dumpEye, dumpAppoint, dumpBook)
	list := filepath.Join(t.TempDir(), "words.txt")
	body := marks(0xFEFF) + "كتاب\r\n" + marks(0x200F) + "عين" + marks(0x200E) + "\n"
	if err := os.WriteFile(list, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.add("1\n", "--file", list); err != nil {
		t.Fatal(err)
	}
	if ns := f.notes(); len(ns) != 2 || ns[0].ID != "كِتَاب" || ns[1].ID != "عَيْن" {
		t.Errorf("notes = %+v", ns)
	}
}

func TestAddRefusesBlankWordsInsteadOfAddingTheNextRankedOnes(t *testing.T) {
	f := newFixture(t)
	for _, blank := range []string{"", " ", ",", "،", marks(0xFEFF), marks(0x200F, 0x200E)} {
		_, err := f.add("", blank)
		var usage usageError
		if !errors.As(err, &usage) || !strings.Contains(err.Error(), "the words to add are empty") {
			t.Errorf("add %q: err = %v", blank, err)
		}
	}
	if f.prompts() != "" || len(f.notes()) != 0 {
		t.Errorf("nothing should have been added: %+v", f.notes())
	}
	f.dump(dumpBook)
	if _, err := f.add("", "", "كتاب"); err != nil {
		t.Fatalf("a blank next to a word is skipped: %v", err)
	}
	if ns := f.notes(); len(ns) != 1 || ns[0].ID != "كِتَاب" {
		t.Errorf("notes = %+v", ns)
	}
}

func TestAddWithYesDownloadsTheDumpOnlyForWordsTheRankingLacks(t *testing.T) {
	t.Run("ranked words need no download", func(t *testing.T) {
		f := newFixture(t)
		hits := serveDump(t, http.StatusOK, dumpDog)
		if _, err := f.add("", "--yes", "كتاب", "عين"); err != nil {
			t.Fatal(err)
		}
		if hits.Load() != 0 {
			t.Errorf("the dump was requested %d times", hits.Load())
		}
		if _, err := os.Stat(f.paths.Kaikki()); err == nil {
			t.Error("the dump should not have been fetched")
		}
		if len(f.notes()) != 2 {
			t.Errorf("notes = %+v", f.notes())
		}
	})
	t.Run("a dump on disk is not read either", func(t *testing.T) {
		f := newFixture(t)
		f.dump("this is not json")
		if _, err := f.add("", "--yes", "كتاب"); err != nil {
			t.Fatalf("the dump should not have been read: %v", err)
		}
	})
	t.Run("a missing word triggers one download", func(t *testing.T) {
		f := newFixture(t)
		hits := serveDump(t, http.StatusOK, dumpDog, dumpBook)
		if _, err := f.add("", "--yes", "كتاب", "كلب"); err != nil {
			t.Fatal(err)
		}
		if hits.Load() != 1 {
			t.Errorf("the dump was requested %d times", hits.Load())
		}
		ns := f.notes()
		if len(ns) != 2 || ns[0].ID != "كِتَاب" || ns[1].ID != "كَلْب" || ns[1].Position != deck.UnrankedBase+1 {
			t.Errorf("notes = %+v", ns)
		}
	})
	t.Run("a failed download leaves the ranked words and reports the others", func(t *testing.T) {
		f := newFixture(t)
		hits := serveDump(t, http.StatusInternalServerError)
		stderr, err := f.add("", "--yes", "كتاب", "كلب")
		if err == nil || !strings.Contains(err.Error(), "1 word could not be found and was not added: كلب") {
			t.Fatalf("err = %v", err)
		}
		for _, want := range []string{"500", "using the ranked list only", "كلب: not in the ranked list, and the Wiktionary dump could not be downloaded"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
		}
		if hits.Load() != 1 {
			t.Errorf("the dump was requested %d times", hits.Load())
		}
		if _, err := os.Stat(f.paths.Kaikki()); err == nil {
			t.Error("a failed download must not leave a dump behind")
		}
		if ns := f.notes(); len(ns) != 1 || ns[0].ID != "كِتَاب" {
			t.Errorf("notes = %+v", ns)
		}
	})
}

func TestAddDownloadsTheDumpWhenTheUserAgrees(t *testing.T) {
	f := newFixture(t)
	hits := serveDump(t, http.StatusOK, dumpDog)
	stderr, err := f.add("y\n", "كلب")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "download it now? [y/N]") || hits.Load() != 1 {
		t.Errorf("hits %d, stderr:\n%s", hits.Load(), stderr)
	}
	if ns := f.notes(); len(ns) != 1 || ns[0].ID != "كَلْب" {
		t.Errorf("notes = %+v", ns)
	}
}
