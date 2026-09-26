package cli

import (
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/curate"
	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func newCurateCommand(paths *deck.Paths) *cobra.Command {
	var (
		from, to    int
		model       string
		effort      string
		maxTokens   int64
		concurrency int
		attempts    int
		vocabulary  int
		examples    int
		fallbacks   bool
		redo        bool
	)
	cmd := &cobra.Command{
		Use:   "curate",
		Short: "Write glosses and example sentences with the Claude API",
		Long: "Send every note in --from..--to that has no gloss or example yet to the\n" +
			"Claude API together with its Wiktionary entries, and store the returned\n" +
			"gloss, hint, forms and fully vowelled example sentence in notes.jsonl.\n" +
			"The model follows internal/curate/guide.md, sees a few finished notes as\n" +
			"examples, and is asked to build its sentences from the most frequent words.\n\n" +
			"Uses the Anthropic SDK's usual credentials, such as ANTHROPIC_API_KEY, and\n" +
			"the model from --model or ANTHROPIC_MODEL.\n" +
			"notes.jsonl is saved after every note, so an interrupted run can simply be\n" +
			"started again. Run 'arabic-vocab check' afterwards: the model's vowels are\n" +
			"cross-checked like any other.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if model == "" {
				model = os.Getenv("ANTHROPIC_MODEL")
			}
			if model == "" {
				return usagef("choose a model with --model or ANTHROPIC_MODEL")
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
			infof("curating %d notes with %s\n", len(targets), model)
			client := anthropic.NewClient(option.WithMaxRetries(4))
			res, runErr := curate.Run(cmd.Context(), &client.Beta.Messages, ns, targets, byID, curate.Options{
				Model:       model,
				MaxTokens:   maxTokens,
				Effort:      effort,
				Fallbacks:   fallbacks,
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
				infof("curated %d notes; tokens: %d input, %d cache reads, %d cache writes, %d output", res.Curated, u.Input, u.CacheRead, u.CacheWrite, u.Output)
				if u.Fallbacks > 0 {
					infof("; %d answered by a fallback model", u.Fallbacks)
				}
				infof("\n")
				for _, f := range res.Failed {
					fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%v\n", f.Position, f.ID, f.Err)
				}
			}
			if runErr != nil {
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
	f.StringVar(&model, "model", "", "Claude model ID (default $ANTHROPIC_MODEL)")
	f.StringVar(&effort, "effort", "", "output effort: low, medium, high, xhigh or max (default: the model's own)")
	f.Int64Var(&maxTokens, "max-tokens", 8192, "output token limit per note")
	f.IntVar(&concurrency, "concurrency", 4, "parallel requests")
	f.IntVar(&attempts, "attempts", 2, "requests per note before giving up on a malformed answer")
	f.IntVar(&vocabulary, "vocabulary", 1000, "offer the model this many top-ranked words to build sentences from")
	f.IntVar(&examples, "examples", 8, "finished notes to show the model as examples")
	f.BoolVar(&fallbacks, "fallbacks", true, "let the API answer on a fallback model when the chosen model declines")
	f.BoolVar(&redo, "redo", false, "also rewrite notes that already have a gloss and example")
	return cmd
}
