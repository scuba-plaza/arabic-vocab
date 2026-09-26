package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
		Short: "Walk through flagged notes and decide what to do with each",
		Long: "Show every note that still has an unreviewed flag from 'arabic-vocab check'\n" +
			"or 'arabic-vocab audio', one at a time, and choose what to do with it:\n" +
			"  a        accept every flag: the words and clips go to the note's reviewed\n" +
			"           and reviewed_audio lists, so the tags disappear on the next build\n" +
			"  a 1 3    accept only the flags with these numbers\n" +
			"  e        edit the note as JSON in $VISUAL or $EDITOR\n" +
			"  c        ask Claude Code for a new version, telling it what was flagged\n" +
			"  p        play the example audio (needs ffplay, which comes with ffmpeg)\n" +
			"  s        skip the note; Enter does the same\n" +
			"  q        quit\n\n" +
			"Every decision is saved to notes.jsonl straight away. Edited and rewritten\n" +
			"notes are checked again by the next 'arabic-vocab check'.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
				infof("%d notes changed since the last check and are left out; run 'arabic-vocab check' to include them\n", stale)
			}
			if len(items) == 0 {
				infof("nothing left to review\n")
				return nil
			}
			scratch, err := os.MkdirTemp("", "arabic-vocab-review-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(scratch)
			s := &review.Session{
				In:   bufio.NewReader(cmd.InOrStdin()),
				Out:  cmd.OutOrStdout(),
				Save: func(ns []notes.Note) error { return notes.WriteJSONL(paths.Notes(), ns) },
				Edit: func(n notes.Note) (notes.Note, error) { return editNote(scratch, n) },
				Clip: func(n notes.Note) string { return exampleClip(paths, index, n) },
				Play: playClip,
			}
			if path, err := exec.LookPath(claude); err == nil {
				s.Rewrite = rewriter(paths, ns, curate.ClaudeCode{Path: path, Model: model, Effort: effort})
			}
			infof("%d notes to review\n", len(items))
			sum, runErr := s.Run(cmd.Context(), ns, items)
			infof("\naccepted %d, edited %d, rewritten by Claude Code %d, skipped %d, not reached %d\n",
				sum.Accepted, sum.Edited, sum.Rewritten, sum.Skipped, sum.Left)
			switch {
			case sum.Edited+sum.Rewritten > 0:
				infof("next: arabic-vocab check, then arabic-vocab audio and arabic-vocab build\n")
			case sum.Accepted > 0:
				infof("next: arabic-vocab build\n")
			}
			return runErr
		},
	}
	f := cmd.Flags()
	f.BoolVar(&minor, "minor", true, "include notes whose only flags are minor disagreements")
	f.StringVar(&claude, "claude", "claude", "Claude Code executable used for new versions")
	f.StringVar(&model, "model", "", "model for Claude Code to use (default: Claude Code's own default)")
	f.StringVar(&effort, "effort", "", "effort level for Claude Code (default: Claude Code's own)")
	return cmd
}

func editNote(dir string, n notes.Note) (notes.Note, error) {
	path := filepath.Join(dir, fmt.Sprintf("note-%d.json", n.Position))
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(n); err != nil {
			return n, err
		}
		if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
			return n, err
		}
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	c := exec.Command(parts[0], append(parts[1:], path)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return n, fmt.Errorf("%s: %w", editor, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return n, err
	}
	var edited notes.Note
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&edited); err != nil {
		return n, fmt.Errorf("%v; your text is kept, press e to fix it", err)
	}
	os.Remove(path)
	return edited, nil
}

func exampleClip(paths *deck.Paths, index map[string]string, n notes.Note) string {
	for _, at := range deck.AudioTexts(n) {
		if at.Field != "ExampleAudio" {
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

func playClip(path string) error {
	player, err := exec.LookPath("ffplay")
	if err != nil {
		return errors.New("ffplay not found; it comes with ffmpeg")
	}
	return exec.Command(player, "-nodisp", "-autoexit", "-loglevel", "error", path).Run()
}

func rewriter(paths *deck.Paths, ns []notes.Note, model curate.Model) func(context.Context, notes.Note, string) (notes.Note, error) {
	var (
		records map[string]*rank.Record
		system  string
	)
	return func(ctx context.Context, n notes.Note, feedback string) (notes.Note, error) {
		if system == "" {
			recs, err := notes.ReadJSONL[rank.Record](paths.Lexicon())
			if err != nil {
				return n, err
			}
			records, system = curateContext(ns, recs, curate.DefaultVocabulary, curate.DefaultExamples)
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
