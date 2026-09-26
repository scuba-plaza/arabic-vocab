package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/curate"
	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func newCurateCommand(paths *deck.Paths) *cobra.Command {
	var (
		from, to    int
		claude      string
		model       string
		effort      string
		batch       int
		concurrency int
		attempts    int
		vocabulary  int
		examples    int
		redo        bool
	)
	cmd := &cobra.Command{
		Use:   "curate",
		Short: "Write glosses and example sentences with Claude Code",
		Long: "Send every note in --from..--to that has no gloss or example yet to Claude\n" +
			"Code, in batches, together with its Wiktionary entries, and store the\n" +
			"returned gloss, hint, forms and fully vowelled example sentence in\n" +
			"notes.jsonl. The model follows internal/curate/guide.md, sees a few finished\n" +
			"notes as examples, and is asked to build its sentences from the most\n" +
			"frequent words.\n\n" +
			"Runs 'claude -p' with your Claude Code login, so a Pro or Max subscription\n" +
			"is enough; no API key is needed. Usage counts towards your plan's limits.\n" +
			"notes.jsonl is saved after every note, so a run that stops at a usage limit\n" +
			"can simply be started again later. Run 'arabic-vocab check' afterwards: the\n" +
			"model's vowels are cross-checked like any other.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := exec.LookPath(claude)
			if err != nil {
				return usagef("%s not found: install Claude Code and log in with 'claude', or pass --claude", claude)
			}
			ns, err := loadNotes(paths.Notes())
			if err != nil {
				return err
			}
			records, err := loadRecords(paths.Lexicon())
			if err != nil {
				return err
			}
			byID := map[string]*rank.Record{}
			var vocab []string
			for i := range records {
				byID[records[i].ID] = &records[i]
				if records[i].Rank <= vocabulary {
					vocab = append(vocab, records[i].ID)
				}
			}
			targets := curate.Targets(ns, from, to, redo)
			if len(targets) == 0 {
				infof("every note in %d..%d already has a gloss and example; pass --redo to write them again\n", from, to)
				return nil
			}
			infof("curating %d notes with Claude Code, %d per request\n", len(targets), batch)
			res, runErr := curate.Run(cmd.Context(), curate.ClaudeCode{Path: path, Model: model, Effort: effort}, ns, targets, byID, curate.Options{
				Batch:       batch,
				Concurrency: concurrency,
				Attempts:    attempts,
				System:      curate.System(curate.PickExamples(ns, examples), vocab),
				Save:        func(ns []notes.Note) error { return notes.WriteJSONL(paths.Notes(), ns) },
				Progress: func(done, total int, n notes.Note, err error) {
					status := n.English
					if err != nil {
						status = "failed: " + err.Error()
					}
					infof("[%d/%d] %d %s  %s\n", done, total, n.Position, n.Arabic, status)
				},
			})
			if res != nil {
				u := res.Usage
				infof("curated %d notes in %d requests; tokens: %d input, %d cache reads, %d cache writes, %d output", res.Curated, u.Calls, u.Input, u.CacheRead, u.CacheWrite, u.Output)
				if len(u.Models) > 0 {
					infof("; model %s", strings.Join(u.Models, ", "))
				}
				infof("\n")
				for _, f := range res.Failed {
					fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%v\n", f.Position, f.ID, f.Err)
				}
			}
			if runErr != nil {
				if res != nil && res.Curated > 0 {
					infof("the %d finished notes are saved; run the same command again to continue\n", res.Curated)
				}
				return runErr
			}
			if len(res.Failed) > 0 {
				return fmt.Errorf("%d notes could not be curated; they are listed above and stay empty in notes.jsonl", len(res.Failed))
			}
			infof("next: arabic-vocab check\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&from, "from", 1, "first position")
	f.IntVar(&to, "to", 100, "last position")
	f.StringVar(&claude, "claude", "claude", "Claude Code executable")
	f.StringVar(&model, "model", "", "model for Claude Code to use (default: Claude Code's own default)")
	f.StringVar(&effort, "effort", "", "effort level: low, medium, high, xhigh or max (default: Claude Code's own)")
	f.IntVar(&batch, "batch", 10, "notes per request")
	f.IntVar(&concurrency, "concurrency", 2, "requests running at the same time")
	f.IntVar(&attempts, "attempts", 2, "requests per note before giving up on a malformed answer")
	f.IntVar(&vocabulary, "vocabulary", 1000, "offer the model this many top-ranked words to build sentences from")
	f.IntVar(&examples, "examples", 8, "finished notes to show the model as examples")
	f.BoolVar(&redo, "redo", false, "also rewrite notes that already have a gloss and example")
	return cmd
}
