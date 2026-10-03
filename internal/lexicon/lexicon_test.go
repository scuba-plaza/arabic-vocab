package lexicon

import (
	"slices"
	"strings"
	"testing"
)

const dump = `{"word":"ماء","pos":"noun","etymology_number":1,"forms":[{"form":"مَاء","tags":["canonical","masculine"]},{"form":"مِيَاه","tags":["plural"]}],"senses":[{"glosses":["water"]}]}
{"word":"ماء","pos":"verb","etymology_number":2,"forms":[{"form":"مَاءَ","tags":["canonical","form-i"]}],"senses":[{"glosses":["to meow"]}]}
{"word":"كتاب","pos":"noun","forms":[{"form":"كِتَاب","tags":["canonical"]}],"senses":[{"glosses":["book"]}]}
{"word":"x","pos":"character","senses":[{"glosses":["a letter"]}]}
`

func titles(entries []*Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Title+"/"+e.Pos)
	}
	return out
}

func TestReadWhereKeepsOnlyChosenTitles(t *testing.T) {
	entries, err := ReadWhere(strings.NewReader(dump), func(title, canonical string) bool { return title == "ماء" })
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(entries); !slices.Equal(got, []string{"ماء/noun", "ماء/verb"}) {
		t.Fatalf("entries = %v", got)
	}
	if entries[0].Canonical != "مَاء" || entries[0].Gender != "m" || entries[1].VerbForm != "I" {
		t.Errorf("entries were not converted as Read does: %+v %+v", entries[0], entries[1])
	}
}

func TestReadIsReadWhereWithoutFilter(t *testing.T) {
	all, err := Read(strings.NewReader(dump))
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(all); !slices.Equal(got, []string{"ماء/noun", "ماء/verb", "كتاب/noun"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestReadWhereReportsBrokenLines(t *testing.T) {
	_, err := ReadWhere(strings.NewReader("{\"word\":\"ماء\"}\nnot json\n"), func(string, string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

const odd = `{"word":"جامعة","pos":"noun","forms":[{"form":"الجَامِعَة","tags":["canonical"]}],"senses":[{"glosses":["university"]}]}
{"word":"عفج","pos":"noun","forms":[{"form":"مَعْفُوج","tags":["canonical"]}],"senses":[{"glosses":["x"]}]}
{"word":"كلب","pos":"noun","senses":[{"glosses":["dog"]}]}
{"word":"قلم","pos":"noun","forms":[{"form":"qalam","tags":["romanization"]}],"senses":[{"glosses":["pen"]}]}
`

func TestReadWhereShowsTheFilterBothTheTitleAndTheCanonicalForm(t *testing.T) {
	var seen []string
	entries, err := ReadWhere(strings.NewReader(odd), func(title, canonical string) bool {
		seen = append(seen, title+"|"+canonical)
		return canonical == "مَعْفُوج" || title == "جامعة"
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"جامعة|الجَامِعَة", "عفج|مَعْفُوج", "كلب|كلب", "قلم|قلم"}; !slices.Equal(seen, want) {
		t.Errorf("the filter saw %q, want %q", seen, want)
	}
	if got := titles(entries); !slices.Equal(got, []string{"جامعة/noun", "عفج/noun"}) {
		t.Fatalf("entries = %v", got)
	}
	if entries[0].Canonical != "الجَامِعَة" || entries[1].Canonical != "مَعْفُوج" {
		t.Errorf("entries were not converted as Read does: %+v %+v", entries[0], entries[1])
	}
}
