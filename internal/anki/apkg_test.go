package anki

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPackage(t *testing.T) (*Package, string) {
	t.Helper()
	dir := t.TempDir()
	media := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(media, []byte("ID3"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Package{
		Deck: Deck{ID: 42, Name: "Arabic::Test"},
		Model: Model{
			ID: 7, Name: "Word", Fields: []string{"Front", "Back"},
			Templates: []Template{{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"}},
			Required:  [][]int{{0}},
		},
		Notes: []Note{
			{GUID: GUID("a"), Fields: []string{"كِتَاب", "book"}, Tags: []string{"pos::noun"}, Cards: []Card{{Ord: 0, Due: 1}}},
			{GUID: GUID("b"), Fields: []string{"<b>قَلَم</b>", "pen"}, Cards: []Card{{Ord: 0, Due: 2}}},
		},
		Media: map[string]string{"a.mp3": media},
		Now:   time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC),
	}, dir
}

func TestWriteProducesAnImportableCollection(t *testing.T) {
	pkg, dir := testPackage(t)
	out := filepath.Join(dir, "deck.apkg")
	if err := pkg.Write(out); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	for _, name := range []string{"collection.anki2", "media", "0"} {
		if files[name] == nil {
			t.Fatalf("package is missing %s", name)
		}
	}
	rc, _ := files["media"].Open()
	var mapping map[string]string
	if err := json.NewDecoder(rc).Decode(&mapping); err != nil || mapping["0"] != "a.mp3" {
		t.Errorf("media mapping = %v, %v", mapping, err)
	}
	rc.Close()

	db := extractDB(t, files["collection.anki2"], dir)
	defer db.Close()
	var notes, cards int
	db.QueryRow("SELECT count(*) FROM notes").Scan(&notes)
	db.QueryRow("SELECT count(*) FROM cards").Scan(&cards)
	if notes != 2 || cards != 2 {
		t.Errorf("notes=%d cards=%d", notes, cards)
	}
	var flds, sfld, tags string
	db.QueryRow("SELECT flds, sfld, tags FROM notes WHERE guid = ?", GUID("b")).Scan(&flds, &sfld, &tags)
	if flds != "<b>قَلَم</b>\x1fpen" || sfld != "قَلَم" {
		t.Errorf("flds=%q sfld=%q", flds, sfld)
	}
	var models, decks string
	db.QueryRow("SELECT models, decks FROM col").Scan(&models, &decks)
	if !strings.Contains(models, `"name":"Word"`) || !strings.Contains(decks, `"name":"Arabic::Test"`) {
		t.Errorf("collection header lacks the model or deck: %s / %s", models, decks)
	}
	var due, did int64
	db.QueryRow("SELECT due, did FROM cards WHERE nid = ?", StableID("note", GUID("b"))).Scan(&due, &did)
	if due != 2 || did != 42 {
		t.Errorf("card due=%d did=%d", due, did)
	}
}

func extractDB(t *testing.T, f *zip.File, dir string) *sql.DB {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	path := filepath.Join(dir, "extracted.anki2")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, rc); err != nil {
		t.Fatal(err)
	}
	out.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestIDsAreStable(t *testing.T) {
	if GUID("كِتَاب") != GUID("كِتَاب") || GUID("كِتَاب") == GUID("قَلَم") {
		t.Error("GUIDs must be deterministic and distinct")
	}
	id := StableID("note", "x")
	if id != StableID("note", "x") || id < 1_700_000_000_000 {
		t.Errorf("unexpected note id %d", id)
	}
}

func TestWriteRejectsFieldCountMismatch(t *testing.T) {
	pkg, dir := testPackage(t)
	pkg.Notes[0].Fields = []string{"only one"}
	if err := pkg.Write(filepath.Join(dir, "bad.apkg")); err == nil {
		t.Error("a note with the wrong number of fields should fail")
	}
}
