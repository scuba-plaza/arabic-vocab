package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func defaultPython() string {
	venv := filepath.Join(".venv", "bin", "python")
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	return "python3"
}

func newCheckCommand(paths *deck.Paths) *cobra.Command {
	var python, script string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Cross-check every diacritic against CAMeL and CATT",
		Long: "Check each note's headword, forms and example sentence word by word:\n" +
			"  - every letter must carry a vowel, sukun or shadda (case endings included\n" +
			"    in sentences);\n" +
			"  - CAMeL's morphological analyzer must accept the exact vowelling;\n" +
			"  - in example sentences, CATT's independent vowelling of the bare sentence\n" +
			"    must agree, with CAMeL's contextual reading reported as a tie-breaker.\n\n" +
			"Results go to qa.jsonl; 'arabic-vocab build' tags every note with a disagreement\n" +
			"as check::diacritics and prints the details on the back of the card.\n\n" +
			"Needs a Python environment with scripts/requirements.txt installed.",
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
			results, err := deck.RunChecker(cmd.Context(), python, script, items, os.Stderr)
			if err != nil {
				return err
			}
			checks := deck.Evaluate(ns, items, results)
			if err := notes.WriteJSONL(paths.QA(), checks); err != nil {
				return err
			}
			kinds := map[string]int{}
			major, minor := 0, 0
			for _, c := range checks {
				sev := ""
				for _, is := range c.Issues {
					kinds[string(is.Severity)+" "+is.Kind]++
					if is.Severity == notes.Major {
						sev = "major"
					} else if sev == "" {
						sev = "minor"
					}
				}
				switch sev {
				case "major":
					major++
				case "minor":
					minor++
				}
			}
			var names []string
			for k := range kinds {
				names = append(names, k)
			}
			sort.Strings(names)
			infof("wrote %s: %d of %d notes conflict (check::diacritics), %d more have a minor disagreement (check::diacritics-minor)\n",
				paths.QA(), major, len(checks), minor)
			for _, k := range names {
				infof("  %-20s %d\n", k, kinds[k])
			}
			out := cmd.OutOrStdout()
			for _, c := range checks {
				for _, is := range c.Issues {
					fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, is.Severity, is.Field, is.Kind, is.Word, is.Detail)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&python, "python", "", "Python interpreter with camel-tools and catt-tashkeel (default .venv/bin/python, then python3)")
	cmd.Flags().StringVar(&script, "script", paths.CheckScript(), "checker script")
	return cmd
}
