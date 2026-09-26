package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/curate"
	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
	"github.com/scuba-plaza/arabic-vocab/internal/review"
)

func newReviewCommand(paths *deck.Paths) *cobra.Command {
	var (
		minor  bool
		claude string
		model  string
		effort string
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Go through flagged notes and decide what to do with each",
		Long: "Open a full-screen review of every note that 'arabic-vocab check' or\n" +
			"'arabic-vocab audio' flagged and you have not dealt with yet. Each note shows\n" +
			"what was flagged in plain words: the card's vowels next to the readings of\n" +
			"CATT and CAMeL with the differing letters highlighted, or the words speech\n" +
			"recognition heard. For each note you can\n" +
			"  enter  say the card is right, so its flags stay out of the next build\n" +
			"  e      edit the note as JSON in $VISUAL or $EDITOR\n" +
			"  c      ask Claude Code for a better version, and keep it or not\n" +
			"  p / w  listen to the sentence or the word (needs ffplay from ffmpeg)\n" +
			"  u      undo your last decision\n\n" +
			"Every decision is saved to notes.jsonl straight away, so you can quit at any\n" +
			"time and continue later. Edited notes are checked again by the next check.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
				return usagef("review needs an interactive terminal")
			}
			ns, err := loadNotes(paths.Notes())
			if err != nil {
				return err
			}
			checks, err := notes.ReadJSONL[notes.Check](paths.QA())
			if err != nil {
				return err
			}
			audioChecks, err := notes.ReadJSONL[notes.AudioCheck](paths.AudioQA())
			if err != nil {
				return err
			}
			manifest, err := notes.ReadJSONL[deck.ManifestEntry](paths.Manifest())
			if err != nil {
				return err
			}
			index := deck.AudioIndex(manifest)
			items, stale := review.Items(ns, checks, audioChecks, index, minor)
			if stale > 0 {
				defer infof("%s new or changed since the last check and not shown; 'arabic-vocab check' checks them\n", count(stale, "note is", "notes are"))
			}
			if len(items) == 0 {
				infof("nothing to review\n")
				return nil
			}
			scratch, err := os.MkdirTemp("", "arabic-vocab-review-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(scratch)
			opts := review.Options{
				Save:       func(ns []notes.Note) error { return notes.WriteJSONL(paths.Notes(), ns) },
				Clip:       func(n notes.Note, field string) string { return clip(paths, index, n, field) },
				Play:       playClip,
				Editor:     editor(),
				ScratchDir: scratch,
			}
			if path, err := exec.LookPath(claude); err == nil {
				opts.Rewrite = rewriter(paths, ns, curate.ClaudeCode{Path: path, Model: model, Effort: effort})
			}
			sum, err := review.Run(cmd.Context(), ns, items, opts)
			infof("%s: %d marked right, %d edited, %d rewritten by Claude Code, %d still open\n",
				count(sum.Total, "flagged note", "flagged notes"), sum.Kept, sum.Edited, sum.Rewritten, sum.Open)
			if next := review.NextSteps(sum); next != "" {
				infof("%s\n", next)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&minor, "minor", true, "include notes whose only flags are minor disagreements")
	f.StringVar(&claude, "claude", "claude", "Claude Code executable used for new versions")
	f.StringVar(&model, "model", "", "model for Claude Code to use (default: Claude Code's own default)")
	f.StringVar(&effort, "effort", "", "effort level for Claude Code (default: Claude Code's own)")
	return cmd
}

func editor() []string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if fields := strings.Fields(os.Getenv(name)); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

func clip(paths *deck.Paths, index map[string]string, n notes.Note, field string) string {
	for _, at := range deck.AudioTexts(n) {
		if at.Field != field {
			continue
		}
		file, ok := index[at.Text]
		if !ok {
			return ""
		}
		path := paths.MediaFile(file)
		if _, err := os.Stat(path); err != nil {
			return ""
		}
		return path
	}
	return ""
}

func playClip(ctx context.Context, path string) error {
	player, err := exec.LookPath("ffplay")
	if err != nil {
		return errors.New("ffplay not found; it comes with ffmpeg")
	}
	return exec.CommandContext(ctx, player, "-nodisp", "-autoexit", "-loglevel", "error", path).Run()
}

func rewriter(paths *deck.Paths, ns []notes.Note, model curate.Model) func(context.Context, notes.Note, string) (notes.Note, error) {
	snapshot := slices.Clone(ns)
	var (
		once    sync.Once
		records map[string]*rank.Record
		system  string
		loadErr error
	)
	return func(ctx context.Context, n notes.Note, feedback string) (notes.Note, error) {
		once.Do(func() {
			recs, err := notes.ReadJSONL[rank.Record](paths.Lexicon())
			if err != nil {
				loadErr = err
				return
			}
			records, system = curateContext(snapshot, recs, curate.DefaultVocabulary, curate.DefaultExamples)
		})
		if loadErr != nil {
			return n, loadErr
		}
		one := []notes.Note{n}
		res, err := curate.Run(ctx, model, one, []int{0}, records, curate.Options{
			Batch: 1, Concurrency: 1, System: system,
			Feedback: map[int]string{n.Position: feedback},
		})
		if err != nil {
			return n, err
		}
		if len(res.Failed) > 0 {
			return n, res.Failed[0].Err
		}
		return one[0], nil
	}
}
