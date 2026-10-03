package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-tts/arabic"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
	"github.com/scuba-plaza/arabic-vocab/internal/tashkeel"
)

const notFound = "no Modern Standard Arabic entry in Wiktionary (names and senses marked archaic, rare, classical or dialectal are left out, and vowels you type must match the entry's)"

var (
	errStopped  = errors.New("stopped")
	errNoAnswer = errors.New("no answer: standard input is closed; answer in a terminal, or pass --yes to skip the questions")
)

var arabicDigits = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9")

func wordList(args []string, file string) ([]string, error) {
	lines := slices.Clone(args)
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line, _, _ = strings.Cut(line, "#")
			lines = append(lines, line)
		}
	}
	var out []string
	for _, line := range lines {
		for _, w := range strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(",،;؛\n", r) }) {
			w = deck.NormalizeWord(w)
			if w == "" || slices.Contains(out, w) {
				continue
			}
			if !arabic.HasArabic(w) {
				return nil, usagef("%q is not Arabic text", w)
			}
			out = append(out, w)
		}
	}
	return out, nil
}

type asker struct {
	ctx context.Context
	in  *bufio.Reader
	out io.Writer
}

func (a *asker) ask(prompt string) (string, error) {
	fmt.Fprint(a.out, prompt)
	type reply struct {
		line string
		err  error
	}
	replies := make(chan reply, 1)
	go func() {
		line, err := a.in.ReadString('\n')
		replies <- reply{line, err}
	}()
	select {
	case <-a.ctx.Done():
		fmt.Fprintln(a.out)
		return "", fmt.Errorf("interrupted: %w", a.ctx.Err())
	case r := <-replies:
		if r.err != nil && (!errors.Is(r.err, io.EOF) || strings.TrimSpace(r.line) == "") {
			if errors.Is(r.err, io.EOF) {
				return "", errNoAnswer
			}
			return "", r.err
		}
		return strings.TrimSpace(r.line), nil
	}
}

func (a *asker) confirm(prompt string) (bool, error) {
	s, err := a.ask(prompt)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(s) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

type verdict int

const (
	pick verdict = iota
	skip
	quit
)

func parseAnswer(s string, n int) ([]int, verdict, error) {
	switch strings.ToLower(s) {
	case "":
		return []int{0}, pick, nil
	case "s", "skip":
		return nil, skip, nil
	case "q", "quit":
		return nil, quit, nil
	case "a", "all":
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return all, pick, nil
	}
	var picks []int
	for _, f := range strings.FieldsFunc(arabicDigits.Replace(s), func(r rune) bool { return r == ',' || r == '،' || unicode.IsSpace(r) }) {
		k, err := strconv.Atoi(f)
		if err != nil || k < 1 || k > n {
			return nil, pick, fmt.Errorf("%q is not a number from 1 to %d", f, n)
		}
		if !slices.Contains(picks, k-1) {
			picks = append(picks, k-1)
		}
	}
	if len(picks) == 0 {
		return nil, pick, fmt.Errorf("choose a number from 1 to %d", n)
	}
	return picks, pick, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func describe(c deck.Candidate) string {
	e := c.Main()
	meta := []string{e.Pos}
	if e.Gender != "" {
		meta = append(meta, e.Gender)
	}
	if e.VerbForm != "" {
		meta = append(meta, "form "+e.VerbForm)
	}
	var glosses, shown []string
	for _, s := range c.Senses() {
		if len(glosses) == 3 {
			break
		}
		g := s.Gloss
		for _, parent := range shown {
			if rest, ok := strings.CutPrefix(g, parent+" › "); ok {
				g = rest
				break
			}
		}
		shown = append(shown, s.Gloss)
		glosses = append(glosses, clip(g, 60))
	}
	return fmt.Sprintf("%s  %s  %s", e.Canonical, strings.Join(meta, ", "), strings.Join(glosses, "; "))
}

func (a *asker) choose(word string, cands []deck.Candidate, planner *deck.Planner) ([]deck.Candidate, verdict, error) {
	fmt.Fprintf(a.out, "%s has %d senses; which one do you mean?\n", word, len(cands))
	for i, c := range cands {
		line := describe(c)
		if n, ok := planner.Existing(c); ok {
			line += fmt.Sprintf("  [already in the deck, position %d]", n.Position)
		}
		fmt.Fprintf(a.out, "  %d  %s\n", i+1, line)
	}
	prompt := fmt.Sprintf("choose 1-%d, several like 1,3, a for all, s to skip, q to quit [1]: ", len(cands))
	for {
		s, err := a.ask(prompt)
		if err != nil {
			return nil, pick, err
		}
		picks, v, err := parseAnswer(s, len(cands))
		if err != nil {
			fmt.Fprintln(a.out, err)
			continue
		}
		var chosen []deck.Candidate
		for _, i := range picks {
			chosen = append(chosen, cands[i])
		}
		return chosen, v, nil
	}
}

func lookupDump(ctx context.Context, a *asker, paths *deck.Paths, words []string, yes bool) ([]*lexicon.Entry, error) {
	src := deck.KaikkiSource(*paths)
	if st, err := os.Stat(src.Path); err != nil || st.Size() == 0 {
		if !yes {
			fmt.Fprintf(a.out, "To find every sense of a spelling, and words outside the ranked list, add needs Wiktionary's Arabic dump from kaikki.org: a download of about 500 MB, kept in %s for later runs ('rank' uses it too).\n", src.Path)
			ok, err := a.confirm("download it now? [y/N]: ")
			if err != nil {
				return nil, err
			}
			if !ok {
				fmt.Fprintln(a.out, "using the ranked list only")
				return nil, nil
			}
		}
		if err := downloadSource(ctx, src, false); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	skeletons := map[string]bool{}
	for _, w := range words {
		skeletons[tashkeel.Fold(tashkeel.Skeleton(w))] = true
	}
	infof("reading the Wiktionary dump\n")
	entries, err := lexicon.ReadWhere(f, func(title string) bool { return skeletons[tashkeel.Fold(tashkeel.Skeleton(title))] })
	if entries == nil {
		entries = []*lexicon.Entry{}
	}
	return entries, err
}

type wordPlan struct {
	notes    []notes.Note
	targets  []int
	context  map[string]*rank.Record
	feedback map[int]string
	missing  []string
}

func planWords(ctx context.Context, cmd *cobra.Command, paths *deck.Paths, existing []notes.Note, records []rank.Record, words []string, yes bool) (*wordPlan, error) {
	a := &asker{ctx: ctx, in: bufio.NewReader(cmd.InOrStdin()), out: cmd.ErrOrStderr()}
	entries, err := lookupDump(ctx, a, paths, words, yes)
	if err != nil {
		return nil, err
	}

	planner := deck.NewPlanner(existing)
	plan := &wordPlan{context: map[string]*rank.Record{}, feedback: map[int]string{}}
	for _, w := range words {
		cands := deck.Find(w, records, entries)
		if len(cands) == 0 {
			if entries == nil {
				fmt.Fprintf(a.out, "%s: not in the ranked list, and the Wiktionary dump was not downloaded\n", w)
			} else {
				fmt.Fprintf(a.out, "%s: %s\n", w, notFound)
			}
			plan.missing = append(plan.missing, w)
			continue
		}
		picked := cands
		switch {
		case len(cands) == 1:
		case yes:
			picked = cands[:1]
		default:
			chosen, v, err := a.choose(w, cands, planner)
			if err != nil {
				return nil, err
			}
			switch v {
			case quit:
				return nil, errStopped
			case skip:
				continue
			}
			picked = chosen
		}
		for _, c := range picked {
			p := planner.Add(c)
			switch {
			case p.Present:
				infof("%s: already in the deck as %s (%s), position %d\n", w, p.Note.Arabic, p.Note.English, p.Note.Position)
			case p.Resumed:
				infof("%s: finishing the unwritten note at position %d\n", w, p.Note.Position)
			default:
				infof("%s: %s\n", w, describe(c))
			}
			if p.Present {
				continue
			}
			plan.context[p.Note.ID] = p.Context
			plan.feedback[p.Note.Position] = p.Feedback
		}
	}
	plan.notes, plan.targets = planner.Notes()
	if err := notes.Validate(plan.notes); err != nil {
		return nil, err
	}
	return plan, nil
}
