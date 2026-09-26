package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-tts/audio"
	"github.com/scuba-plaza/arabic-tts/config"
	"github.com/scuba-plaza/arabic-tts/gcp"
	"github.com/scuba-plaza/arabic-tts/stt"
	"github.com/scuba-plaza/arabic-tts/tts"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

func newAudioCommand(paths *deck.Paths) *cobra.Command {
	var (
		voice       string
		rate        float64
		concurrency int
		verify      bool
	)
	cmd := &cobra.Command{
		Use:   "audio",
		Short: "Synthesize word, form and example audio for every note",
		Long: "Synthesize three clips per note (headword, forms, example sentence) from\n" +
			"the fully vowelled text, as MP3 in --cache/media. Clips are named after a\n" +
			"hash of voice, rate and text, so re-running only synthesizes what changed.\n\n" +
			"Example sentences are spoken with a pausal ending, as a reader stops: the\n" +
			"last word of each sentence drops its case vowel (أَمْسِ is read أَمْسْ).\n\n" +
			"With --verify (the default) every example clip is transcribed back with\n" +
			"speech-to-text; a transcript that does not match the sentence tags the note\n" +
			"check::audio. Transcripts carry no vowels, so this catches skipped, garbled\n" +
			"or invented words, not wrong vowels; pick a voice with 'arabic-vocab voicetest'.\n" +
			"Once you have listened to a flagged clip and it sounds right, add its file\n" +
			"name to the note's reviewed_audio list.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ns, err := loadNotes(paths.Notes())
			if err != nil {
				return err
			}
			if verify {
				if err := audio.Available(); err != nil {
					return usagef("%v; ffmpeg is needed to verify audio, or pass --verify=false", err)
				}
			}
			creds, err := resolve()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ttsClient, err := gcp.NewTextToSpeechClient(ctx, creds)
			if err != nil {
				return err
			}
			defer ttsClient.Close()
			opts := tts.Options{Voice: voice, Language: config.DefaultLanguage, SpeakingRate: rate, Concurrency: 1}
			if err := opts.Validate(); err != nil {
				return usageError{err}
			}
			speak := func(ctx context.Context, text, path string) error {
				o := opts
				o.Output = path
				_, err := tts.Synthesize(ctx, ttsClient, text, o)
				return err
			}
			var listen deck.Listener
			if verify {
				speechClient, err := gcp.NewSpeechClient(ctx, creds, g.region)
				if err != nil {
					return err
				}
				defer speechClient.Close()
				listen = func(ctx context.Context, path string) (string, error) {
					tr, err := stt.TranscribeFile(ctx, speechClient, path, stt.Options{
						Project: creds.ProjectID, Region: g.region, Language: config.DefaultLanguage,
						Model: config.DefaultSTTModel, Concurrency: 1,
					})
					if errors.Is(err, stt.ErrNoSpeech) {
						return "", nil
					}
					if err != nil {
						return "", err
					}
					return tr.Text(), nil
				}
			}
			manifest, err := notes.ReadJSONL[deck.ManifestEntry](paths.Manifest())
			if err != nil {
				return err
			}
			previous, err := notes.ReadJSONL[notes.AudioCheck](paths.AudioQA())
			if err != nil {
				return err
			}
			res, runErr := deck.GenerateAudio(ctx, ns, manifest, previous, speak, listen, deck.AudioOptions{
				Voice:       deck.Voice{Name: voice, Rate: rate},
				MediaDir:    paths.Media(),
				Concurrency: concurrency,
				Verify:      verify,
				Progress: func(done, total int) {
					infof("\rsynthesized %d/%d", done, total)
				},
			})
			if res != nil {
				if err := notes.WriteJSONL(paths.Manifest(), res.Manifest); err != nil {
					return err
				}
				if err := notes.WriteJSONL(paths.AudioQA(), res.Checks); err != nil {
					return err
				}
			}
			infof("\n")
			if runErr != nil {
				if res != nil {
					infof("%d clips were saved before the error; run the same command again to continue\n", res.Synthesized)
				}
				return runErr
			}
			infof("%d clips synthesized, %d already present\n", res.Synthesized, res.Reused)
			if verify {
				byID := map[string]notes.Note{}
				for _, n := range ns {
					byID[n.ID] = n
				}
				var flagged []notes.AudioCheck
				for _, c := range res.Checks {
					if !c.Match && !slices.Contains(byID[c.ID].ReviewedAudio, c.File) {
						flagged = append(flagged, c)
					}
				}
				if len(flagged) == 0 {
					infof("every example clip transcribed back to its sentence\n")
				} else {
					infof("%d example clips did not transcribe back to their sentence; listen to them, and add the file of any that sound right to the note's reviewed_audio:\n", len(flagged))
					out := cmd.OutOrStdout()
					for _, c := range flagged {
						fmt.Fprintf(out, "%d\t%s\t%s\theard: %s\n", byID[c.ID].Position, c.ID, c.File, c.Transcript)
					}
				}
			}
			infof("next: arabic-vocab build\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&voice, "voice", config.DefaultVoice, "voice name; compare candidates with 'arabic-vocab voicetest'")
	f.Float64Var(&rate, "rate", 0.9, "speaking rate between 0.25 and 2.0")
	f.IntVar(&concurrency, "concurrency", 4, "parallel synthesis requests")
	f.BoolVar(&verify, "verify", true, "transcribe example clips back and flag mismatches")
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

func newVoiceTestCommand(paths *deck.Paths) *cobra.Command {
	var (
		voices string
		rate   float64
	)
	cmd := &cobra.Command{
		Use:   "voicetest",
		Short: "Compare how voices pronounce words that differ only in their vowels",
		Long: "Synthesize minimal pairs such as عَلِمَ / عَلَّمَ / عُلِمَ and a fully vowelled\n" +
			"sentence with several voices, and write an HTML page with an audio player\n" +
			"for each. Open it, listen, and use the voice that follows the marks.\n\n" +
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
			page, err := deck.VoiceTest(ctx, list, paths.VoiceTest(), func(ctx context.Context, v deck.Voice, text, path string) error {
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
	cmd.Flags().Float64Var(&rate, "rate", 0.9, "speaking rate")
	return cmd
}
