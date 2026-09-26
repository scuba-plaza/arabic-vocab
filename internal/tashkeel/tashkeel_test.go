package tashkeel

import (
	"slices"
	"testing"
)

func TestSkeleton(t *testing.T) {
	cases := map[string]string{
		"كِتَابٌ":      "كتاب",
		"ٱسْتَخْدَمَ":  "استخدم",
		"مُحَمَّـــدٌ": "محمد",
		"هٰذَا":        "هذا",
	}
	for in, want := range cases {
		if got := Skeleton(in); got != want {
			t.Errorf("Skeleton(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLexKeyMatchesAcrossConventions(t *testing.T) {
	pairs := [][2]string{
		{"كِتَاب", "كِتاب"},
		{"قَالَ", "قال"},
		{"اِسْتَخْدَمَ", "ٱِسْتَخْدَم"},
		{"هٰذَا", "هٰذا"},
		{"سَيَّارَة", "سَيّارَة"},
		{"حَتَّى", "حَتَّى"},
		{"مُسْتَشْفًى", "مُسْتَشْفَى"},
		{"أَيْضًا", "أَيْضاً"},
		{"اَلَّذِي", "الَّذِي"},
		{"أَرَادَ", "أَراد"},
		{"لَيْسَ", "لَيْس"},
		{"يَقُولُ", "يَقول"},
	}
	for _, p := range pairs {
		if LexKey(p[0]) != LexKey(p[1]) {
			t.Errorf("LexKey(%q)=%q and LexKey(%q)=%q should match", p[0], LexKey(p[0]), p[1], LexKey(p[1]))
		}
	}
}

func TestLexKeyKeepsDistinctWordsApart(t *testing.T) {
	pairs := [][2]string{
		{"عِلْم", "عَلَم"},
		{"رَجُل", "رَجِل"},
		{"بَعْدَ", "بُعْد"},
		{"كَتَبَ", "كُتِبَ"},
		{"لَيْسَ", "لَيِسَ"},
	}
	for _, p := range pairs {
		if LexKey(p[0]) == LexKey(p[1]) {
			t.Errorf("LexKey should separate %q and %q, both gave %q", p[0], p[1], LexKey(p[0]))
		}
	}
}

func TestDiacKeyIgnoresOrthographicConventions(t *testing.T) {
	pairs := [][2]string{
		{"الشَّارِعِ", "الشارِعِ"},
		{"الطَّالِبُ", "الطّالِبُ"},
		{"الِامْتِحَانَ", "الاِمْتِحانَ"},
		{"إِلَى", "إلَى"},
		{"قِطًّا", "قِطّاً"},
		{"هَذَا", "هٰذا"},
		{"يَقُولُ", "يَقولُ"},
		{"بِالْكِتَابِ", "بِالكِتابِ"},
		{"لِلطَّالِبِ", "لِلطالِبِ"},
		{"وَالْكِتَابُ", "وَالكِتابُ"},
		{"عَلَىٰ", "عَلَى"},
	}
	for _, p := range pairs {
		if DiacKey(p[0]) != DiacKey(p[1]) {
			t.Errorf("DiacKey(%q)=%q and DiacKey(%q)=%q should match", p[0], DiacKey(p[0]), p[1], DiacKey(p[1]))
		}
	}
}

func TestDiacKeyKeepsRealDifferences(t *testing.T) {
	pairs := [][2]string{
		{"وَقْتٌ", "وَقْتُ"},
		{"الْعَلَمَ", "الْعِلْمَ"},
		{"الْيَوْمَ", "الْيَوْمِ"},
		{"أَنْ", "أَنَّ"},
		{"وَالِدٌ", "وَالَدٌ"},
		{"كِتَابًا", "كِتَابٌ"},
	}
	for _, p := range pairs {
		if DiacKey(p[0]) == DiacKey(p[1]) {
			t.Errorf("DiacKey should separate %q and %q, both gave %q", p[0], p[1], DiacKey(p[0]))
		}
	}
}

func TestWordsDropsPunctuationAndMarkup(t *testing.T) {
	got := Words("قَرَأْتُ <b>كِتَابًا</b> جَدِيدًا، ثُمَّ نِمْتُ.")
	want := []string{"قَرَأْتُ", "كِتَابًا", "جَدِيدًا", "ثُمَّ", "نِمْتُ"}
	if !slices.Equal(got, want) {
		t.Errorf("Words = %q, want %q", got, want)
	}
}

func TestUnmarkedLetters(t *testing.T) {
	cases := []struct {
		word     string
		citation bool
		missing  []string
	}{
		{"كِتَابٌ", false, nil},
		{"كِتَاب", true, nil},
		{"كِتَاب", false, []string{"ب"}},
		{"الشَّمْسُ", false, nil},
		{"الْقَمَرُ", false, nil},
		{"القَمَرُ", false, []string{"ل"}},
		{"يَقُولُ", false, nil},
		{"كتب", false, []string{"ك", "ت", "ب"}},
		{"فِي", false, nil},
		{"كَتَبُوا", false, nil},
		{"هٰذَا", false, nil},
		{"الِامْتِحَانَ", false, nil},
	}
	for _, c := range cases {
		var got []string
		for _, m := range UnmarkedLetters(c.word, c.citation) {
			got = append(got, m.Letter)
		}
		if !slices.Equal(got, c.missing) {
			t.Errorf("UnmarkedLetters(%q, %v) = %q, want %q", c.word, c.citation, got, c.missing)
		}
	}
}

func TestCompatibleTreatsUnmarkedLettersAsUnspecified(t *testing.T) {
	agree := [][2]string{
		{"أُرِيدُ", "أُرِيد"},
		{"ذَهَبْتُ", "ذَهَبْتْ"},
		{"مَاءً", "ماء"},
		{"الشَّايَ", "الشاي"},
		{"وَعَلَيْكُمُ", "وَعَلَيكُم"},
		{"تُسَاعِدَنِي", "تُساعِدنِي"},
		{"وَاثْنَانِ", "وَاِثْنانِ"},
		{"قِيَامٌ", "قِيامٌ"},
		{"أَيَّ", "أَيّ"},
	}
	for _, p := range agree {
		if !Compatible(p[0], p[1]) {
			t.Errorf("%q should be compatible with the reading %q", p[0], p[1])
		}
	}
	disagree := [][2]string{
		{"الْكِتَابُ", "الْكِتَابِ"},
		{"أَيَّ", "أَيْ"},
		{"تُفَضِّلُ", "تَفْضُل"},
		{"حَدَثَ", "حَدَّثَ"},
		{"قَرَأْتُ", "قَرَأَتْ"},
	}
	for _, p := range disagree {
		if Compatible(p[0], p[1]) {
			t.Errorf("%q should conflict with the reading %q", p[0], p[1])
		}
	}
}

func TestCompatibleCitationIgnoresEndings(t *testing.T) {
	if !CompatibleCitation("كِتَاب", "كِتابٌ") || !CompatibleCitation("قِيَام", "قِيامٍ") {
		t.Error("citation forms should ignore case endings")
	}
	if CompatibleCitation("كِتَاب", "كُتّاب") {
		t.Error("different stems must still conflict")
	}
}

func TestPausal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"كَانَ الْجَوُّ جَمِيلًا أَمْسِ.", "كَانَ الْجَوُّ جَمِيلًا أَمْسْ."},
		{"سَأَنْتَظِرُ حَتَّى الْمَسَاءِ.", "سَأَنْتَظِرُ حَتَّى الْمَسَاءْ."},
		{"مَاذَا فَعَلْتَ أَمْسِ؟", "مَاذَا فَعَلْتَ أَمْسْ؟"},
		{"شُكْرًا جَزِيلًا!", "شُكْرًا جَزِيلَا!"},
		{"شُكْرًا! عَفْوًا.", "شُكْرَا! عَفْوَا."},
		{"السَّلَامُ عَلَيْكُمْ. وَعَلَيْكُمُ السَّلَامُ.", "السَّلَامُ عَلَيْكُمْ. وَعَلَيْكُمُ السَّلَامْ."},
		{"عُمْرِي عِشْرُونَ سَنَةً.", "عُمْرِي عِشْرُونَ سَنَة."},
		{"أُرِيدُ مَاءً.", "أُرِيدُ مَاءَ."},
		{"هٰذَا الطَّالِبُ جَدِيدٌ", "هٰذَا الطَّالِبُ جَدِيدْ"},
		{"الْعَدُوُّ", "الْعَدُوّ"},
		{"مَرْحَبًا، كَيْفَ حَالُكَ؟", "مَرْحَبًا، كَيْفَ حَالُكْ؟"},
		{"أَنَا هُنَا.", "أَنَا هُنَا."},
		{"هٰذَا صَدِيقِي.", "هٰذَا صَدِيقِي."},
		{"يَا صَدِيقِي، أَيْنَ أَنْتَ؟", "يَا صَدِيقِي، أَيْنَ أَنْتَ؟"},
		{"جِئْتُ فَقَطْ.", "جِئْتُ فَقَطْ."},
		{"أَرَاكَ غَدًا إِنْ شَاءَ اللّٰهُ.", "أَرَاكَ غَدًا إِنْ شَاءَ اللّٰهْ."},
	}
	for _, tc := range cases {
		if got := Pausal(tc.in); render(clusters(got)) != render(clusters(tc.want)) {
			t.Errorf("Pausal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
