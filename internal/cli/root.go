package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-tts/config"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
)

const (
	ExitOK    = 0
	ExitUsage = 1
	ExitError = 2
)

type globals struct {
	credentials string
	project     string
	region      string
	quiet       bool
}

var g = globals{region: config.DefaultRegion}

type usageError struct{ error }

func usagef(format string, args ...any) error {
	return usageError{fmt.Errorf(format, args...)}
}

func newRootCommand() *cobra.Command {
	cobra.EnableCommandSorting = false
	paths := &deck.Paths{}
	root := &cobra.Command{
		Use:   "arabic-vocab",
		Short: "Build a frequency-ordered Modern Standard Arabic Anki deck",
		Long: "Build an Anki deck of the most common Modern Standard Arabic words, with full\n" +
			"vowel marks, audio and an example sentence for every word.\n\n" +
			"New words go through add, check, review, audio and build, in that order;\n" +
			"'arabic-vocab status' tells you where the deck stands and what to run next.\n" +
			"The deck's sources live in --deck-dir, downloads and audio in --cache.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddGroup(
		&cobra.Group{ID: "deck", Title: "Making the deck:"},
		&cobra.Group{ID: "setup", Title: "Setup and maintenance:"},
	)
	pf := root.PersistentFlags()
	pf.StringVar(&paths.Deck, "deck-dir", filepath.Join("decks", "msa-core"), "directory with the committed deck sources")
	pf.StringVar(&paths.Cache, "cache", "deck-data", "directory for downloads, audio and other local files")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "suppress progress output")

	for _, c := range []*cobra.Command{
		newStatusCommand(paths),
		newAddCommand(paths),
		newCheckCommand(paths),
		newReviewCommand(paths),
		newAudioCommand(paths),
		newBuildCommand(paths),
	} {
		c.GroupID = "deck"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{newVoicesCommand(paths), newRankCommand(paths)} {
		c.GroupID = "setup"
		root.AddCommand(c)
	}
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	return root
}

func googleFlags(cmd *cobra.Command, region bool) {
	f := cmd.Flags()
	f.StringVar(&g.credentials, "credentials", "", "Google service account JSON key (default: $GOOGLE_APPLICATION_CREDENTIALS, then .env/*.json)")
	f.StringVar(&g.project, "project", "", "Google Cloud project ID (default: the key's project)")
	if region {
		f.StringVar(&g.region, "region", config.DefaultRegion, "Speech-to-Text region for checking the audio")
	}
}

func Execute(ctx context.Context) int {
	if err := newRootCommand().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		var usageErr usageError
		if errors.As(err, &usageErr) || errors.Is(err, config.ErrNoCredentials) {
			return ExitUsage
		}
		return ExitError
	}
	return ExitOK
}

func resolve() (config.Credentials, error) {
	creds, err := config.ResolveCredentials(g.credentials)
	if err != nil {
		return config.Credentials{}, err
	}
	if g.project != "" {
		creds.ProjectID = g.project
	}
	return creds, nil
}

func count(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func infof(format string, args ...any) {
	if g.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
}
