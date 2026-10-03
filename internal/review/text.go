package review

import (
	"strings"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

type piece struct {
	runes []rune
	bold  []bool
	word  bool
}

func (p piece) String() string {
	return string(p.runes)
}

func wordRune(r rune) bool {
	return arabic.IsArabicLetter(r) || tashkeel.IsMark(r) || r == tashkeel.Tatweel
}

func pieces(markup string) []piece {
	var out []piece
	bold := false
	rest := markup
	for rest != "" {
		if rest[0] == '<' {
			if end := strings.IndexByte(rest, '>'); end > 0 {
				switch strings.ToLower(strings.TrimSpace(rest[1:end])) {
				case "b":
					bold = true
				case "/b":
					bold = false
				}
				rest = rest[end+1:]
				continue
			}
		}
		next := len(rest)
		if i := strings.IndexByte(rest[1:], '<'); i >= 0 {
			next = i + 1
		}
		for _, r := range rest[:next] {
			w := wordRune(r)
			if n := len(out); n > 0 && out[n-1].word == w {
				out[n-1].runes = append(out[n-1].runes, r)
				out[n-1].bold = append(out[n-1].bold, bold)
				continue
			}
			out = append(out, piece{runes: []rune{r}, bold: []bool{bold}, word: w})
		}
		rest = rest[next:]
	}
	return out
}

func words(ps []piece) []string {
	var out []string
	for _, p := range ps {
		if p.word {
			out = append(out, p.String())
		}
	}
	return out
}

func common(a, b []string) ([]bool, []bool) {
	n, m := len(a), len(b)
	length := make([][]int, n+1)
	for i := range length {
		length[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				length[i][j] = length[i+1][j+1] + 1
			} else {
				length[i][j] = max(length[i+1][j], length[i][j+1])
			}
		}
	}
	inA, inB := make([]bool, n), make([]bool, m)
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case a[i] == b[j]:
			inA[i], inB[j] = true, true
			i++
			j++
		case length[i+1][j] >= length[i][j+1]:
			i++
		default:
			j++
		}
	}
	return inA, inB
}
