package cli

import (
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/review"
)

func newStatusCommand(paths *deck.Paths) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show where the deck stands and what to run next",
		Long: "Show how many words the deck has, which notes need a fresh check,\n" +
			"how many flags are waiting for review, how much audio is missing and whether\n" +
			"the Anki package is up to date, and name the command to run next.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := readStatus(paths)
			if err != nil {
				return err
			}
			s.print(cmd.OutOrStdout(), paths)
			return nil
		},
	}
}

type deckStatus struct {
	settings   deck.Settings
	written    int
	unfinished int
	checked    bool
	unchecked  int
	major      int
	minor      int
	clips      int
	missing    int
	silent     int
	pkg        string
	pkgExists  bool
	pkgStale   bool
}

func readStatus(paths *deck.Paths) (deckStatus, error) {
	var s deckStatus
	ns, err := notes.ReadJSONL[notes.Note](paths.Notes())
	if err != nil {
		return s, err
	}
	checks, err := notes.ReadJSONL[notes.Check](paths.QA())
	if err != nil {
		return s, err
	}
	audioChecks, err := notes.ReadJSONL[notes.Check](paths.AudioQA())
	if err != nil {
		return s, err
	}
	if s.settings, err = deck.LoadSettings(paths.Settings()); err != nil {
		return s, err
	}
	var written []notes.Note
	for _, n := range ns {
		if n.Authored() {
			written = append(written, n)
		} else {
			s.unfinished++
		}
	}
	s.written = len(written)
	s.checked = len(checks) > 0
	items, stale := review.Items(written, checks, audioChecks, review.Filter{Minor: true})
	s.unchecked = stale
	for _, it := range items {
		if it.Major() {
			s.major++
		} else {
			s.minor++
		}
	}
	audioByID := map[string]notes.Check{}
	for _, c := range audioChecks {
		audioByID[c.ID] = c
	}
	silent := func(n notes.Note, field string) bool {
		c, ok := audioByID[n.ID]
		return ok && deck.CurrentAudio(n, c) && slices.ContainsFunc(c.Issues, func(is notes.Issue) bool {
			return is.Field == field && is.Kind == deck.KindSilent
		})
	}
	voice := s.settings.AudioVoice().Key()
	seen := map[string]bool{}
	for _, n := range written {
		for _, at := range deck.AudioTexts(n) {
			file := deck.AudioFile(voice, at.Text)
			if seen[file] {
				continue
			}
			seen[file] = true
			s.clips++
			if _, err := os.Stat(paths.MediaFile(file)); err != nil {
				if silent(n, at.Field) {
					s.silent++
				} else {
					s.missing++
				}
			}
		}
	}
	s.pkg = paths.Package(deck.DeckFileName)
	if st, err := os.Stat(s.pkg); err == nil {
		s.pkgExists = true
		s.pkgStale = st.ModTime().Before(newest(paths.Notes(), paths.QA(), paths.AudioQA(), paths.Manifest(), paths.Settings()))
	}
	return s, nil
}

func newest(paths ...string) time.Time {
	var t time.Time
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}

func (s deckStatus) next() string {
	switch {
	case s.written == 0 || s.unfinished > 0:
		return "add"
	case s.unchecked > 0:
		return "check"
	case s.major > 0:
		return "review"
	case s.missing > 0:
		return "audio"
	case !s.pkgExists || s.pkgStale:
		return "build"
	}
	return ""
}

func (s deckStatus) print(w io.Writer, paths *deck.Paths) {
	next := s.next()
	fmt.Fprintf(w, "%s: %s, voice %s at rate %.2f\n\n", paths.Deck, count(s.written, "word", "words"), s.settings.Voice, s.settings.Rate)

	add := count(s.written, "word", "words") + " written"
	if s.unfinished > 0 {
		add += ", " + count(s.unfinished, "note", "notes") + " not written yet"
	}
	check := "every note checked"
	switch {
	case !s.checked && s.written > 0:
		check = "not run yet"
	case s.unchecked > 0:
		check = count(s.unchecked, "note needs", "notes need") + " a fresh check"
	}
	flags := "nothing flagged"
	switch {
	case s.major > 0 && s.minor > 0:
		flags = fmt.Sprintf("%s with major flags, %d with minor ones", count(s.major, "note", "notes"), s.minor)
	case s.major > 0:
		flags = count(s.major, "note", "notes") + " with major flags"
	case s.minor > 0:
		flags = count(s.minor, "note", "notes") + " with only minor flags, which are optional"
	}
	audio := fmt.Sprintf("all %s", count(s.clips, "clip", "clips"))
	switch {
	case s.missing > 0:
		audio = fmt.Sprintf("%d of %s missing", s.missing, count(s.clips, "clip", "clips"))
	case s.silent > 0:
		audio = fmt.Sprintf("%d of %s made", s.clips-s.silent, count(s.clips, "clip", "clips"))
	}
	if s.silent > 0 {
		audio += "; " + count(s.silent, "clip", "clips") + " came back silent and wait for review"
	}
	build := s.pkg + " is up to date"
	switch {
	case !s.pkgExists:
		build = "no package yet"
	case s.pkgStale:
		build = s.pkg + " is older than the notes"
	}
	for _, step := range []struct {
		name, text string
		done       bool
	}{
		{"add", add, s.written > 0 && s.unfinished == 0},
		{"check", check, s.checked && s.unchecked == 0},
		{"review", flags, s.major == 0},
		{"audio", audio, s.missing == 0 && s.silent == 0},
		{"build", build, s.pkgExists && !s.pkgStale},
	} {
		mark := " "
		switch {
		case step.name == next:
			mark = "→"
		case step.done:
			mark = "✓"
		}
		fmt.Fprintf(w, "  %s %-7s %s\n", mark, step.name, step.text)
	}
	if next == "" {
		fmt.Fprintf(w, "\nThe deck is up to date; 'arabic-vocab add' adds the next 100 words.\n")
		return
	}
	fmt.Fprintf(w, "\nnext: arabic-vocab %s\n", next)
}
