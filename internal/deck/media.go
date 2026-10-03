package deck

import (
	"context"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type Voice struct {
	Name string
	Rate float64
}

func (v Voice) Key() string {
	return fmt.Sprintf("%s@%.2f", v.Name, v.Rate)
}

type ManifestEntry struct {
	Text  string  `json:"text"`
	Voice string  `json:"voice"`
	Rate  float64 `json:"rate"`
	File  string  `json:"file"`
}

type Speaker func(ctx context.Context, text, path string) error

type AudioOptions struct {
	Voice       Voice
	MediaDir    string
	Concurrency int
	Progress    func(done, total int)
}

type AudioResult struct {
	Manifest    []ManifestEntry
	Synthesized int
	Reused      int
}

func AudioIndex(manifest []ManifestEntry) map[string]string {
	out := map[string]string{}
	for _, m := range manifest {
		out[m.Text] = m.File
	}
	return out
}

func GenerateAudio(ctx context.Context, ns []notes.Note, previous []ManifestEntry, speak Speaker, opts AudioOptions) (*AudioResult, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if err := os.MkdirAll(opts.MediaDir, 0o755); err != nil {
		return nil, err
	}
	res := &AudioResult{Manifest: append([]ManifestEntry(nil), previous...)}
	known := map[string]bool{}
	for _, m := range previous {
		known[m.File] = true
	}

	type job struct {
		text string
		file string
	}
	var jobs []job
	queued := map[string]bool{}
	for _, n := range ns {
		if !n.Authored() {
			continue
		}
		for _, at := range AudioTexts(n) {
			file := AudioFile(opts.Voice.Key(), at.Text)
			if queued[file] {
				continue
			}
			queued[file] = true
			if _, err := os.Stat(MediaPath(opts.MediaDir, file)); err == nil {
				res.Reused++
				if !known[file] {
					res.Manifest = append(res.Manifest, ManifestEntry{Text: at.Text, Voice: opts.Voice.Name, Rate: opts.Voice.Rate, File: file})
					known[file] = true
				}
				continue
			}
			jobs = append(jobs, job{text: at.Text, file: file})
		}
	}

	var mu sync.Mutex
	done := 0
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	for _, j := range jobs {
		g.Go(func() error {
			if err := speak(gctx, j.text, MediaPath(opts.MediaDir, j.file)); err != nil {
				return fmt.Errorf("synthesizing %q: %w", j.text, err)
			}
			mu.Lock()
			defer mu.Unlock()
			res.Manifest = append(res.Manifest, ManifestEntry{Text: j.text, Voice: opts.Voice.Name, Rate: opts.Voice.Rate, File: j.file})
			res.Synthesized++
			done++
			if opts.Progress != nil {
				opts.Progress(done, len(jobs))
			}
			return nil
		})
	}
	err := g.Wait()
	return res, err
}
