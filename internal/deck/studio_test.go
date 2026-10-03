package deck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

type studio struct {
	mu        sync.Mutex
	script    map[string][]string
	failing   map[string]error
	calls     map[string]int
	inspected []string
}

func newStudio() *studio {
	return &studio{script: map[string][]string{}, failing: map[string]error{}, calls: map[string]int{}}
}

func (s *studio) say(text string, outcomes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script[text] = outcomes
	s.calls[text] = 0
}

func (s *studio) speak(_ context.Context, text, path string) error {
	s.mu.Lock()
	n := s.calls[text]
	s.calls[text]++
	plan := s.script[text]
	failure := s.failing[text]
	s.mu.Unlock()
	kind := "sound"
	if len(plan) > 0 {
		kind = plan[min(n, len(plan)-1)]
	}
	if failure != nil {
		os.WriteFile(path, []byte("partial"), 0o644)
		return failure
	}
	return os.WriteFile(path, []byte(kind), 0o644)
}

func (s *studio) inspect(_ context.Context, path string) (sound.Result, error) {
	s.mu.Lock()
	s.inspected = append(s.inspected, filepath.Base(path))
	s.mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return sound.Result{}, err
	}
	switch string(raw) {
	case "silent", "":
		return sound.Result{Silent: true}, nil
	case "trim":
		return sound.Result{Duration: 5 * time.Second, Start: time.Second, End: 4 * time.Second}, nil
	case "broken":
		return sound.Result{}, errors.New("not an audio file")
	}
	return sound.Result{Duration: 2 * time.Second, End: 2 * time.Second}, nil
}

func (s *studio) callsFor(text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[text]
}

func (s *studio) inspections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inspected)
}

var testVoice = Voice{Name: "v", Rate: 0.9}

func clipNote(id string, pos int) notes.Note {
	n := note(id, pos)
	n.Example = "هٰذَا <b>" + id + "</b>."
	return n
}

func options(dir string) AudioOptions {
	return AudioOptions{Voice: testVoice, MediaDir: dir, Concurrency: 1}
}

func wordOf(n notes.Note) string {
	return AudioTexts(n)[0].Text
}

func sentenceOf(n notes.Note) string {
	return AudioTexts(n)[1].Text
}

func clipPath(dir, text string) string {
	return filepath.Join(dir, AudioFile(testVoice.Key(), text))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func manifestFiles(m []ManifestEntry) []string {
	var out []string
	for _, e := range m {
		out = append(out, e.File)
	}
	slices.Sort(out)
	return out
}

func run(t *testing.T, st *studio, ns []notes.Note, manifest []ManifestEntry, checks []notes.Check, opts AudioOptions) *AudioResult {
	t.Helper()
	res, err := GenerateAudio(context.Background(), ns, manifest, checks, st.speak, st.inspect, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
