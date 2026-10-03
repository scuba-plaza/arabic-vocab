package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
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
		file        string
		yes         bool
		claude      string
		model       string
		effort      string
		batch       int
		concurrency int
	)
	cmd := &cobra.Command{
		Use:   "add [WORD...]",
		Short: "Add the next most common words, or the words you name, written by Claude Code",
		Long: "Add words to the deck. Without arguments, add the most common words of the\n" +
			"ranked list that the deck does not have yet. With words, add exactly those:\n" +
			"give them as arguments, separated by commas, or one per line in --file.\n\n" +
			"A spelling can stand for several words or senses, such as the noun ماء\n" +
			"\"water\" and the verb ماء \"to meow\". When more than one MSA entry fits a\n" +
			"word, add lists them and asks which one you mean; type the vowels to narrow\n" +
			"the list. The entries come from Wiktionary's Arabic dump, which add offers to\n" +
			"download once (about 500 MB, shared with 'rank'); without it only the ranked\n" +
			"list is searched. With --yes nothing is asked, so every word takes its likeliest\n" +
			"sense and the dump is downloaded only for words the ranked list lacks. The main sense of a ranked word keeps its rank in the deck;\n" +
			"every other named word goes after the ranked ones, and a word the deck\n" +
			"already has is left as it is.\n\n" +
			"The headword, its forms, gender and root come from Wiktionary; Claude Code\n" +
			"writes the English meaning, a hint where the meaning needs one, and a fully\n" +
			"vowelled example sentence built from common words, following\n" +
			"internal/curate/guide.md. Added words then go through check, review, audio\n" +
			"and build like every other word.\n\n" +
			"Runs 'claude -p' with your Claude Code login, so a Pro or Max subscription is\n" +
			"enough and no API key is needed; usage counts towards your plan's limits.\n" +
			"Every finished note is saved straight away. If a run stops early, at a usage\n" +
			"limit for example, add prints the command that writes the rest: plain add\n" +
			"continues with the words that were not written, and for named words you give\n" +
			"those words again.",
		Example: "  arabic-vocab add                  the next 100 words\n" +
			"  arabic-vocab add -n 20            the next 20 words\n" +
			"  arabic-vocab add كتاب عين         these two words, asking about senses\n" +
			"  arabic-vocab add --file words.txt every word in the file\n" +
			"  arabic-vocab add --yes ماء        no questions: the likeliest sense of every word",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			named, err := wordList(args, file)
			if err != nil {
				return err
			}
			if len(named) == 0 && file != "" {
				return usagef("%s has no words", file)
			}
			if len(named) == 0 && len(args) > 0 {
				return usagef("the words to add are empty; give Arabic words, or no words to add the next ranked ones")
			}
			if len(named) > 0 && cmd.Flags().Changed("count") {
				return usagef("--count and the words to add cannot be combined")
			}
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
			job := addJob{
				claude:      curate.ClaudeCode{Path: path, Model: model, Effort: effort},
				batch:       batch,
				concurrency: concurrency,
				existing:    existing,
				records:     records,
			}
			var missing []string
			if len(named) == 0 {
				ns, targets := deck.NextWords(existing, records, words)
				if len(targets) == 0 {
					infof("every ranked word is in the deck; 'arabic-vocab rank --limit N' ranks more\n")
					return nil
				}
				job.notes, job.targets = ns, targets
				job.announce = fmt.Sprintf("adding %s, positions %d to %d, with Claude Code (%d per request)",
					count(len(targets), "word", "words"), ns[targets[0]].Position, ns[targets[len(targets)-1]].Position, batch)
			} else {
				plan, err := planWords(cmd.Context(), cmd, paths, existing, records, named, yes)
				if errors.Is(err, errStopped) {
					infof("stopped; nothing was added\n")
					return nil
				}
				if err != nil {
					return err
				}
				missing = plan.missing
				if len(plan.targets) == 0 {
					if len(missing) > 0 {
						return fmt.Errorf("%s could not be found", count(len(missing), "word", "words"))
					}
					infof("nothing to add\n")
					return nil
				}
				job.notes, job.targets, job.context, job.feedback, job.typed = plan.notes, plan.targets, plan.context, plan.feedback, plan.typed
				job.announce = fmt.Sprintf("adding %s with Claude Code (%d per request)", count(len(plan.targets), "word", "words"), batch)
			}
			if err := job.run(cmd, paths); err != nil {
				return err
			}
			if len(missing) > 0 {
				return fmt.Errorf("%s could not be found and was not added: %s", count(len(missing), "word", "words"), strings.Join(missing, "، "))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVarP(&words, "count", "n", 100, "number of words to add")
	f.StringVarP(&file, "file", "f", "", "file with the words to add, one per line (# starts a comment)")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask: take the likeliest sense of every word; the Wiktionary dump is downloaded only for words the ranked list lacks")
	f.StringVar(&claude, "claude", "claude", "Claude Code executable")
	f.StringVar(&model, "model", "", "model for Claude Code to use (default: Claude Code's own default)")
	f.StringVar(&effort, "effort", "", "effort level: low, medium, high, xhigh or max (default: Claude Code's own)")
	f.IntVar(&batch, "batch", 10, "words per request to Claude Code")
	f.IntVar(&concurrency, "concurrency", 2, "requests running at the same time")
	return cmd
}

type addJob struct {
	claude      curate.ClaudeCode
	batch       int
	concurrency int
	existing    []notes.Note
	records     []rank.Record
	notes       []notes.Note
	targets     []int
	context     map[string]*rank.Record
	feedback    map[int]string
	typed       map[string]string
	announce    string
}

func (j addJob) unwritten() []string {
	var out []string
	for _, i := range j.targets {
		if j.notes[i].Authored() {
			continue
		}
		if w := j.typed[j.notes[i].ID]; w != "" && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

func (j addJob) resume() string {
	if len(j.typed) == 0 {
		return "arabic-vocab add"
	}
	words := j.unwritten()
	for i, w := range words {
		if strings.ContainsAny(w, " \t") {
			words[i] = `"` + w + `"`
		}
	}
	return strings.Join(append([]string{"arabic-vocab add"}, words...), " ")
}

func (j addJob) run(cmd *cobra.Command, paths *deck.Paths) error {
	save := func(ns []notes.Note) error {
		current, err := notes.ReadJSONL[notes.Note](paths.Notes())
		if err != nil {
			return err
		}
		for _, i := range j.targets {
			n := ns[i]
			if !n.Authored() {
				continue
			}
			if at := slices.IndexFunc(current, func(c notes.Note) bool { return c.ID == n.ID }); at >= 0 {
				current[at] = n
			} else {
				current = append(current, n)
			}
		}
		notes.Sort(current)
		if err := notes.Validate(current); err != nil {
			return err
		}
		return notes.WriteJSONL(paths.Notes(), current)
	}
	byID, system := curateContext(j.notes, j.records, curate.DefaultVocabulary, curate.DefaultExamples)
	for id, rec := range j.context {
		byID[id] = rec
	}
	infof("%s\n", j.announce)
	res, runErr := curate.Run(cmd.Context(), j.claude, j.notes, j.targets, byID, curate.Options{
		Batch:       j.batch,
		Concurrency: j.concurrency,
		Attempts:    2,
		System:      system,
		Feedback:    j.feedback,
		Accept:      j.accept,
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
		if res != nil && (res.Curated > 0 || len(j.typed) > 0) {
			infof("the finished notes are saved; to write the rest, run: %s\n", j.resume())
		}
		return runErr
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%s could not be written; they are listed above, and running '%s' tries them again", count(len(res.Failed), "word", "words"), j.resume())
	}
	infof("next: arabic-vocab check\n")
	return nil
}

func (j addJob) accept(draft notes.Note, card curate.Card) error {
	if j.feedback[draft.Position] == "" {
		return nil
	}
	return deck.CheckEntry(draft, card.Pos, card.Arabic)
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
