package review

import (
	"slices"
	"testing"
)

func TestPiecesFollowBoldMarkup(t *testing.T) {
	ps := pieces("وَ<b>الْبَيْتُ</b> كَبِيرٌ.")
	if got := words(ps); !slices.Equal(got, []string{"وَالْبَيْتُ", "كَبِيرٌ"}) {
		t.Fatalf("words = %q", got)
	}
	if ps[0].bold[0] || !ps[0].bold[len(ps[0].bold)-1] {
		t.Errorf("bold = %v", ps[0].bold)
	}
	if ps[len(ps)-1].word || ps[len(ps)-1].String() != "." {
		t.Errorf("last piece = %+v", ps[len(ps)-1])
	}
}

func TestCommonFindsTheSharedWords(t *testing.T) {
	a := []string{"وَجَدْتُ", "الْمَعْلُومَاتِ", "فِي", "الْمَوْقِعِ"}
	b := []string{"وَجَدَ", "الطَّالِبُ", "الْمَعْلُومَاتِ", "فِي", "الْمَوْقِعِ"}
	inA, inB := common(a, b)
	if !slices.Equal(inA, []bool{false, true, true, true}) || !slices.Equal(inB, []bool{false, false, true, true, true}) {
		t.Errorf("common = %v %v", inA, inB)
	}
}

func TestSpokenDiffIgnoresVowelsAndPunctuation(t *testing.T) {
	want, missing, heard, extra := spokenDiff("أَغْلِقِ الْبَابَ.", "اغلق الباب الان")
	if len(want) != 2 || slices.Contains(missing, true) {
		t.Errorf("want %q missing %v", want, missing)
	}
	if !slices.Equal(extra, []bool{false, false, true}) || heard[2] != "الان" {
		t.Errorf("heard %q extra %v", heard, extra)
	}
}
