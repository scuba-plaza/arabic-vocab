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

const CheckVersion = 3

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
