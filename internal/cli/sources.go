package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func newFetchCommand(paths *deck.Paths) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Download the Wiktionary dump and frequency lists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &http.Client{Timeout: 30 * time.Minute}
			for _, s := range deck.Sources(*paths) {
				infof("%s ... ", deck.Describe(s))
				downloaded, err := deck.Fetch(cmd.Context(), client, s, force)
				if err != nil {
					infof("failed\n")
					return err
				}
				if downloaded {
					infof("saved %s\n", s.Path)
				} else {
					infof("already present\n")
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "download again even if the file exists")
	return cmd
}

func openOptional(path string) (*os.File, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return f, err
}

func loadLexicon(path string) ([]*lexicon.Lemma, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w; run 'arabic-vocab fetch' first", err)
	}
	defer f.Close()
	entries, err := lexicon.Read(f)
	if err != nil {
		return nil, err
	}
	return lexicon.Group(entries), nil
}

func newRankCommand(paths *deck.Paths) *cobra.Command {
	var subsLimit, msaLimit, limit int
	cmd := &cobra.Command{
		Use:   "rank",
		Short: "Order dictionary words by frequency in two corpora",
		Long: "Map every frequent word form in the subtitle and MSA frequency lists to a\n" +
			"Wiktionary lemma, once with CAMeL's disambiguator and once with the\n" +
			"inflection tables in the dump, then blend the two corpora with a geometric\n" +
			"mean. Writes ranked.tsv and lexicon.jsonl into --deck-dir.\n\n" +
			"CAMeL's analyses come from scripts/camel_lemmas.py; without them the\n" +
			"ranking falls back to the inflection tables alone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			infof("reading Wiktionary dump\n")
			lemmas, err := loadLexicon(paths.Kaikki())
			if err != nil {
				return err
			}
			ix := rank.NewIndex(lemmas)
			infof("%d lemma groups\n", len(lemmas))

			var corpora []*rank.Corpus
			for _, src := range []struct {
				name  string
				path  string
				limit int
			}{{"subtitles", paths.Subtitles(), subsLimit}, {"msa", paths.MSA(), msaLimit}} {
				f, err := os.Open(src.path)
				if err != nil {
					return fmt.Errorf("%w; run 'arabic-vocab fetch' first", err)
				}
				c, err := rank.ReadCounts(src.name, f, src.limit)
				f.Close()
				if err != nil {
					return err
				}
				corpora = append(corpora, c)
			}

			var camel map[string]rank.CamelAnalysis
			if f, err := openOptional(paths.CamelLemmas()); err != nil {
				return err
			} else if f != nil {
				camel, err = rank.ReadCamel(f)
				f.Close()
				if err != nil {
					return err
				}
			} else {
				infof("warning: %s is missing, so homographs are resolved by the inflection tables alone\n", paths.CamelLemmas())
			}

			var kelly map[string]rank.KellyWord
			if f, err := openOptional(paths.Kelly()); err != nil {
				return err
			} else if f != nil {
				kelly, err = rank.ReadKelly(f)
				f.Close()
				if err != nil {
					return err
				}
			}

			var essentials []rank.Essential
			if f, err := openOptional(paths.Essentials()); err != nil {
				return err
			} else if f != nil {
				essentials, err = rank.ReadEssentials(f)
				f.Close()
				if err != nil {
					return err
				}
			}
			var overrides []rank.Override
			if f, err := openOptional(paths.Overrides()); err != nil {
				return err
			} else if f != nil {
				overrides, err = rank.ReadOverrides(f)
				f.Close()
				if err != nil {
					return err
				}
			}

			result, err := rank.Rank(ix, corpora, camel, kelly, essentials, overrides, rank.Options{Limit: limit})
			if err != nil {
				return err
			}
			rows := result.Rows
			if err := os.MkdirAll(paths.Deck, 0o755); err != nil {
				return err
			}
			out, err := os.Create(paths.Ranked())
			if err != nil {
				return err
			}
			var names []string
			for _, c := range corpora {
				names = append(names, c.Name)
			}
			if err := rank.WriteTSV(out, rows, names); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if err := notes.WriteJSONL(paths.Lexicon(), rank.Records(rows, 12)); err != nil {
				return err
			}

			if err := os.MkdirAll(paths.Cache, 0o755); err != nil {
				return err
			}
			report, err := os.Create(paths.RankReport())
			if err != nil {
				return err
			}
			if err := rank.WriteDecisions(report, ix, result.Decisions, 3000, map[string]bool{"contested": true, "proper-noun": true}); err != nil {
				report.Close()
				return err
			}
			if err := report.Close(); err != nil {
				return err
			}

			contested := 0
			for _, r := range rows[:min(1000, len(rows))] {
				if r.Contested > 0.5 {
					contested++
				}
			}
			infof("wrote %s and %s (%d lemmas; %d of the top 1000 mostly contested)\n",
				paths.Ranked(), paths.Lexicon(), len(rows), contested)
			infof("disputed word forms, most frequent first: %s\n", paths.RankReport())
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&subsLimit, "subtitles-limit", 50000, "word types to read from the subtitle list")
	f.IntVar(&msaLimit, "msa-limit", 100000, "word types to read from the MSA list")
	f.IntVar(&limit, "limit", 5000, "lemmas to keep")
	return cmd
}
