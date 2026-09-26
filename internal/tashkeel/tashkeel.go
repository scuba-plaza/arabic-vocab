package tashkeel

import (
	"regexp"
	"slices"
	"strings"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"golang.org/x/text/unicode/norm"
)

const (
	Fathatan = 'ً'
	Dammatan = 'ٌ'
	Kasratan = 'ٍ'
	Fatha    = 'َ'
	Damma    = 'ُ'
	Kasra    = 'ِ'
	Shadda   = 'ّ'
	Sukun    = 'ْ'
	Dagger   = 'ٰ'
	Tatweel  = 'ـ'

	alef        = 'ا'
	alefWasla   = 'ٱ'
	alefMadda   = 'آ'
	alefHamzaDn = 'إ'
	alefMaqsura = 'ى'
	waw         = 'و'
	yeh         = 'ي'
	lam         = 'ل'
)

var sunLetters = map[rune]bool{
	'ت': true, 'ث': true, 'د': true, 'ذ': true, 'ر': true, 'ز': true, 'س': true,
	'ش': true, 'ص': true, 'ض': true, 'ط': true, 'ظ': true, 'ل': true, 'ن': true,
}

func IsMark(r rune) bool {
	return (r >= 0x064B && r <= 0x065F) || r == Dagger || (r >= 0x06D6 && r <= 0x06ED)
}

func isVowel(r rune) bool {
	switch r {
	case Fatha, Damma, Kasra, Fathatan, Dammatan, Kasratan, Dagger:
		return true
	}
	return false
}

type cluster struct {
	base  rune
	marks []rune
}

func (c *cluster) has(m rune) bool {
	return slices.Contains(c.marks, m)
}

func (c *cluster) drop(ms ...rune) {
	c.marks = slices.DeleteFunc(c.marks, func(r rune) bool { return slices.Contains(ms, r) })
}

func (c *cluster) hasVowel() bool {
	return slices.ContainsFunc(c.marks, isVowel)
}

func clusters(word string) []cluster {
	var out []cluster
	for _, r := range norm.NFC.String(word) {
		switch {
		case r == Tatweel:
		case IsMark(r):
			if len(out) > 0 {
				out[len(out)-1].marks = append(out[len(out)-1].marks, r)
			}
		case r == alefWasla:
			out = append(out, cluster{base: alef})
		default:
			out = append(out, cluster{base: r})
		}
	}
	return out
}

func render(cs []cluster) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteRune(c.base)
		marks := slices.Clone(c.marks)
		slices.Sort(marks)
		for _, m := range slices.Compact(marks) {
			b.WriteRune(m)
		}
	}
	return b.String()
}

func Skeleton(s string) string {
	var b strings.Builder
	for _, r := range norm.NFC.String(s) {
		switch {
		case IsMark(r), r == Tatweel:
		case r == alefWasla:
			b.WriteRune(alef)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func articleLamFree(c cluster, next cluster) bool {
	if !c.hasVowel() {
		return true
	}
	return next.base == alef && c.has(Kasra) && len(c.marks) == 1
}

func articleStart(cs []cluster) (int, bool) {
	for p := 0; p <= 2 && p+2 < len(cs); p++ {
		if p > 0 && !strings.ContainsRune("وفبك", cs[p-1].base) {
			break
		}
		if cs[p].base == alef && cs[p+1].base == lam && articleLamFree(cs[p+1], cs[p+2]) {
			return p + 2, true
		}
	}
	for p := 0; p <= 1 && p+2 < len(cs); p++ {
		if p > 0 && !strings.ContainsRune("وف", cs[p-1].base) {
			break
		}
		if cs[p].base == lam && cs[p+1].base == lam && articleLamFree(cs[p+1], cs[p+2]) {
			return p + 2, true
		}
	}
	return 0, false
}

func onlySukun(c cluster) bool {
	for _, m := range c.marks {
		if m != Sukun {
			return false
		}
	}
	return true
}

func normalize(word string, keepSukun bool) []cluster {
	cs := clusters(word)
	for i := range cs {
		for j, m := range cs[i].marks {
			if m == Dagger {
				cs[i].marks[j] = Fatha
			}
		}
		cs[i].marks = slices.DeleteFunc(cs[i].marks, func(r rune) bool {
			if r == Sukun {
				return !keepSukun
			}
			return !(isVowel(r) || r == Shadda)
		})
	}
	if n := len(cs); n >= 2 && (cs[n-1].base == alef || cs[n-1].base == alefMaqsura) && cs[n-1].has(Fathatan) {
		cs[n-1].drop(Fathatan)
		if !cs[n-2].has(Fathatan) {
			cs[n-2].marks = append(cs[n-2].marks, Fathatan)
		}
	}
	if x, ok := articleStart(cs); ok {
		cs[x-1].marks = nil
		if cs[x-2].base == alef {
			cs[x-2].marks = nil
		}
		if sunLetters[cs[x].base] {
			cs[x].drop(Shadda)
		}
		if cs[x].base == alef {
			cs[x].marks = nil
		}
	}
	for i := range cs {
		switch cs[i].base {
		case alef:
			cs[i].marks = slices.DeleteFunc(cs[i].marks, func(r rune) bool { return r != Fathatan })
		case alefHamzaDn:
			cs[i].drop(Kasra)
		case alefMaqsura:
			cs[i].drop(Fatha, Sukun)
		}
	}
	vowelLetter := func(i int) bool {
		if i >= len(cs) || !onlySukun(cs[i]) {
			return false
		}
		switch cs[i].base {
		case alef, alefMaqsura:
			return true
		case waw, yeh:
			return i+1 >= len(cs) || (cs[i+1].base != alef && cs[i+1].base != alefMaqsura)
		}
		return false
	}
	for i := range cs {
		if !vowelLetter(i + 1) {
			continue
		}
		switch cs[i+1].base {
		case alef, alefMaqsura:
			cs[i].drop(Fatha)
		case waw:
			if cs[i].has(Damma) {
				cs[i].drop(Damma)
				cs[i+1].drop(Sukun)
			}
		case yeh:
			if cs[i].has(Kasra) {
				cs[i].drop(Kasra)
				cs[i+1].drop(Sukun)
			}
		}
	}
	return cs
}

func canonical(word string) []cluster {
	return normalize(word, false)
}

func vowels(c cluster) []rune {
	var out []rune
	for _, m := range c.marks {
		if isVowel(m) {
			out = append(out, m)
		}
	}
	slices.Sort(out)
	return out
}

func Compatible(word, reading string) bool {
	return compatible(normalize(word, true), normalize(reading, true))
}

func CompatibleCitation(word, reading string) bool {
	a, b := normalize(word, true), normalize(reading, true)
	for _, cs := range [][]cluster{a, b} {
		if n := len(cs); n > 0 {
			cs[n-1].marks = slices.DeleteFunc(cs[n-1].marks, func(r rune) bool { return r != Shadda })
		}
		for i := range cs {
			cs[i].drop(Fathatan, Dammatan, Kasratan)
		}
	}
	return compatible(a, b)
}

func compatible(word, reading []cluster) bool {
	if len(word) != len(reading) {
		return false
	}
	for i := range word {
		w, r := word[i], reading[i]
		if w.base != r.base {
			return false
		}
		if len(r.marks) == 0 {
			continue
		}
		if w.has(Shadda) != r.has(Shadda) {
			return false
		}
		rv := vowels(r)
		if len(rv) == 0 {
			if r.has(Sukun) && i < len(word)-1 && len(vowels(w)) > 0 {
				return false
			}
			continue
		}
		if !slices.Equal(rv, vowels(w)) {
			return false
		}
	}
	return true
}

func DiacKey(word string) string {
	return render(canonical(word))
}

func LexKey(word string) string {
	cs := canonical(word)
	for i := range cs {
		cs[i].drop(Fathatan, Dammatan, Kasratan)
	}
	if n := len(cs); n > 0 {
		cs[n-1].drop(Fatha, Damma, Kasra)
	}
	return render(cs)
}

func Words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(arabic.IsArabicLetter(r) || IsMark(r) || r == Tatweel)
	})
}

type MissingMark struct {
	Index  int
	Letter string
}

func UnmarkedLetters(word string, citation bool) []MissingMark {
	cs := clusters(word)
	var missing []MissingMark
	art, hasArticle := articleStart(cs)
	for i, c := range cs {
		if c.hasVowel() || c.has(Sukun) || !arabic.IsArabicLetter(c.base) {
			continue
		}
		switch c.base {
		case alef, alefMaqsura, alefMadda:
			continue
		case waw:
			if i > 0 && cs[i-1].has(Damma) {
				continue
			}
		case yeh:
			if i > 0 && cs[i-1].has(Kasra) {
				continue
			}
		case lam:
			if hasArticle && i == art-1 && (sunLetters[cs[art].base] || cs[art].base == alef) {
				continue
			}
		}
		if i == len(cs)-1 && citation {
			continue
		}
		missing = append(missing, MissingMark{Index: i, Letter: string(c.base)})
	}
	return missing
}

var sentenceEnd = regexp.MustCompile(`([\x{0621}-\x{0652}\x{0670}-\x{06D3}]+)(\s*)([.!?؟]|$)`)

var keepsFinalVowel = map[string]bool{}

func init() {
	for _, w := range []string{"أَنْتَ", "أَنْتِ", "هُوَ", "هِيَ", "نَحْنُ"} {
		keepsFinalVowel[render(clusters(w))] = true
	}
}

func Pausal(s string) string {
	return sentenceEnd.ReplaceAllStringFunc(s, func(m string) string {
		sub := sentenceEnd.FindStringSubmatch(m)
		return pausalWord(sub[1]) + sub[2] + sub[3]
	})
}

func isCaseVowel(r rune) bool {
	switch r {
	case Fatha, Damma, Kasra, Fathatan, Dammatan, Kasratan:
		return true
	}
	return false
}

func pausalWord(word string) string {
	cs := clusters(word)
	n := len(cs)
	if n == 0 || keepsFinalVowel[render(cs)] {
		return word
	}
	last := &cs[n-1]
	switch {
	case (last.base == alef || last.base == alefMaqsura) && n > 1 && (last.has(Fathatan) || cs[n-2].has(Fathatan)):
		last.drop(Fathatan)
		cs[n-2].drop(Fathatan)
		cs[n-2].marks = append(cs[n-2].marks, Fatha)
	case last.has(Fathatan) && last.base != 'ة':
		last.drop(Fathatan)
		last.marks = append(last.marks, Fatha)
	case slices.ContainsFunc(last.marks, isCaseVowel):
		last.marks = slices.DeleteFunc(last.marks, isCaseVowel)
		if !last.has(Shadda) && last.base != 'ة' {
			last.marks = append(last.marks, Sukun)
		}
	default:
		return word
	}
	return render(cs)
}
