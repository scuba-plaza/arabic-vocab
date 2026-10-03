package cli

import (
	"strings"
	"testing"
)

func TestStaleNoticeDoesNotClaimNotesWereHiddenThatMayBeShown(t *testing.T) {
	got := staleNotice(2, false)
	if !strings.HasPrefix(got, "2 notes need a fresh 'arabic-vocab check'") || strings.Contains(got, "were not shown") || !strings.Contains(got, "before their vowel flags can be shown") {
		t.Errorf("notice = %q", got)
	}
	if got := staleNotice(1, false); !strings.HasPrefix(got, "1 note needs a fresh 'arabic-vocab check'") {
		t.Errorf("notice = %q", got)
	}
	if got := staleNotice(3, true); !strings.Contains(got, "older than the note") || strings.Contains(got, "were not shown") {
		t.Errorf("notice = %q", got)
	}
}
