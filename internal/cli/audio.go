package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-tts/config"
	"github.com/scuba-plaza/arabic-tts/gcp"
	"github.com/scuba-plaza/arabic-tts/tts"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func newAudioCommand(paths *deck.Paths) *cobra.Command {
	var (
		voice       string
		rate        float64
		concurrency int
	)
	cmd := &cobra.Command{
		Use:   "audio",
		Short: "Synthesize the audio",
		Long: "Synthesize three clips per note (headword, forms, example sentence) from\n" +
			"the fully vowelled text, as MP3 in --cache/media. Clips are named after a\n" +
			"hash of voice, rate and text, so running audio again only synthesizes what\n" +
			"changed. Example sentences are spoken with a pausal ending, as a reader\n" +
			"stops: the last word of each sentence drops its case vowel.\n\n" +
			"The voice and speaking rate come from deck.json in --deck-dir. --voice and\n" +
			"--rate change them there, so later runs keep using them; compare voices\n" +
			"with 'arabic-vocab voices'.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ns, err := loadNotes(paths.Notes())
			if err != nil {
				return err
			}
			saved, err := deck.LoadSettings(paths.Settings())
			if err != nil {
				return err
			}
			settings := saved
			if cmd.Flags().Changed("voice") {
				settings.Voice = voice
			}
			if cmd.Flags().Changed("rate") {
				settings.Rate = rate
			}
			remember := func(res *deck.AudioResult, runErr error) error {
				if settings == saved || res == nil || runErr != nil && res.Synthesized == 0 {
					return nil
				}
				infof("from now on the deck uses voice %s at rate %.2f (saved in %s)\n", settings.Voice, settings.Rate, paths.Settings())
				return deck.SaveSettings(paths.Settings(), settings)
			}
			creds, err := resolve()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			speak, ttsClient, err := newSpeaker(ctx, creds, settings.AudioVoice())
			if err != nil {
				return err
			}
			defer ttsClient.Close()
			manifest, err := notes.ReadJSONL[deck.ManifestEntry](paths.Manifest())
			if err != nil {
				return err
			}
			res, runErr := deck.GenerateAudio(ctx, ns, manifest, speak, deck.AudioOptions{
				Voice:       settings.AudioVoice(),
				MediaDir:    paths.Media(),
				Concurrency: concurrency,
				Progress: func(done, total int) {
					infof("\rsynthesized %d/%d", done, total)
				},
			})
			if res != nil {
				if err := notes.WriteJSONL(paths.Manifest(), res.Manifest); err != nil {
					return err
				}
			}
			infof("\n")
			if err := remember(res, runErr); err != nil {
				return err
			}
			if runErr != nil {
				if res != nil {
					infof("%d clips were saved before the error; run the same command again to continue\n", res.Synthesized)
				}
				return runErr
			}
			infof("%d clips synthesized, %d already present\n", res.Synthesized, res.Reused)
			infof("next: arabic-vocab build\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&voice, "voice", "", "use this voice from now on and save it in deck.json")
	f.Float64Var(&rate, "rate", 0, "use this speaking rate (0.25 to 2.0) from now on and save it in deck.json")
	f.IntVar(&concurrency, "concurrency", 4, "parallel synthesis requests")
	googleFlags(cmd)
	return cmd
}

func pickVoices(all []tts.VoiceInfo) []string {
	limits := map[string]int{"Chirp3-HD": 2, "Neural2": 1, "Wavenet": 1, "Standard": 1}
	var out []string
	for _, v := range all {
		if limits[v.Tier] > 0 {
			limits[v.Tier]--
			out = append(out, v.Name)
		}
	}
	return out
}

func newVoicesCommand(paths *deck.Paths) *cobra.Command {
	var (
		voices string
		rate   float64
	)
	cmd := &cobra.Command{
		Use:   "voices",
		Short: "Compare voices on words that differ only in their vowels",
		Long: "Synthesize minimal pairs such as عَلِمَ / عَلَّمَ / عُلِمَ and a fully vowelled\n" +
			"sentence with several voices, and write an HTML page with an audio player\n" +
			"for each. Open it, listen, and give the voice that follows the marks to\n" +
			"'arabic-vocab audio --voice'.\n\n" +
			"Without --voices, two Chirp 3 HD voices and one voice of each older tier\n" +
			"are picked from Google's ar-XA voices.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			creds, err := resolve()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			client, err := gcp.NewTextToSpeechClient(ctx, creds)
			if err != nil {
				return err
			}
			defer client.Close()
			var names []string
			if voices != "" {
				names = strings.Split(voices, ",")
			} else {
				all, err := tts.ListVoices(ctx, client, config.DefaultLanguage, "")
				if err != nil {
					return err
				}
				names = pickVoices(all)
			}
			var list []deck.Voice
			for _, n := range names {
				list = append(list, deck.Voice{Name: strings.TrimSpace(n), Rate: rate})
			}
			page, err := deck.CompareVoices(ctx, list, paths.Voices(), func(ctx context.Context, v deck.Voice, text, path string) error {
				_, err := tts.Synthesize(ctx, client, text, tts.Options{Voice: v.Name, Language: config.DefaultLanguage, SpeakingRate: v.Rate, Output: path, Concurrency: 1})
				return err
			}, func(done, total int) { infof("\rsynthesized %d/%d", done, total) })
			infof("\n")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "open %s\n", page)
			return nil
		},
	}
	cmd.Flags().StringVar(&voices, "voices", "", "comma-separated voice names (default: a few per tier)")
	cmd.Flags().Float64Var(&rate, "rate", deck.DefaultRate, "speaking rate")
	googleFlags(cmd)
	return cmd
}
