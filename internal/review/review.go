package review

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type Item struct {
	Index  int
	Issues []notes.Issue
	Audio  []notes.AudioCheck
}

func (it Item) Len() int {
	return len(it.Issues) + len(it.Audio)
}

func Items(ns []notes.Note, checks []notes.Check, audio []notes.AudioCheck, index map[string]string, minor bool) ([]Item, int) {
	byID := map[string]notes.Check{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	audioByID := map[string][]notes.AudioCheck{}
	for _, a := range audio {
		audioByID[a.ID] = append(audioByID[a.ID], a)
	}
	var items []Item
	stale := 0
	for i, n := range ns {
		if !n.Authored() {
			continue
		}
		it := Item{Index: i, Audio: deck.OpenAudio(n, audioByID[n.ID], index)}
		c, ok := byID[n.ID]
		if deck.Current(n, c, ok) {
			for _, is := range deck.OpenIssues(n, c) {
				if minor || is.Severity == notes.Major {
					it.Issues = append(it.Issues, is)
				}
			}
		} else {
			stale++
		}
		if it.Len() > 0 {
			items = append(items, it)
		}
	}
	return items, stale
}

func Feedback(it Item) string {
	var b strings.Builder
	b.WriteString("An automatic check disagreed with this card:\n")
	for _, is := range it.Issues {
		if is.Word != "" {
			fmt.Fprintf(&b, "- %s in the %s: %s\n", is.Word, is.Field, is.Detail)
		} else {
			fmt.Fprintf(&b, "- the %s: %s\n", is.Field, is.Detail)
		}
	}
	for _, a := range it.Audio {
		fmt.Fprintf(&b, "- speech recognition heard %q when the %s audio was played back\n", a.Transcript, strings.ToLower(strings.TrimSuffix(a.Field, "Audio")))
	}
	b.WriteString("If the card is wrong, correct it. If it is right but the example can be read another way without the vowel marks, rewrite the example so it reads only one way.")
	return b.String()
}

type Summary struct {
	Accepted  int
	Edited    int
	Rewritten int
	Skipped   int
	Left      int
}

type Session struct {
	In      *bufio.Reader
	Out     io.Writer
	Save    func([]notes.Note) error
	Edit    func(notes.Note) (notes.Note, error)
	Rewrite func(ctx context.Context, n notes.Note, feedback string) (notes.Note, error)
	Clip    func(notes.Note) string
	Play    func(path string) error
}

func (s *Session) Run(ctx context.Context, ns []notes.Note, items []Item) (Summary, error) {
	var sum Summary
	for k, it := range items {
		quit, err := s.review(ctx, ns, it, k, len(items), &sum)
		if err != nil {
			return sum, err
		}
		if quit {
			sum.Left = len(items) - k
			return sum, nil
		}
	}
	return sum, nil
}

func (s *Session) ask(prompt string) (string, error) {
	fmt.Fprint(s.Out, prompt)
	line, err := s.In.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (s *Session) review(ctx context.Context, ns []notes.Note, it Item, k, total int, sum *Summary) (bool, error) {
	for {
		n := ns[it.Index]
		s.show(n, it, k, total)
		clip := ""
		if s.Clip != nil && s.Play != nil {
			clip = s.Clip(n)
		}
		line, err := s.ask(s.menu(it, clip != ""))
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		fields := strings.Fields(strings.ReplaceAll(line, ",", " "))
		cmd := ""
		if len(fields) > 0 {
			cmd = strings.ToLower(fields[0])
		}
		switch cmd {
		case "a":
			picked, err := pick(fields[1:], it.Len())
			if err != nil {
				fmt.Fprintln(s.Out, err)
				continue
			}
			rest := accept(&ns[it.Index], it, picked)
			if err := s.Save(ns); err != nil {
				return false, err
			}
			if rest.Len() > 0 {
				it = rest
				continue
			}
			sum.Accepted++
			return false, nil
		case "e":
			if s.Edit == nil {
				fmt.Fprintln(s.Out, "editing is not available")
				continue
			}
			edited, err := s.Edit(n)
			if err != nil {
				fmt.Fprintf(s.Out, "not saved: %v\n", err)
				continue
			}
			if reflect.DeepEqual(edited, n) {
				fmt.Fprintln(s.Out, "no changes")
				continue
			}
			if err := s.replace(ns, it.Index, edited); err != nil {
				fmt.Fprintf(s.Out, "not saved: %v\n", err)
				continue
			}
			sum.Edited++
			return false, nil
		case "c":
			if s.Rewrite == nil {
				fmt.Fprintln(s.Out, "Claude Code is not available; install it and log in with 'claude'")
				continue
			}
			fmt.Fprintln(s.Out, "asking Claude Code for a new version…")
			fresh, err := s.Rewrite(ctx, n, Feedback(it))
			if err != nil {
				fmt.Fprintf(s.Out, "no new version: %v\n", err)
				continue
			}
			fmt.Fprintln(s.Out, "\nnew version:")
			s.showNote(fresh)
			answer, err := s.ask("keep it? [y/N] ")
			if err != nil && !errors.Is(err, io.EOF) {
				return false, err
			}
			if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
				fmt.Fprintln(s.Out, "kept the current version")
				continue
			}
			if err := s.replace(ns, it.Index, fresh); err != nil {
				fmt.Fprintf(s.Out, "not saved: %v\n", err)
				continue
			}
			sum.Rewritten++
			return false, nil
		case "p":
			if clip == "" {
				fmt.Fprintln(s.Out, "no example audio for this note yet")
				continue
			}
			if err := s.Play(clip); err != nil {
				fmt.Fprintf(s.Out, "could not play %s: %v\n", clip, err)
			}
			continue
		case "s", "":
			sum.Skipped++
			return false, nil
		case "q":
			return true, nil
		default:
			fmt.Fprintf(s.Out, "unknown choice %q\n", line)
		}
	}
}

func (s *Session) replace(ns []notes.Note, i int, n notes.Note) error {
	if n.ID != ns[i].ID {
		return errors.New("the id cannot change: it is the note's identity in Anki")
	}
	old := ns[i]
	ns[i] = n
	if err := notes.Validate(ns); err != nil {
		ns[i] = old
		return err
	}
	return s.Save(ns)
}

func pick(args []string, total int) ([]int, error) {
	if len(args) == 0 {
		all := make([]int, total)
		for i := range all {
			all[i] = i + 1
		}
		return all, nil
	}
	var out []int
	for _, a := range args {
		v, err := strconv.Atoi(a)
		if err != nil || v < 1 || v > total {
			return nil, fmt.Errorf("pick flags between 1 and %d, like: a %d", total, total)
		}
		out = append(out, v)
	}
	return out, nil
}

func accept(n *notes.Note, it Item, picked []int) Item {
	for _, p := range picked {
		if p <= len(it.Issues) {
			if key := deck.IssueKey(it.Issues[p-1]); !slices.Contains(n.Reviewed, key) {
				n.Reviewed = append(n.Reviewed, key)
			}
			continue
		}
		if file := it.Audio[p-1-len(it.Issues)].File; !slices.Contains(n.ReviewedAudio, file) {
			n.ReviewedAudio = append(n.ReviewedAudio, file)
		}
	}
	rest := Item{Index: it.Index}
	for _, is := range it.Issues {
		if !slices.Contains(n.Reviewed, deck.IssueKey(is)) {
			rest.Issues = append(rest.Issues, is)
		}
	}
	for _, a := range it.Audio {
		if !slices.Contains(n.ReviewedAudio, a.File) {
			rest.Audio = append(rest.Audio, a)
		}
	}
	return rest
}

func (s *Session) show(n notes.Note, it Item, k, total int) {
	fmt.Fprintf(s.Out, "\n── %d of %d · position %d ──\n", k+1, total, n.Position)
	s.showNote(n)
	fmt.Fprintln(s.Out)
	i := 1
	for _, is := range it.Issues {
		word := is.Word
		if word == "" {
			word = "—"
		}
		fmt.Fprintf(s.Out, " %2d  %-5s  %-7s  %s  %s\n", i, is.Severity, is.Field, word, is.Detail)
		i++
	}
	for _, a := range it.Audio {
		fmt.Fprintf(s.Out, " %2d  audio  %-7s  heard: %s\n", i, strings.ToLower(strings.TrimSuffix(a.Field, "Audio")), a.Transcript)
		i++
	}
}

func (s *Session) showNote(n notes.Note) {
	fmt.Fprintf(s.Out, "%s  (%s) %s", n.Arabic, n.Pos, n.English)
	if n.Hint != "" {
		fmt.Fprintf(s.Out, "  %s", n.Hint)
	}
	fmt.Fprintln(s.Out)
	if forms := formsText(n); forms != "" {
		fmt.Fprintf(s.Out, "  forms    %s\n", forms)
	}
	fmt.Fprintf(s.Out, "  example  %s\n", deck.PlainText(n.Example))
	fmt.Fprintf(s.Out, "           %s\n", n.ExampleEn)
	if n.Comment != "" {
		fmt.Fprintf(s.Out, "  comment  %s\n", n.Comment)
	}
}

func formsText(n notes.Note) string {
	var parts []string
	for _, f := range n.Forms {
		parts = append(parts, f.Label+" "+f.Arabic)
	}
	return strings.Join(parts, "، ")
}

func (s *Session) menu(it Item, playable bool) string {
	options := []string{"[a] accept all"}
	if it.Len() > 1 {
		options = append(options, "[a N] accept flag N")
	}
	options = append(options, "[e] edit")
	if s.Rewrite != nil {
		options = append(options, "[c] ask Claude Code")
	}
	if playable {
		options = append(options, "[p] play example")
	}
	options = append(options, "[s] skip", "[q] quit")
	return strings.Join(options, "  ") + "\n> "
}
