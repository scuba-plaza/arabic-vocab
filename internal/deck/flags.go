package deck

import (
	"slices"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func IssueKey(is notes.Issue) string {
	if is.Word != "" {
		return is.Word
	}
	return is.Field + ":" + is.Kind
}

const CheckVersion = 2

func Current(n notes.Note, c notes.Check, ok bool) bool {
	return ok && c.Version == CheckVersion && c.Digest == n.Digest()
}

func OpenIssues(n notes.Note, c notes.Check) []notes.Issue {
	var out []notes.Issue
	for _, is := range c.Issues {
		if !slices.Contains(n.Reviewed, IssueKey(is)) {
			out = append(out, is)
		}
	}
	return out
}

func OpenAudio(n notes.Note, checks []notes.AudioCheck, index map[string]string) []notes.AudioCheck {
	var out []notes.AudioCheck
	for _, a := range checks {
		if a.Match || a.ID != n.ID || index[a.Text] != a.File || slices.Contains(n.ReviewedAudio, a.File) {
			continue
		}
		current := slices.ContainsFunc(AudioTexts(n), func(at AudioText) bool { return at.Field == a.Field && at.Text == a.Text })
		if current {
			out = append(out, a)
		}
	}
	return out
}
