package anki

import (
	"archive/zip"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Template struct {
	Name  string
	Front string
	Back  string
}

type Model struct {
	ID        int64
	Name      string
	Fields    []string
	RTL       []string
	Templates []Template
	CSS       string
	Required  [][]int
}

type Deck struct {
	ID          int64
	Name        string
	Description string
}

type Card struct {
	Ord int
	Due int
}

type Note struct {
	GUID   string
	Fields []string
	Tags   []string
	Cards  []Card
}

type Package struct {
	Deck  Deck
	Model Model
	Notes []Note
	Media map[string]string
	Now   time.Time
}

const schema = `
CREATE TABLE col (id integer primary key, crt integer not null, mod integer not null, scm integer not null, ver integer not null, dty integer not null, usn integer not null, ls integer not null, conf text not null, models text not null, decks text not null, dconf text not null, tags text not null);
CREATE TABLE notes (id integer primary key, guid text not null, mid integer not null, mod integer not null, usn integer not null, tags text not null, flds text not null, sfld integer not null, csum integer not null, flags integer not null, data text not null);
CREATE TABLE cards (id integer primary key, nid integer not null, did integer not null, ord integer not null, mod integer not null, usn integer not null, type integer not null, queue integer not null, due integer not null, ivl integer not null, factor integer not null, reps integer not null, lapses integer not null, left integer not null, odue integer not null, odid integer not null, flags integer not null, data text not null);
CREATE TABLE revlog (id integer primary key, cid integer not null, usn integer not null, ivl integer not null, lastIvl integer not null, factor integer not null, time integer not null, type integer not null);
CREATE TABLE graves (usn integer not null, oid integer not null, type integer not null);
CREATE INDEX ix_notes_usn on notes (usn);
CREATE INDEX ix_cards_usn on cards (usn);
CREATE INDEX ix_revlog_usn on revlog (usn);
CREATE INDEX ix_cards_nid on cards (nid);
CREATE INDEX ix_cards_sched on cards (did, queue, due);
CREATE INDEX ix_revlog_cid on revlog (cid);
CREATE INDEX ix_notes_csum on notes (csum);
`

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func StripHTML(s string) string {
	return strings.TrimSpace(htmlTag.ReplaceAllString(s, ""))
}

func checksum(field string) int64 {
	sum := sha1.Sum([]byte(StripHTML(field)))
	v, _ := strconv.ParseInt(hex.EncodeToString(sum[:4]), 16, 64)
	return v
}

func StableID(parts ...string) int64 {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return 1_700_000_000_000 + int64(binary.BigEndian.Uint64(h[:8])%100_000_000_000)
}

func GUID(key string) string {
	h := sha256.Sum256([]byte("arabic-vocab:" + key))
	return hex.EncodeToString(h[:10])
}

func (p *Package) modelJSON(mod int64) map[string]any {
	var tmpls []map[string]any
	for i, t := range p.Model.Templates {
		tmpls = append(tmpls, map[string]any{
			"name": t.Name, "ord": i, "qfmt": t.Front, "afmt": t.Back,
			"did": nil, "bqfmt": "", "bafmt": "",
		})
	}
	var flds []map[string]any
	for i, f := range p.Model.Fields {
		flds = append(flds, map[string]any{
			"name": f, "ord": i, "sticky": false, "rtl": slices.Contains(p.Model.RTL, f),
			"font": "Arial", "size": 20, "media": []string{},
		})
	}
	var req []any
	for ord, fields := range p.Model.Required {
		req = append(req, []any{ord, "all", fields})
	}
	return map[string]any{
		"id": strconv.FormatInt(p.Model.ID, 10), "name": p.Model.Name, "type": 0, "mod": mod, "usn": -1,
		"sortf": 0, "did": p.Deck.ID, "tmpls": tmpls, "flds": flds, "css": p.Model.CSS,
		"latexPre":  "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		"latexPost": "\\end{document}", "latexsvg": false, "req": req, "tags": []string{}, "vers": []any{},
	}
}

func deckJSON(id int64, name, desc string, mod int64) map[string]any {
	return map[string]any{
		"id": id, "name": name, "mod": mod, "usn": -1, "desc": desc, "dyn": 0, "conf": 1,
		"collapsed": false, "browserCollapsed": true, "extendNew": 0, "extendRev": 0,
		"lrnToday": []int{0, 0}, "revToday": []int{0, 0}, "newToday": []int{0, 0}, "timeToday": []int{0, 0},
	}
}

var defaultDeckConfig = map[string]any{
	"1": map[string]any{
		"id": 1, "name": "Default", "mod": 0, "usn": 0, "maxTaken": 60, "autoplay": true, "timer": 0, "replayq": true,
		"new":   map[string]any{"bury": true, "delays": []float64{1, 10}, "initialFactor": 2500, "ints": []int{1, 4, 7}, "order": 1, "perDay": 20, "separate": true},
		"rev":   map[string]any{"bury": true, "ease4": 1.3, "fuzz": 0.05, "ivlFct": 1, "maxIvl": 36500, "minSpace": 1, "perDay": 200},
		"lapse": map[string]any{"delays": []float64{10}, "leechAction": 0, "leechFails": 8, "minInt": 1, "mult": 0},
	},
}

func marshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	return string(raw), err
}

func (p *Package) Write(path string) error {
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	dir, err := os.MkdirTemp("", "apkg-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "collection.anki2")
	if err := p.writeCollection(dbPath, now); err != nil {
		return err
	}
	return p.zip(path, dbPath)
}

func (p *Package) writeCollection(dbPath string, now time.Time) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("creating collection: %w", err)
	}
	sec, ms := now.Unix(), now.UnixMilli()
	models, err := marshal(map[string]any{strconv.FormatInt(p.Model.ID, 10): p.modelJSON(sec)})
	if err != nil {
		return err
	}
	decks, err := marshal(map[string]any{
		"1":                              deckJSON(1, "Default", "", sec),
		strconv.FormatInt(p.Deck.ID, 10): deckJSON(p.Deck.ID, p.Deck.Name, p.Deck.Description, sec),
	})
	if err != nil {
		return err
	}
	dconf, err := marshal(defaultDeckConfig)
	if err != nil {
		return err
	}
	conf, err := marshal(map[string]any{
		"activeDecks": []int64{1}, "curDeck": 1, "newSpread": 0, "collapseTime": 1200, "timeLim": 0,
		"estTimes": true, "dueCounts": true, "curModel": strconv.FormatInt(p.Model.ID, 10), "nextPos": 1,
		"sortType": "noteFld", "sortBackwards": false, "addToCur": true,
	})
	if err != nil {
		return err
	}
	crt := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, time.UTC).Unix()
	if _, err := db.Exec(`INSERT INTO col VALUES (1, ?, ?, ?, 11, 0, 0, 0, ?, ?, ?, ?, '{}')`,
		crt, ms, ms, conf, models, decks, dconf); err != nil {
		return fmt.Errorf("writing collection header: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	noteStmt, err := tx.Prepare(`INSERT INTO notes VALUES (?, ?, ?, ?, -1, ?, ?, ?, ?, 0, '')`)
	if err != nil {
		tx.Rollback()
		return err
	}
	cardStmt, err := tx.Prepare(`INSERT INTO cards VALUES (?, ?, ?, ?, ?, -1, 0, 0, ?, 0, 0, 0, 0, 0, 0, 0, 0, '')`)
	if err != nil {
		tx.Rollback()
		return err
	}
	seen := map[int64]string{}
	for _, n := range p.Notes {
		if len(n.Fields) != len(p.Model.Fields) {
			tx.Rollback()
			return fmt.Errorf("note %s has %d fields, the note type has %d", n.GUID, len(n.Fields), len(p.Model.Fields))
		}
		nid := StableID("note", n.GUID)
		if other, ok := seen[nid]; ok {
			tx.Rollback()
			return fmt.Errorf("notes %s and %s collide on id %d", other, n.GUID, nid)
		}
		seen[nid] = n.GUID
		tags := ""
		if len(n.Tags) > 0 {
			tags = " " + strings.Join(n.Tags, " ") + " "
		}
		if _, err := noteStmt.Exec(nid, n.GUID, p.Model.ID, sec, tags, strings.Join(n.Fields, "\x1f"), StripHTML(n.Fields[0]), checksum(n.Fields[0])); err != nil {
			tx.Rollback()
			return fmt.Errorf("writing note %s: %w", n.GUID, err)
		}
		for _, c := range n.Cards {
			if _, err := cardStmt.Exec(StableID("card", n.GUID, strconv.Itoa(c.Ord)), nid, p.Deck.ID, c.Ord, sec, c.Due); err != nil {
				tx.Rollback()
				return fmt.Errorf("writing card for %s: %w", n.GUID, err)
			}
		}
	}
	return tx.Commit()
}

func (p *Package) zip(path, dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	fail := func(err error) error {
		zw.Close()
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := addFile(zw, "collection.anki2", dbPath); err != nil {
		return fail(err)
	}
	names := make([]string, 0, len(p.Media))
	for name := range p.Media {
		names = append(names, name)
	}
	slices.Sort(names)
	mapping := map[string]string{}
	for i, name := range names {
		key := strconv.Itoa(i)
		mapping[key] = name
		if err := addFile(zw, key, p.Media[name]); err != nil {
			return fail(err)
		}
	}
	w, err := zw.Create("media")
	if err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(w).Encode(mapping); err != nil {
		return fail(err)
	}
	if err := zw.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func addFile(zw *zip.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
