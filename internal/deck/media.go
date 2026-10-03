package deck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	Text      string  `json:"text"`
	Voice     string  `json:"voice"`
	Rate      float64 `json:"rate"`
	File      string  `json:"file"`
	Inspected bool    `json:"inspected,omitempty"`
}

type Speaker func(ctx context.Context, text, path string) error

type AudioOptions struct {
	Voice       Voice
	MediaDir    string
	Concurrency int
	Attempts    int
	Progress    func(done, total int)
}

type ClipFailure struct {
	ID       string
	Position int
	Field    string
	Err      error
}

type AudioResult struct {
	Manifest    []ManifestEntry
	Checks      []notes.Check
	Synthesized int
	Reused      int
	Trimmed     int
	Failed      []ClipFailure
}

func AudioIndex(manifest []ManifestEntry) map[string]string {
	out := map[string]string{}
	for _, m := range manifest {
		out[m.Text] = m.File
	}
	return out
}

type manifestSet struct {
	order   []string
	entries map[string]ManifestEntry
}

func newManifestSet(previous []ManifestEntry) *manifestSet {
	m := &manifestSet{entries: map[string]ManifestEntry{}}
	for _, e := range previous {
		m.put(e)
	}
	return m
}

func (m *manifestSet) put(e ManifestEntry) {
	if _, ok := m.entries[e.File]; !ok {
		m.order = append(m.order, e.File)
	}
	m.entries[e.File] = e
}

func (m *manifestSet) drop(file string) {
	delete(m.entries, file)
}

func (m *manifestSet) list() []ManifestEntry {
	out := make([]ManifestEntry, 0, len(m.entries))
	seen := map[string]bool{}
	for _, file := range m.order {
		if e, ok := m.entries[file]; ok && !seen[file] {
			seen[file] = true
			out = append(out, e)
		}
	}
	return out
}

func clearLeftovers(dir string) {
	for _, pattern := range []string{".make-*", ".trim-*"} {
		stale, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, path := range stale {
			os.Remove(path)
		}
	}
}

type clipOwner struct {
	note  int
	field string
}

type clipTask struct {
	text      string
	file      string
	owners    []clipOwner
	onDisk    bool
	inspected bool
}

func GenerateAudio(ctx context.Context, ns []notes.Note, previous []ManifestEntry, previousChecks []notes.Check, speak Speaker, inspect Inspector, opts AudioOptions) (*AudioResult, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Attempts <= 0 {
		opts.Attempts = DefaultClipAttempts
	}
	if err := os.MkdirAll(opts.MediaDir, 0o755); err != nil {
		return nil, err
	}
	clearLeftovers(opts.MediaDir)

	var authored []notes.Note
	for _, n := range ns {
		if n.Authored() {
			authored = append(authored, n)
		}
	}
	manifest := newManifestSet(previous)
	checks := PruneAudioChecks(previousChecks, authored)
	res := &AudioResult{}
	checked := map[string]bool{}
	for _, m := range previous {
		if m.Inspected {
			checked[m.File] = true
		}
	}

	var tasks []*clipTask
	byFile := map[string]*clipTask{}
	for i, n := range ns {
		if !n.Authored() {
			continue
		}
		for _, at := range AudioTexts(n) {
			file := AudioFile(opts.Voice.Key(), at.Text)
			t := byFile[file]
			if t == nil {
				t = &clipTask{text: at.Text, file: file, inspected: checked[file]}
				if _, err := os.Stat(MediaPath(opts.MediaDir, file)); err == nil {
					t.onDisk = true
				}
				byFile[file] = t
				tasks = append(tasks, t)
			}
			t.owners = append(t.owners, clipOwner{note: i, field: at.Field})
		}
	}

	var jobs []*clipTask
	for _, t := range tasks {
		if t.onDisk && t.inspected {
			res.Reused++
			for _, o := range t.owners {
				checks = WithoutAudioIssue(checks, ns[o.note], o.field)
			}
			continue
		}
		jobs = append(jobs, t)
	}

	var mu sync.Mutex
	done := 0
	finish := func(t *clipTask, silent bool, attempts int) {
		for _, o := range t.owners {
			if silent {
				checks = WithAudioIssue(checks, ns[o.note], SilentIssue(o.field, attempts))
				res.Failed = append(res.Failed, ClipFailure{ID: ns[o.note].ID, Position: ns[o.note].Position, Field: o.field, Err: SilentError(attempts)})
			} else {
				checks = WithoutAudioIssue(checks, ns[o.note], o.field)
			}
		}
		done++
		if opts.Progress != nil {
			opts.Progress(done, len(jobs))
		}
	}
	entry := func(t *clipTask) ManifestEntry {
		return ManifestEntry{Text: t.text, Voice: opts.Voice.Name, Rate: opts.Voice.Rate, File: t.file, Inspected: true}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	for _, t := range jobs {
		g.Go(func() error {
			path := MediaPath(opts.MediaDir, t.file)
			if t.onDisk {
				r, err := inspect(gctx, path)
				if err != nil {
					return fmt.Errorf("inspecting %s: %w", t.file, err)
				}
				if !r.Silent {
					mu.Lock()
					defer mu.Unlock()
					manifest.put(entry(t))
					res.Reused++
					if r.Trimmed() {
						res.Trimmed++
					}
					finish(t, false, 0)
					return nil
				}
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			out, err := MakeClip(gctx, speak, inspect, t.text, path, opts.Attempts)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				return err
			}
			if out.Silent {
				manifest.drop(t.file)
				finish(t, true, out.Attempts)
				return nil
			}
			manifest.put(entry(t))
			res.Synthesized++
			if out.Trimmed {
				res.Trimmed++
			}
			finish(t, false, out.Attempts)
			return nil
		})
	}
	err := g.Wait()
	res.Manifest = manifest.list()
	res.Checks = checks
	slices.SortFunc(res.Failed, func(a, b ClipFailure) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(a.Field, b.Field)
	})
	return res, err
}
