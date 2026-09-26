package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/curate"
	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
	"github.com/scuba-plaza/arabic-vocab/internal/review"
)

func newReviewCommand(paths *deck.Paths) *cobra.Command {
	var (
		minor     bool
		claude    string
		model     string
		effort    string
		noBrowser bool
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Go through flagged notes in your browser and decide what to do with each",
		Long: "Open a page in your browser, served only on this machine, with every note that\n" +
			"'arabic-vocab check' or 'arabic-vocab audio' flagged and you have not dealt\n" +
			"with yet. Each flag says in plain words what disagreed: the card's vowels\n" +
			"next to CATT's and CAMeL's readings with the differing letters highlighted,\n" +
			"or the words speech recognition heard. For each note you can\n" +
			"  enter  say the card is right, so its flags stay out of the next build\n" +
			"  e      edit the note\n" +
			"  c      ask Claude Code for a better version, and keep it or not\n" +
			"  p / w  listen to the sentence or the word\n" +
			"  u      undo your last decision\n\n" +
			"Every decision is saved to notes.jsonl straight away. Click 'Finish review'\n" +
			"on the page or press Ctrl+C here when you are done. Edited notes are checked\n" +
			"again by the next check.",
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
				defer infof("%s a fresh 'arabic-vocab check' and were not shown\n", count(stale, "note needs", "notes need"))
			}
			if len(items) == 0 {
				infof("nothing to review\n")
				return nil
			}
			opts := review.Options{
				Save:     func(ns []notes.Note) error { return notes.WriteJSONL(paths.Notes(), ns) },
				Clip:     func(n notes.Note, field string) string { return clip(paths, index, n, field) },
				FontPath: paths.Asset("ScheherazadeNew-Regular.ttf"),
			}
			if path, err := exec.LookPath(claude); err == nil {
				opts.Rewrite = rewriter(paths, ns, curate.ClaudeCode{Path: path, Model: model, Effort: effort})
			}
			sum, err := review.Serve(cmd.Context(), ns, items, opts, func(url string) {
				fmt.Fprintf(cmd.OutOrStdout(), "reviewing %s at %s\n", count(len(items), "flagged note", "flagged notes"), url)
				if !noBrowser {
					if err := openBrowser(url); err != nil {
						infof("could not open a browser (%v); open the address above yourself\n", err)
					}
				}
				infof("click 'Finish review' on the page or press Ctrl+C here when you are done\n")
			})
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
	f.BoolVar(&noBrowser, "no-browser", false, "only print the address instead of opening a browser")
	return cmd
}

func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
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
