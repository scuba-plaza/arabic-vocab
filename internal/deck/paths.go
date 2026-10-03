package deck

import "path/filepath"

type Paths struct {
	Deck  string
	Cache string
}

func (p Paths) Raw(name string) string       { return filepath.Join(p.Cache, "raw", name) }
func (p Paths) Kaikki() string               { return p.Raw("kaikki-ar.jsonl") }
func (p Paths) Subtitles() string            { return p.Raw("subtitles-ar-50k.txt") }
func (p Paths) MSA() string                  { return p.Raw("camel-msa-top.tsv") }
func (p Paths) Kelly() string                { return p.Raw("kelly-ar.json") }
func (p Paths) CamelLemmas() string          { return filepath.Join(p.Cache, "camel-lemmas.tsv") }
func (p Paths) Media() string                { return filepath.Join(p.Cache, "media") }
func (p Paths) RankReport() string           { return filepath.Join(p.Cache, "rank-report.tsv") }
func (p Paths) AudioQA() string              { return filepath.Join(p.Cache, "audio-qa.jsonl") }
func (p Paths) AudioLock() string            { return filepath.Join(p.Cache, "audio.lock") }
func (p Paths) Manifest() string             { return filepath.Join(p.Media(), "manifest.jsonl") }
func (p Paths) Essentials() string           { return filepath.Join(p.Deck, "essentials.tsv") }
func (p Paths) Overrides() string            { return filepath.Join(p.Deck, "overrides.tsv") }
func (p Paths) Ranked() string               { return filepath.Join(p.Deck, "ranked.tsv") }
func (p Paths) Lexicon() string              { return filepath.Join(p.Deck, "lexicon.jsonl") }
func (p Paths) Notes() string                { return filepath.Join(p.Deck, "notes.jsonl") }
func (p Paths) QA() string                   { return filepath.Join(p.Deck, "qa.jsonl") }
func (p Paths) Settings() string             { return filepath.Join(p.Deck, "deck.json") }
func (p Paths) Asset(name string) string     { return filepath.Join(p.Deck, "..", "assets", name) }
func (p Paths) CheckScript() string          { return filepath.Join("scripts", "check.py") }
func (p Paths) CamelLemmasScript() string    { return filepath.Join("scripts", "camel_lemmas.py") }
func (p Paths) Package(name string) string   { return filepath.Join("out", name+".apkg") }
func (p Paths) Voices() string               { return filepath.Join("out", "voices") }
func (p Paths) MediaFile(name string) string { return filepath.Join(p.Media(), name) }
