package cli

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func newBuildCommand(paths *deck.Paths) *cobra.Command {
	var (
		out             string
		productionLimit int
		productionDelay int
	)
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Write the Anki package",
		Long: "Write an .apkg with one note per authored entry in notes.jsonl.\n\n" +
			"Every note gets a recognition card (Arabic to English). Notes at or above\n" +
			"--production-limit also get a production card (English to Arabic), which\n" +
			"is queued --production-delay positions after its recognition card.\n\n" +
			"Notes keep stable IDs, so importing a rebuilt package into Anki updates the\n" +
			"existing notes in place and keeps their review history. Audio from\n" +
			"'arabic-vocab audio' is included when present.",
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
			audio, err := notes.ReadJSONL[notes.AudioCheck](paths.AudioQA())
			if err != nil {
				return err
			}
			manifest, err := notes.ReadJSONL[deck.ManifestEntry](paths.Manifest())
			if err != nil {
				return err
			}
			pkg, summary, err := deck.BuildPackage(ns, checks, audio, deck.BuildOptions{
				ProductionLimit: productionLimit,
				ProductionDelay: productionDelay,
				Audio:           deck.AudioIndex(manifest),
				MediaDir:        paths.Media(),
				FontPath:        paths.Asset("ScheherazadeNew-Regular.ttf"),
			})
			if err != nil {
				return err
			}
			if out == "" {
				out = paths.Package(deck.DeckFileName)
			}
			if err := pkg.Write(out); err != nil {
				return err
			}
			infof("wrote %s: %d notes, %d cards (%d production), %d audio files", out, summary.Notes, summary.Cards, summary.Production, summary.AudioFiles)
			if summary.MissingAudio > 0 {
				infof(", %d clips not synthesized yet (run 'arabic-vocab audio')", summary.MissingAudio)
			}
			infof("\n")
			var tags []string
			for t := range summary.Tagged {
				tags = append(tags, t)
			}
			sort.Strings(tags)
			for _, t := range tags {
				infof("  %-26s %d notes\n", t, summary.Tagged[t])
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&out, "out", "o", "", "output file (default out/arabic-msa-core.apkg)")
	f.IntVar(&productionLimit, "production-limit", 1000, "give notes up to this position an English-to-Arabic card")
	f.IntVar(&productionDelay, "production-delay", 20, "queue each production card this many positions after its recognition card")
	return cmd
}
