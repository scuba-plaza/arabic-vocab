package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func TestStatusNamesTheNextStep(t *testing.T) {
	dir := t.TempDir()
	paths := &deck.Paths{Deck: filepath.Join(dir, "deck"), Cache: filepath.Join(dir, "cache")}
	n := notes.Note{ID: "كِتَاب", Position: 1, Arabic: "كِتَاب", Pos: "noun", English: "book", Example: "هٰذَا <b>كِتَابٌ</b>.", ExampleEn: "This is a book."}
	next := func() string {
		t.Helper()
		s, err := readStatus(paths)
		if err != nil {
			t.Fatal(err)
		}
		return s.next()
	}
	if got := next(); got != "add" {
		t.Errorf("empty deck: next = %q, want add", got)
	}
	if err := notes.WriteJSONL(paths.Notes(), []notes.Note{n}); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "check" {
		t.Errorf("unchecked note: next = %q, want check", got)
	}
	major := notes.Check{ID: n.ID, Version: deck.CheckVersion, Digest: n.Digest(), Issues: []notes.Issue{{Field: "example", Kind: "diacritics", Severity: notes.Major, Word: "كِتَابٌ"}}}
	if err := notes.WriteJSONL(paths.QA(), []notes.Check{major}); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "review" {
		t.Errorf("major flag: next = %q, want review", got)
	}
	n.Reviewed = []string{"كِتَابٌ"}
	if err := notes.WriteJSONL(paths.Notes(), []notes.Note{n}); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "audio" {
		t.Errorf("reviewed flag: next = %q, want audio", got)
	}
	s, _ := readStatus(paths)
	var out strings.Builder
	s.print(&out, paths)
	for _, want := range []string{"1 word", "→ audio", "2 of 2 clips missing", "next: arabic-vocab audio"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
}
