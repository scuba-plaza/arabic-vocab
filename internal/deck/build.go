package deck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/anki"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type BuildOptions struct {
	ProductionLimit int
	ProductionDelay int
	Audio           map[string]string
	MediaDir        string
	FontPath        string
}

type BuildSummary struct {
	Notes         int
	Cards         int
	Production    int
	AudioFiles    int
	MissingAudio  int
	Tagged        map[string]int
	UnverifiedIDs []string
}

func AudioFile(voice, text string) string {
	h := sha256.Sum256([]byte(voice + "\x1f" + text))
	return "ar-" + hex.EncodeToString(h[:8]) + ".mp3"
}

type AudioText struct {
	Field string
	Text  string
}

func AudioTexts(n notes.Note) []AudioText {
	out := []AudioText{{Field: "WordAudio", Text: n.Arabic}}
	if f := n.FormsText(); f != "" {
		out = append(out, AudioText{Field: "FormsAudio", Text: f})
	}
	if n.Example != "" {
		out = append(out, AudioText{Field: "ExampleAudio", Text: tashkeel.Pausal(PlainText(n.Example))})
	}
	return out
}

var arabicRun = regexp.MustCompile(`[\x{0600}-\x{06FF}\x{0750}-\x{077F}]+(?:[\s،]+[\x{0600}-\x{06FF}\x{0750}-\x{077F}]+)*`)

func markArabic(s string) string {
	escaped := html.EscapeString(s)
	return arabicRun.ReplaceAllStringFunc(escaped, func(run string) string {
		return `<span class="ar" lang="ar" dir="rtl">` + run + `</span>`
	})
}

var posLabels = map[string]string{
	"noun": "noun", "verb": "verb", "adj": "adjective", "adv": "adverb", "prep": "preposition",
	"conj": "conjunction", "particle": "particle", "pron": "pronoun", "num": "number",
	"intj": "interjection", "phrase": "phrase", "det": "determiner",
}

func PosLabel(pos string) string {
	if l, ok := posLabels[pos]; ok {
		return l
	}
	return pos
}

func details(n notes.Note) string {
	var parts []string
	switch n.Gender {
	case "m":
		parts = append(parts, "masc.")
	case "f":
		parts = append(parts, "fem.")
	case "m+f":
		parts = append(parts, "masc./fem.")
	}
	if n.VerbForm != "" {
		parts = append(parts, "form "+n.VerbForm)
	}
	if n.Root != "" {
		parts = append(parts, "root "+markArabic(n.Root))
	}
	if n.CEFR != "" {
		parts = append(parts, "CEFR "+n.CEFR)
	}
	return strings.Join(parts, " · ")
}

func formsHTML(n notes.Note) string {
	var parts []string
	for _, f := range n.Forms {
		parts = append(parts, `<span class="form"><span class="lbl">`+html.EscapeString(f.Label)+`</span><span class="ar" lang="ar" dir="rtl">`+html.EscapeString(f.Arabic)+`</span></span>`)
	}
	return strings.Join(parts, " ")
}

func rankBucket(pos int) string {
	if pos > UnrankedBase {
		return "rank::unranked"
	}
	lo := (pos-1)/500*500 + 1
	return fmt.Sprintf("rank::%04d-%04d", lo, lo+499)
}

func WantsProduction(n notes.Note, limit int) bool {
	if n.Production != nil {
		return *n.Production
	}
	return n.Position <= limit
}

func BuildPackage(ns []notes.Note, checks, audio []notes.Check, opts BuildOptions) (*anki.Package, BuildSummary, error) {
	if opts.ProductionLimit == 0 {
		opts.ProductionLimit = 1000
	}
	if opts.ProductionDelay == 0 {
		opts.ProductionDelay = 20
	}
	byID := map[string]notes.Check{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	audioByID := map[string]notes.Check{}
	for _, c := range audio {
		audioByID[c.ID] = c
	}
	pkg := &anki.Package{
		Deck: anki.Deck{
			ID:          DeckID,
			Name:        DeckName,
			Description: "The most common Modern Standard Arabic words in frequency order, with fully vowelled examples. Cards tagged check::diacritics had conflicting vowel readings from independent sources.",
		},
		Model: NoteType(),
		Media: map[string]string{},
	}
	summary := BuildSummary{Tagged: map[string]int{}}
	if opts.FontPath != "" {
		if _, err := os.Stat(opts.FontPath); err != nil {
			return nil, summary, fmt.Errorf("font: %w", err)
		}
		pkg.Media[FontFile] = opts.FontPath
	}

	for _, n := range ns {
		if !n.Authored() {
			continue
		}
		fields := make([]string, len(Fields))
		set := func(name, value string) { fields[fieldIndex(name)] = value }
		set("Arabic", html.EscapeString(n.Arabic))
		set("English", html.EscapeString(n.English))
		set("Hint", markArabic(n.Hint))
		set("Forms", formsHTML(n))
		set("Example", n.Example)
		set("ExampleEnglish", html.EscapeString(n.ExampleEn))
		set("Pos", PosLabel(n.Pos))
		set("Details", details(n))
		set("Position", strconv.Itoa(n.Position))
		set("NoteID", html.EscapeString(n.ID))
		set("Source", html.EscapeString(n.Source))
		for _, at := range AudioTexts(n) {
			file, ok := opts.Audio[at.Text]
			path := MediaPath(opts.MediaDir, file)
			if !ok {
				summary.MissingAudio++
				continue
			}
			if _, err := os.Stat(path); err != nil {
				summary.MissingAudio++
				continue
			}
			pkg.Media[file] = path
			summary.AudioFiles++
			set(at.Field, "[sound:"+file+"]")
		}

		tags := []string{"pos::" + n.Pos, rankBucket(n.Position)}
		if n.CEFR != "" {
			tags = append(tags, "cefr::"+n.CEFR)
		}
		var checkLines []string
		c, ok := byID[n.ID]
		switch {
		case !ok:
			tags = append(tags, "check::unverified")
			checkLines = append(checkLines, "Vowels not cross-checked yet.")
			summary.UnverifiedIDs = append(summary.UnverifiedIDs, n.ID)
		case !Current(n, c, ok):
			tags = append(tags, "check::unverified")
			checkLines = append(checkLines, "Changed since the vowels were last cross-checked.")
			summary.UnverifiedIDs = append(summary.UnverifiedIDs, n.ID)
		default:
			major, minor := false, false
			for _, is := range OpenIssues(n, c) {
				if is.Severity == notes.Major {
					major = true
				} else {
					minor = true
				}
				line := html.EscapeString(is.Detail)
				if is.Word != "" {
					line = `<span class="ar" lang="ar" dir="rtl">` + html.EscapeString(is.Word) + `</span>: ` + markArabic(is.Detail)
				}
				checkLines = append(checkLines, line)
			}
			switch {
			case major:
				tags = append(tags, "check::diacritics")
			case minor:
				tags = append(tags, "check::diacritics-minor")
			}
		}
		if ac, ok := audioByID[n.ID]; ok && CurrentAudio(n, ac) {
			for _, is := range OpenIssues(n, ac) {
				if !slices.Contains(tags, "check::audio") {
					tags = append(tags, "check::audio")
				}
				checkLines = append(checkLines, html.EscapeString(is.Detail))
			}
		}
		if len(checkLines) > 0 {
			set("Check", "<b>Check:</b><br>"+strings.Join(checkLines, "<br>"))
		}
		for _, t := range tags {
			if strings.HasPrefix(t, "check::") {
				summary.Tagged[t]++
			}
		}

		cards := []anki.Card{{Ord: 0, Due: n.Position}}
		if WantsProduction(n, opts.ProductionLimit) {
			set("Production", "y")
			cards = append(cards, anki.Card{Ord: 1, Due: n.Position + opts.ProductionDelay})
			summary.Production++
		}
		pkg.Notes = append(pkg.Notes, anki.Note{GUID: anki.GUID(n.ID), Fields: fields, Tags: tags, Cards: cards})
		summary.Notes++
		summary.Cards += len(cards)
	}
	return pkg, summary, nil
}

func MediaPath(dir, file string) string {
	return filepath.Join(dir, file)
}
