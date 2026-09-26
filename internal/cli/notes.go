package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func loadRecords(path string) ([]rank.Record, error) {
	records, err := notes.ReadJSONL[rank.Record](path)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s is empty; run 'arabic-vocab rank' first", path)
	}
	return records, nil
}

func loadNotes(path string) ([]notes.Note, error) {
	ns, err := notes.ReadJSONL[notes.Note](path)
	if err != nil {
		return nil, err
	}
	if len(ns) == 0 {
		return nil, fmt.Errorf("%s has no notes; run 'arabic-vocab prepare' first", path)
	}
	if err := notes.Validate(ns); err != nil {
		return nil, err
	}
	notes.Sort(ns)
	return ns, nil
}

func newPrepareCommand(paths *deck.Paths) *cobra.Command {
	var from, to int
	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Add note skeletons for a rank range to notes.jsonl",
		Long: "Add a note for every ranked lemma in --from..--to that notes.jsonl does not\n" +
			"have yet. The headword, forms, gender, root and verb form come from\n" +
			"Wiktionary; the English gloss, hint and example sentence stay empty for\n" +
			"'arabic-vocab curate' or a person to fill in. Existing notes are never touched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := loadRecords(paths.Lexicon())
			if err != nil {
				return err
			}
			existing, err := notes.ReadJSONL[notes.Note](paths.Notes())
			if err != nil {
				return err
			}
			all, added := deck.Prepare(existing, records, from, to)
			if err := notes.WriteJSONL(paths.Notes(), all); err != nil {
				return err
			}
			pending := 0
			for _, n := range all {
				if !n.Authored() {
					pending++
				}
			}
			infof("added %d note(s); %d of %d still need a gloss and example\n", added, pending, len(all))
			return nil
		},
	}
	cmd.Flags().IntVar(&from, "from", 1, "first rank")
	cmd.Flags().IntVar(&to, "to", 100, "last rank")
	return cmd
}

func newShowCommand(paths *deck.Paths) *cobra.Command {
	var from, to int
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the Wiktionary entries for a rank range",
		Long: "Print every Wiktionary entry grouped under each ranked lemma, with its\n" +
			"senses. Senses marked x are tagged obsolete, classical, dialectal or similar\n" +
			"and should not be used for an MSA card.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := loadRecords(paths.Lexicon())
			if err != nil {
				return err
			}
			deck.WriteWorksheet(cmd.OutOrStdout(), records, from, to)
			return nil
		},
	}
	cmd.Flags().IntVar(&from, "from", 1, "first rank")
	cmd.Flags().IntVar(&to, "to", 100, "last rank")
	return cmd
}
