package cli

import (
	"context"
	"fmt"
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
		all       bool
		claude    string
		model     string
		effort    string
		noBrowser bool
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Go through flagged notes in your browser, or every note with --all",
		Long: "Open a page in your browser, served only on this machine, with every note that\n" +
			"'arabic-vocab check' or 'arabic-vocab audio' flagged and you have not dealt\n" +
			"with yet. Each flag says in plain words what disagreed: the card's vowels\n" +
			"next to CATT's and CAMeL's readings with the differing letters highlighted,\n" +
			"or which clip came back without any sound. For each note you can\n" +
			"  enter  say the card is right, so its flags stay out of the next build\n" +
			"  e      edit the note\n" +
			"  c      ask Claude Code for a better version, and keep it or not\n" +
			"  w/f/s  listen to the word, the forms or the sentence\n" +
			"  u      undo your last decision\n\n" +
			"With --all every written note is listed, flagged or not, so you can go\n" +
			"through the whole deck and edit any of it.\n\n" +
			"Each clip of a note also has buttons that remove its MP3 or synthesize it\n" +
			"again with Google Text-to-Speech, as 'arabic-vocab audio' does. A voice that\n" +
			"was unlucky once usually gets the word right on the next try; a clip that\n" +
			"comes back silent is asked for again, as 'arabic-vocab audio' does, and its\n" +
			"flag clears when the new clip has sound.\n\n" +
			"The voice itself can be picked from Google's ar-XA voices next to those\n" +
			"buttons. It is saved in deck.json, so it speaks every clip made from then\n" +
			"on, here and in later 'arabic-vocab audio' runs.\n\n" +
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
			audioChecks, err := notes.ReadJSONL[notes.Check](paths.AudioQA())
			if err != nil {
				return err
			}
			manifest, err := notes.ReadJSONL[deck.ManifestEntry](paths.Manifest())
			if err != nil {
				return err
			}
			settings, err := deck.LoadSettings(paths.Settings())
			if err != nil {
				return err
			}
			store := newClips(paths, settings.AudioVoice(), manifest, audioChecks)
			defer store.close()
			items, stale := review.Items(ns, checks, audioChecks, review.Filter{Minor: minor, All: all})
			noun := func(n int) string {
				if all {
					return count(n, "note", "notes")
				}
				return count(n, "flagged note", "flagged notes")
			}
			if stale > 0 {
				if all {
					defer infof("%s a fresh 'arabic-vocab check'; the flags shown for them are older than the note\n", count(stale, "note needs", "notes need"))
				} else {
					defer infof("%s a fresh 'arabic-vocab check' and were not shown\n", count(stale, "note needs", "notes need"))
				}
			}
			if left := unwritten(ns); all && left > 0 {
				defer infof("%s not written yet and were not shown; 'arabic-vocab add' writes them\n", count(left, "note is", "notes are"))
			}
			if len(items) == 0 {
				infof("nothing to review\n")
				return nil
			}
			opts := review.Options{
				Save:     func(ns []notes.Note) error { return notes.WriteJSONL(paths.Notes(), ns) },
				Clip:     store.path,
				Remake:   store.remake,
				Remove:   store.remove,
				Voice:    settings.Voice,
				Voices:   store.voiceOptions,
				SetVoice: store.setVoice,
				All:      all,
				FontPath: paths.Asset("ScheherazadeNew-Regular.ttf"),
			}
			if path, err := exec.LookPath(claude); err == nil {
				opts.Rewrite = rewriter(paths, ns, curate.ClaudeCode{Path: path, Model: model, Effort: effort})
			}
			sum, err := review.Serve(cmd.Context(), ns, items, opts, func(url string) {
				fmt.Fprintf(cmd.OutOrStdout(), "reviewing %s at %s\n", noun(len(items)), url)
				if !noBrowser {
					if err := openBrowser(url); err != nil {
						infof("could not open a browser (%v); open the address above yourself\n", err)
					}
				}
				infof("click 'Finish review' on the page or press Ctrl+C here when you are done\n")
			})
			infof("%s: %d marked right, %d edited, %d rewritten by Claude Code, %d still open\n",
				noun(sum.Total), sum.Kept, sum.Edited, sum.Rewritten, sum.Open)
			if next := review.NextSteps(sum); next != "" {
				infof("%s\n", next)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&minor, "minor", true, "include notes whose only flags are minor disagreements")
	f.BoolVar(&all, "all", false, "list every written note, not only the flagged ones, to review and edit")
	f.StringVar(&claude, "claude", "claude", "Claude Code executable used for new versions")
	f.StringVar(&model, "model", "", "model for Claude Code to use (default: Claude Code's own default)")
	f.StringVar(&effort, "effort", "", "effort level for Claude Code (default: Claude Code's own)")
	f.BoolVar(&noBrowser, "no-browser", false, "only print the address instead of opening a browser")
	googleFlags(cmd)
	return cmd
}

func unwritten(ns []notes.Note) int {
	n := 0
	for i := range ns {
		if !ns[i].Authored() {
			n++
		}
	}
	return n
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
