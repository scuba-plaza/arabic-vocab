package deck

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func TestNextWordsFinishesLeftoversBeforeNewWords(t *testing.T) {
	entries := func(w string) []*lexicon.Entry { return []*lexicon.Entry{{Title: w, Pos: "noun", Canonical: w}} }
	records := []rank.Record{
		{Rank: 3, ID: "c", Entries: entries("c")},
		{Rank: 1, ID: "a", Entries: entries("a")},
		{Rank: 2, ID: "b", Entries: entries("b")},
		{Rank: 4, ID: "d"},
		{Rank: 5, ID: "e", Entries: entries("e")},
		{Rank: 6, ID: "f", Entries: entries("f")},
	}
	existing := []notes.Note{
		{ID: "a", Position: 1, English: "a", Example: "a", ExampleEn: "a"},
		{ID: "x", Position: 3},
	}
	ns, targets := NextWords(existing, records, 3)
	var got []string
	for _, i := range targets {
		got = append(got, ns[i].ID)
	}
	if !slices.Equal(got, []string{"b", "x", "e"}) {
		t.Fatalf("targets = %v", got)
	}
	if len(ns) != 4 || ns[1].ID != "b" || ns[1].Position != 2 || ns[3].Position != 5 {
		t.Fatalf("notes = %+v", ns)
	}
	if _, targets := NextWords(ns, records, 0); len(targets) != 0 {
		t.Errorf("n = 0 should add nothing, got %v", targets)
	}
}

func TestSettingsDefaultAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deck.json")
	s, err := LoadSettings(path)
	if err != nil || s != DefaultSettings() {
		t.Fatalf("missing file: %+v, %v", s, err)
	}
	want := Settings{Voice: "ar-XA-Chirp3-HD-Achernar", Rate: 0.85}
	if err := SaveSettings(path, want); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadSettings(path); err != nil || s != want {
		t.Fatalf("round trip: %+v, %v", s, err)
	}
	os.WriteFile(path, []byte(`{"voice": "x", "speed": 2}`), 0o644)
	if _, err := LoadSettings(path); err == nil {
		t.Error("an unknown setting should be an error")
	}
}
