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

func newAddCommand(paths *deck.Paths) *cobra.Command {
	var (
		words       int
		claude      string
		model       string
		effort      string
		batch       int
		concurrency int
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add the next most common words, written by Claude Code",
		Long: "Add the most common words of the ranked list that the deck does not have yet.\n" +
			"The headword, its forms, gender and root come from Wiktionary; Claude Code\n" +
			"writes the English meaning, a hint where the meaning needs one, and a fully\n" +
			"vowelled example sentence built from common words, following\n" +
			"internal/curate/guide.md.\n\n" +
			"Runs 'claude -p' with your Claude Code login, so a Pro or Max subscription is\n" +
			"enough and no API key is needed; usage counts towards your plan's limits.\n" +
			"Every finished note is saved straight away. If a run stops early, at a usage\n" +
			"limit for example, run add again: the words that were not written come first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if words < 1 {
				return usagef("--count must be at least 1")
			}
			path, err := exec.LookPath(claude)
			if err != nil {
				return usagef("%s not found: install Claude Code and log in with 'claude', or pass --claude", claude)
			}
			records, err := loadRecords(paths.Lexicon())
			if err != nil {
				return err
			}
			existing, err := notes.ReadJSONL[notes.Note](paths.Notes())
			if err != nil {
				return err
			}
			if err := notes.Validate(existing); err != nil {
				return err
			}
			notes.Sort(existing)
			ns, targets := deck.NextWords(existing, records, words)
			if len(targets) == 0 {
				infof("every ranked word is in the deck; 'arabic-vocab rank --limit N' ranks more\n")
				return nil
			}
			kept := map[string]bool{}
			for _, n := range existing {
				kept[n.ID] = true
			}
			save := func(ns []notes.Note) error {
				var out []notes.Note
				for _, n := range ns {
					if n.Authored() || kept[n.ID] {
						out = append(out, n)
					}
				}
				return notes.WriteJSONL(paths.Notes(), out)
			}
			byID, system := curateContext(ns, records, curate.DefaultVocabulary, curate.DefaultExamples)
			infof("adding %s, positions %d to %d, with Claude Code (%d per request)\n",
				count(len(targets), "word", "words"), ns[targets[0]].Position, ns[targets[len(targets)-1]].Position, batch)
			res, runErr := curate.Run(cmd.Context(), curate.ClaudeCode{Path: path, Model: model, Effort: effort}, ns, targets, byID, curate.Options{
				Batch:       batch,
				Concurrency: concurrency,
				Attempts:    2,
				System:      system,
				Save:        save,
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
				infof("wrote %s in %s; tokens: %d input, %d cache reads, %d cache writes, %d output",
					count(res.Curated, "note", "notes"), count(u.Calls, "request", "requests"), u.Input, u.CacheRead, u.CacheWrite, u.Output)
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
					infof("the finished notes are saved; run 'arabic-vocab add' again to write the rest\n")
				}
				return runErr
			}
			if len(res.Failed) > 0 {
				return fmt.Errorf("%s could not be written; they are listed above, and the next 'arabic-vocab add' tries them again", count(len(res.Failed), "word", "words"))
			}
			infof("next: arabic-vocab check\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVarP(&words, "count", "n", 100, "number of words to add")
	f.StringVar(&claude, "claude", "claude", "Claude Code executable")
	f.StringVar(&model, "model", "", "model for Claude Code to use (default: Claude Code's own default)")
	f.StringVar(&effort, "effort", "", "effort level: low, medium, high, xhigh or max (default: Claude Code's own)")
	f.IntVar(&batch, "batch", 10, "words per request to Claude Code")
	f.IntVar(&concurrency, "concurrency", 2, "requests running at the same time")
	return cmd
}

func curateContext(ns []notes.Note, records []rank.Record, vocabulary, examples int) (map[string]*rank.Record, string) {
	byID := map[string]*rank.Record{}
	var vocab []string
	for i := range records {
		byID[records[i].ID] = &records[i]
		if records[i].Rank <= vocabulary {
			vocab = append(vocab, records[i].ID)
		}
	}
	return byID, curate.System(curate.PickExamples(ns, examples), vocab)
}
