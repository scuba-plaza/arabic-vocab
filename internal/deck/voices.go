package deck

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

type VoiceSample struct {
	Arabic  string
	Roman   string
	English string
}

var VoiceSamples = []VoiceSample{
	{"عَلِمَ", "ʕalima", "he knew"},
	{"عَلَّمَ", "ʕallama", "he taught"},
	{"عُلِمَ", "ʕulima", "it was known"},
	{"كَتَبَ", "kataba", "he wrote"},
	{"كُتِبَ", "kutiba", "it was written"},
	{"كُتُب", "kutub", "books"},
	{"مَلِك", "malik", "king"},
	{"مَلَك", "malak", "angel"},
	{"مُلْك", "mulk", "kingdom"},
	{"ذَهَبَ", "ḏahaba", "he went"},
	{"ذَهَب", "ḏahab", "gold"},
	{"كِتَابٌ، كِتَابًا، كِتَابٍ", "kitābun, kitāban, kitābin", "case endings with tanween"},
	{"الشَّمْسُ وَالْقَمَرُ", "aš-šamsu wa-l-qamaru", "sun and moon letters"},
	{"قَرَأَ الطَّالِبُ الْكِتَابَ فِي الْمَكْتَبَةِ.", "qaraʔa ṭ-ṭālibu l-kitāba fī l-maktabati", "full sentence with case endings"},
}

func CompareVoices(ctx context.Context, voices []Voice, dir string, speak func(ctx context.Context, voice Voice, text, path string) error, progress func(done, total int)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	total := len(voices) * len(VoiceSamples)
	done := 0
	for _, v := range voices {
		vdir := filepath.Join(dir, v.Name)
		if err := os.MkdirAll(vdir, 0o755); err != nil {
			return "", err
		}
		for i, s := range VoiceSamples {
			path := filepath.Join(vdir, fmt.Sprintf("%02d.mp3", i+1))
			if _, err := os.Stat(path); err != nil {
				if err := speak(ctx, v, s.Arabic, path); err != nil {
					return "", fmt.Errorf("%s: %w", v.Name, err)
				}
			}
			done++
			if progress != nil {
				progress(done, total)
			}
		}
	}
	page := filepath.Join(dir, "index.html")
	if err := os.WriteFile(page, []byte(voicesPage(voices)), 0o644); err != nil {
		return "", err
	}
	return page, nil
}

func voicesPage(voices []Voice) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Voices</title>
<style>
body{font-family:system-ui,sans-serif;margin:16px;background:#fafafa;color:#1d1d1f}
table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #ddd;padding:8px;vertical-align:middle}
.ar{font-size:1.8rem;direction:rtl;font-family:"Scheherazade New","Noto Naskh Arabic",serif}.en{color:#666;font-size:.9rem}
audio{width:180px}
</style></head><body>
<h1>Which voice follows the vowel marks?</h1>
<p>Each row is a word or sentence whose meaning depends on its vowels. Listen for the difference between rows in the same group, then run <code>arabic-vocab audio --voice NAME</code> with the voice you trust. It is saved in the deck's <code>deck.json</code>, so later runs use it too.</p>
<table><tr><th>Text</th>`)
	for _, v := range voices {
		b.WriteString("<th>" + html.EscapeString(v.Name) + "</th>")
	}
	b.WriteString("</tr>\n")
	for i, s := range VoiceSamples {
		b.WriteString(`<tr><td><div class="ar">` + html.EscapeString(s.Arabic) + `</div><div class="en">` + html.EscapeString(s.Roman+" · "+s.English) + `</div></td>`)
		for _, v := range voices {
			b.WriteString(fmt.Sprintf(`<td><audio controls preload="none" src="%s/%02d.mp3"></audio></td>`, html.EscapeString(v.Name), i+1))
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table></body></html>\n")
	return b.String()
}
