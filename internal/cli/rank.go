package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/lexicon"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

const (
	subtitlesLimit = 50000
	msaLimit       = 100000
)

func download(ctx context.Context, paths *deck.Paths, refresh bool) error {
	for _, s := range deck.Sources(*paths) {
		if err := downloadSource(ctx, s, refresh); err != nil {
			return err
		}
	}
	return nil
}

func downloadSource(ctx context.Context, s deck.Source, refresh bool) error {
	if st, err := os.Stat(s.Path); err == nil && st.Size() > 0 && !refresh {
		return nil
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	infof("downloading %s ... ", deck.Describe(s))
	if _, err := deck.Fetch(ctx, client, s, true); err != nil {
		infof("failed\n")
		return err
	}
	infof("done\n")
	return nil
}

func analyse(ctx context.Context, paths *deck.Paths, python string, refresh bool) error {
	out := paths.CamelLemmas()
	if _, err := os.Stat(out); err == nil && !refresh {
		return nil
	}
	if python == "" {
		python = defaultPython()
	}
	infof("analysing the frequent word forms with CAMeL Tools; this takes a few minutes\n")
	c := exec.CommandContext(ctx, python, paths.CamelLemmasScript(), out+".partial",
		fmt.Sprintf("%s:%d", paths.Subtitles(), subtitlesLimit), fmt.Sprintf("%s:%d", paths.MSA(), msaLimit))
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	if err := c.Run(); err != nil {
		os.Remove(out + ".partial")
		return fmt.Errorf("running %s %s: %w; 'make venv' sets up CAMeL Tools", python, paths.CamelLemmasScript(), err)
	}
	return os.Rename(out+".partial", out)
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
		return nil, err
	}
	defer f.Close()
	entries, err := lexicon.Read(f)
	if err != nil {
		return nil, err
	}
	return lexicon.Group(entries), nil
}

func newRankCommand(paths *deck.Paths) *cobra.Command {
	var (
		limit   int
		refresh bool
		python  string
	)
	cmd := &cobra.Command{
		Use:   "rank",
		Short: "Rank Wiktionary's words by how often they are used",
		Long: "Order Wiktionary's Arabic words by how often they occur in subtitles and in\n" +
			"written MSA, and write ranked.tsv and lexicon.jsonl into --deck-dir. The\n" +
			"deck ships with a ranking, so this is only needed after editing\n" +
			"essentials.tsv or overrides.tsv, or to rank more than --limit words.\n\n" +
			"The first run downloads the Wiktionary dump and the frequency lists into\n" +
			"--cache and has CAMeL Tools analyse every frequent word form, which takes a\n" +
			"few minutes and needs the Python environment from 'make venv'. Both are kept\n" +
			"for later runs; --refresh downloads and analyses everything again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := download(cmd.Context(), paths, refresh); err != nil {
				return err
			}
			if err := analyse(cmd.Context(), paths, python, refresh); err != nil {
				return err
			}
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
			}{{"subtitles", paths.Subtitles(), subtitlesLimit}, {"msa", paths.MSA(), msaLimit}} {
				f, err := os.Open(src.path)
				if err != nil {
					return err
				}
				c, err := rank.ReadCounts(src.name, f, src.limit)
				f.Close()
				if err != nil {
					return err
				}
				corpora = append(corpora, c)
			}

			f, err := os.Open(paths.CamelLemmas())
			if err != nil {
				return err
			}
			camel, err := rank.ReadCamel(f)
			f.Close()
			if err != nil {
				return err
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
	f.IntVar(&limit, "limit", 5000, "number of words to rank")
	f.BoolVar(&refresh, "refresh", false, "download the sources and analyse them again")
	f.StringVar(&python, "python", "", "Python interpreter with camel-tools (default .venv/bin/python, then python3)")
	return cmd
}
