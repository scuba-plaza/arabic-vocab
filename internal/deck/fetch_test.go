package deck

import "testing"

func TestDescribeNamesTheHostOnlyOnce(t *testing.T) {
	cases := []struct {
		src  Source
		want string
	}{
		{Source{Name: "Wiktionary Arabic (kaikki.org)", URL: "https://kaikki.org/dictionary/Arabic/x.jsonl"}, "Wiktionary Arabic (kaikki.org)"},
		{Source{Name: "OpenSubtitles frequency list", URL: "https://raw.githubusercontent.com/a/b/c.txt"}, "OpenSubtitles frequency list (raw.githubusercontent.com)"},
		{Source{Name: "Local", URL: "http://127.0.0.1:8080/x"}, "Local (127.0.0.1:8080)"},
	}
	for _, tc := range cases {
		if got := Describe(tc.src); got != tc.want {
			t.Errorf("Describe(%+v) = %q, want %q", tc.src, got, tc.want)
		}
	}
}
