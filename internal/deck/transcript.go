package deck

import (
	"slices"
	"strconv"
	"strings"

	"github.com/scuba-plaza/arabic-tts/arabic"
)

var numberWords = map[string]int{
	"صفر":  0,
	"واحد": 1, "واحده": 1,
	"اثنان": 2, "اثنين": 2, "اثنتان": 2, "اثنتين": 2, "اثنا": 2, "اثنتا": 2,
	"ثلاثه": 3, "ثلاث": 3,
	"اربعه": 4, "اربع": 4,
	"خمسه": 5, "خمس": 5,
	"سته": 6, "ست": 6,
	"سبعه": 7, "سبع": 7,
	"ثمانيه": 8, "ثماني": 8, "ثمان": 8,
	"تسعه": 9, "تسع": 9,
	"عشره": 10, "عشر": 10,
	"عشرون": 20, "عشرين": 20,
	"ثلاثون": 30, "ثلاثين": 30,
	"اربعون": 40, "اربعين": 40,
	"خمسون": 50, "خمسين": 50,
	"ستون": 60, "ستين": 60,
	"سبعون": 70, "سبعين": 70,
	"ثمانون": 80, "ثمانين": 80,
	"تسعون": 90, "تسعين": 90,
	"مئه": 100, "مائه": 100,
	"مئتان": 200, "مئتين": 200, "مائتان": 200, "مائتين": 200,
	"الف":   1000,
	"الفان": 2000, "الفين": 2000,
}

var spokenSymbols = strings.NewReplacer(
	"+", " و ", "=", " ",
	"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
)

func number(t string) (int, bool) {
	if v, ok := numberWords[t]; ok {
		return v, true
	}
	if t == "" || strings.Trim(t, "0123456789") != "" {
		return 0, false
	}
	v, err := strconv.Atoi(t)
	return v, err == nil
}

func isTen(t string) bool {
	return t == "عشر" || t == "عشره"
}

func spokenTokens(s string) []string {
	var split []string
	for _, t := range strings.Fields(arabic.Normalize(spokenSymbols.Replace(s))) {
		if _, ok := number(t); !ok && strings.HasPrefix(t, "و") {
			if _, ok := number(strings.TrimPrefix(t, "و")); ok {
				split = append(split, "و", strings.TrimPrefix(t, "و"))
				continue
			}
		}
		split = append(split, t)
	}
	var out []string
	for i := 0; i < len(split); i++ {
		t := split[i]
		v, ok := number(t)
		switch {
		case (t == "احد" || t == "احدي") && i+1 < len(split) && isTen(split[i+1]):
			v, ok = 11, true
			i++
		case ok && v >= 1 && v <= 9 && i+1 < len(split) && isTen(split[i+1]):
			v += 10
			i++
		case ok && v >= 1 && v <= 9 && i+2 < len(split) && split[i+1] == "و":
			if tens, isNum := number(split[i+2]); isNum && tens >= 20 && tens <= 90 && tens%10 == 0 {
				v += tens
				i += 2
			}
		}
		if ok {
			t = strconv.Itoa(v)
		}
		out = append(out, t)
	}
	return out
}

func TranscriptMatches(text, transcript string) bool {
	return slices.Equal(spokenTokens(text), spokenTokens(transcript))
}
