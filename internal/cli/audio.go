package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-tts/config"
	"github.com/scuba-plaza/arabic-tts/gcp"
	"github.com/scuba-plaza/arabic-tts/tts"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

func newAudioCommand(paths *deck.Paths) *cobra.Command {
	var (
		voice       string
		rate        float64
		concurrency int
		dryRun      bool
	)
	cmd := &cobra.Command{
		Use:   "audio",
		Short: "Synthesize the audio",
		Long: "Synthesize three clips per note (headword, forms, example sentence) from\n" +
			"the fully vowelled text, as MP3 in --cache/media. Clips are named after a\n" +
			"hash of voice, rate and text, so running audio again only synthesizes what\n" +
			"changed. Example sentences are spoken with a pausal ending, as a reader\n" +
			"stops: the last word of each sentence drops its case vowel.\n\n" +
			"Every clip is listened to for sound. Silence at the start or end is cut off,\n" +
			"leaving a short pad. A clip that comes back with no sound at all is asked\n" +
			"for again, up to three more times; if it stays silent it is not kept, the\n" +
			"note is flagged for 'arabic-vocab review' and the next run tries it again.\n" +
			"Clips made before this check are listened to once, on the first run, and cut\n" +
			"in place without a backup: 'audio --dry-run' first listens to every clip you\n" +
			"have and reports what would be cut or made again, without changing anything\n" +
			"or needing Google.\n\n" +
			"The voice and speaking rate come from deck.json in --deck-dir. --voice and\n" +
			"--rate change them there, so later runs keep using them; compare voices\n" +
			"with 'arabic-vocab voices'.\n\n" +
			"Only one audio run, or one review at a time, may change the audio files; the\n" +
			"others wait a few seconds and then say so.\n\n" +
			"Needs ffmpeg.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sound.Available(); err != nil {
				if dryRun {
					return usagef("%v; ffmpeg is needed to listen to the clips", err)
				}
				return usagef("%v; ffmpeg is needed to cut the silence off the clips", err)
			}
			ns, err := loadNotes(paths.Notes())
			if err != nil {
				return err
			}
			saved, err := deck.LoadSettings(paths.Settings())
			if err != nil {
				return err
			}
			if dryRun {
				return surveyClips(cmd, paths, ns, saved.AudioVoice(), concurrency)
			}
			settings := saved
			if cmd.Flags().Changed("voice") {
				settings.Voice = voice
			}
			if cmd.Flags().Changed("rate") {
				settings.Rate = rate
			}
			remember := func(res *deck.AudioResult, runErr error) error {
				if settings == saved || res == nil || (runErr != nil || len(res.Failed) > 0) && res.Synthesized == 0 {
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
			unlock, err := deck.LockAudio(ctx, paths.AudioLock(), lockWait)
			if err != nil {
				return err
			}
			defer unlock()
			speak, ttsClient, err := newSpeaker(ctx, creds, settings.AudioVoice())
			if err != nil {
				return err
			}
			defer ttsClient.Close()
			manifest, err := notes.ReadJSONL[deck.ManifestEntry](paths.Manifest())
			if err != nil {
				return err
			}
			checks, err := notes.ReadJSONL[notes.Check](paths.AudioQA())
			if err != nil {
				return err
			}
			res, runErr := deck.GenerateAudio(ctx, ns, manifest, checks, speak, sound.Trim, deck.AudioOptions{
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
				if err := notes.WriteJSONL(paths.AudioQA(), res.Checks); err != nil {
					return err
				}
			}
			infof("\n")
			if err := remember(res, runErr); err != nil {
				return err
			}
			if res != nil {
				for _, f := range res.Failed {
					fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\t%v\n", f.Position, f.ID, deck.ClipLabel(f.Field), f.Err)
				}
			}
			if runErr != nil {
				if res != nil {
					infof("%d clips were saved before the error; run the same command again to continue\n", res.Synthesized)
				}
				return runErr
			}
			summary := fmt.Sprintf("%d clips synthesized, %d already present", res.Synthesized, res.Reused)
			if res.Trimmed > 0 {
				summary += "; silence cut off " + count(res.Trimmed, "clip", "clips")
			}
			infof("%s\n", summary)
			if silent, broken := res.Silent(), res.Unreadable(); silent+broken > 0 {
				var parts []string
				if silent > 0 {
					parts = append(parts, fmt.Sprintf("%s had no sound in any of %d attempts and %s flagged for 'arabic-vocab review'", count(silent, "clip", "clips"), deck.DefaultClipAttempts, plural(silent, "is", "are")))
				}
				if broken > 0 {
					parts = append(parts, fmt.Sprintf("%s could not be read after it was made", count(broken, "clip", "clips")))
				}
				return fmt.Errorf("%s; the next 'arabic-vocab audio' tries again", strings.Join(parts, " and "))
			}
			infof("next: arabic-vocab build\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&voice, "voice", "", "use this voice from now on and save it in deck.json")
	f.Float64Var(&rate, "rate", 0, "use this speaking rate (0.25 to 2.0) from now on and save it in deck.json")
	f.IntVar(&concurrency, "concurrency", 4, "parallel synthesis requests")
	f.BoolVar(&dryRun, "dry-run", false, "listen to the clips already made and report what would be cut or made again, changing nothing")
	googleFlags(cmd)
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func seconds(d time.Duration) string {
	return fmt.Sprintf("%.1f s", d.Seconds())
}

const longestCuts = 10

func surveyClips(cmd *cobra.Command, paths *deck.Paths, ns []notes.Note, voice deck.Voice, concurrency int) error {
	survey, err := deck.SurveyAudio(cmd.Context(), ns, voice, paths.Media(), sound.Inspect, concurrency, func(done, total int) {
		infof("\rlistened to %d/%d", done, total)
	})
	infof("\n")
	if err != nil {
		return err
	}
	printSurvey(cmd.OutOrStdout(), survey)
	return nil
}

func printSurvey(w io.Writer, survey *deck.Survey) {
	trimmed, silent, broken := survey.Trimmed(), survey.Silent(), survey.Unreadable()
	fmt.Fprintf(w, "listened to %s", count(len(survey.Clips), "clip", "clips"))
	if survey.Missing > 0 {
		fmt.Fprintf(w, "; %s not made yet", count(survey.Missing, "more clip is", "more clips are"))
	}
	fmt.Fprintf(w, "\n  %d are fine as they are\n", survey.Untouched())
	if len(trimmed) > 0 {
		fmt.Fprintf(w, "  %d would be cut, %s of silence in all; the longest cuts:\n", len(trimmed), seconds(survey.Cut()))
		for _, c := range trimmed[:min(len(trimmed), longestCuts)] {
			fmt.Fprintf(w, "    %s  %d\t%s\t%s\t%s\n", seconds(c.Cut()), c.Position, c.ID, deck.ClipLabel(c.Field), c.Text)
		}
	}
	if len(silent) > 0 {
		fmt.Fprintf(w, "  %s no sound and would be made again:\n", count(len(silent), "clip has", "clips have"))
		for _, c := range silent {
			fmt.Fprintf(w, "    %d\t%s\t%s\n", c.Position, c.ID, deck.ClipLabel(c.Field))
		}
	}
	if len(broken) > 0 {
		fmt.Fprintf(w, "  %s be read and would be made again:\n", count(len(broken), "clip cannot", "clips cannot"))
		for _, c := range broken {
			fmt.Fprintf(w, "    %d\t%s\t%s\t%v\n", c.Position, c.ID, deck.ClipLabel(c.Field), c.Err)
		}
	}
	fmt.Fprintln(w, "nothing was changed; 'arabic-vocab audio' makes these changes")
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
