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

var g globals

type usageError struct{ error }

func usagef(format string, args ...any) error {
	return usageError{fmt.Errorf(format, args...)}
}

func newRootCommand() *cobra.Command {
	paths := &deck.Paths{}
	root := &cobra.Command{
		Use:   "arabic-vocab",
		Short: "Build a frequency-ordered Modern Standard Arabic Anki deck",
		Long: "Build an Anki deck of the most common Modern Standard Arabic words.\n\n" +
			"The pipeline runs in stages, each writing a file you can inspect:\n" +
			"  fetch      download Wiktionary and the frequency lists\n" +
			"  rank       order dictionary words by corpus frequency\n" +
			"  prepare    add note skeletons for a rank range\n" +
			"  show       print a curation worksheet for a rank range\n" +
			"  curate     fill glosses and example sentences with the Claude API\n" +
			"  check      cross-check every diacritic against CAMeL and CATT\n" +
			"  voicetest  compare voices on words that differ only in their vowels\n" +
			"  audio      synthesize word, form and example audio\n" +
			"  build      write the .apkg\n\n" +
			"Committed sources live in --deck-dir; downloads and audio live in --cache.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&paths.Deck, "deck-dir", filepath.Join("decks", "msa-core"), "directory with the committed deck sources")
	pf.StringVar(&paths.Cache, "cache", "deck-data", "directory for downloads, audio and other local files")
	pf.StringVar(&g.credentials, "credentials", "", "Google service account JSON key for audio (default: $GOOGLE_APPLICATION_CREDENTIALS, then .env/*.json)")
	pf.StringVar(&g.project, "project", "", "Google Cloud project ID (default: the credential's project)")
	pf.StringVar(&g.region, "region", config.DefaultRegion, "Speech-to-Text region for verifying audio")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "suppress progress output")

	root.AddCommand(
		newFetchCommand(paths),
		newRankCommand(paths),
		newPrepareCommand(paths),
		newShowCommand(paths),
		newCurateCommand(paths),
		newCheckCommand(paths),
		newVoiceTestCommand(paths),
		newAudioCommand(paths),
		newBuildCommand(paths),
	)
	return root
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

func infof(format string, args ...any) {
	if g.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
}
