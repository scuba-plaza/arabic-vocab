package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/review"
)

func defaultPython() string {
	venv := filepath.Join(".venv", "bin", "python")
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	return "python3"
}

func newCheckCommand(paths *deck.Paths) *cobra.Command {
	var python string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Cross-check every diacritic against CAMeL and CATT",
		Long: "Check each note's headword, forms and example sentence word by word:\n" +
			"  - every letter must carry its vowel, sukun or shadda, unless it is never\n" +
			"    marked (a long vowel, the lam of ال, the last letter of a headword) or\n" +
			"    CAMeL or CATT read it without a vowel; a word's ending in a sentence\n" +
			"    only passes bare when CATT reads it so;\n" +
			"  - CAMeL's morphological analyzer must accept the exact vowelling;\n" +
			"  - in example sentences, CATT's independent vowelling of the bare sentence\n" +
			"    must agree, with CAMeL's contextual reading reported as a tie-breaker.\n\n" +
			"Results go to qa.jsonl. 'arabic-vocab review' shows every disagreement and lets\n" +
			"you decide on it; the ones you leave open are tagged check::diacritics in the\n" +
			"deck, with the details on the back of the card.\n\n" +
			"Needs the Python environment from 'make venv'.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ns, err := loadNotes(paths.Notes())
			if err != nil {
				return err
			}
			if python == "" {
				python = defaultPython()
			}
			items := deck.CheckItems(ns)
			infof("checking %d notes (%d fields) with %s\n", len(ns), len(items), python)
			results, err := deck.RunChecker(cmd.Context(), python, paths.CheckScript(), items, os.Stderr)
			if err != nil {
				return err
			}
			checks := deck.Evaluate(ns, items, results)
			if err := notes.WriteJSONL(paths.QA(), checks); err != nil {
				return err
			}
			flagged, _ := review.Items(ns, checks, nil, review.Filter{Minor: true})
			major := 0
			for _, it := range flagged {
				if it.Major() {
					major++
				}
			}
			infof("checked %s: %d with major flags, %d with only minor ones\n", count(len(ns), "note", "notes"), major, len(flagged)-major)
			if len(flagged) > 0 {
				infof("next: arabic-vocab review\n")
			} else {
				infof("next: arabic-vocab audio\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&python, "python", "", "Python interpreter with camel-tools and catt-tashkeel (default .venv/bin/python, then python3)")
	return cmd
}
