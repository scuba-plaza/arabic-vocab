package deck

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

type SurveyClip struct {
	ID       string
	Position int
	Field    string
	Text     string
	File     string
	Result   sound.Result
	Err      error
}

func (c SurveyClip) Cut() time.Duration {
	if c.Err != nil || !c.Result.Trimmed() {
		return 0
	}
	return c.Result.Head() + c.Result.Tail()
}

type Survey struct {
	Clips   []SurveyClip
	Missing int
}

func (s *Survey) Trimmed() []SurveyClip {
	var out []SurveyClip
	for _, c := range s.Clips {
		if c.Err == nil && c.Result.Trimmed() {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b SurveyClip) int { return int(b.Cut() - a.Cut()) })
	return out
}

func (s *Survey) Silent() []SurveyClip {
	return s.pick(func(c SurveyClip) bool { return c.Err == nil && c.Result.Silent })
}

func (s *Survey) Unreadable() []SurveyClip {
	return s.pick(func(c SurveyClip) bool { return c.Err != nil })
}

func (s *Survey) Untouched() int {
	return len(s.Clips) - len(s.Trimmed()) - len(s.Silent()) - len(s.Unreadable())
}

func (s *Survey) Cut() time.Duration {
	var total time.Duration
	for _, c := range s.Clips {
		total += c.Cut()
	}
	return total
}

func (s *Survey) pick(keep func(SurveyClip) bool) []SurveyClip {
	var out []SurveyClip
	for _, c := range s.Clips {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func SurveyAudio(ctx context.Context, ns []notes.Note, voice Voice, mediaDir string, inspect Inspector, concurrency int, progress func(done, total int)) (*Survey, error) {
	if concurrency <= 0 {
		concurrency = 4
	}
	survey := &Survey{}
	var todo []SurveyClip
	seen := map[string]bool{}
	for _, n := range ns {
		if !n.Authored() {
			continue
		}
		for _, at := range AudioTexts(n) {
			file := AudioFile(voice.Key(), at.Text)
			if seen[file] {
				continue
			}
			seen[file] = true
			if _, err := os.Stat(MediaPath(mediaDir, file)); err != nil {
				survey.Missing++
				continue
			}
			todo = append(todo, SurveyClip{ID: n.ID, Position: n.Position, Field: at.Field, Text: at.Text, File: file})
		}
	}
	var mu sync.Mutex
	done := 0
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for i := range todo {
		g.Go(func() error {
			res, err := inspect(gctx, MediaPath(mediaDir, todo[i].File))
			if err != nil && gctx.Err() != nil {
				return gctx.Err()
			}
			mu.Lock()
			defer mu.Unlock()
			todo[i].Result, todo[i].Err = res, err
			done++
			if progress != nil {
				progress(done, len(todo))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	slices.SortFunc(todo, func(a, b SurveyClip) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(a.Field, b.Field)
	})
	survey.Clips = todo
	return survey, nil
}
