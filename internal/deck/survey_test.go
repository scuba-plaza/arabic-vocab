package deck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

func surveyClips(t *testing.T, dir string, contents map[string]string) {
	t.Helper()
	for text, content := range contents {
		if err := os.WriteFile(clipPath(dir, text), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSurveyAudioSortsClipsByWhatWouldHappenToThemAndChangesNothing(t *testing.T) {
	dir := t.TempDir()
	a, b, c := clipNote("كِتَاب", 1), clipNote("قَلَم", 2), clipNote("بَاب", 3)
	surveyClips(t, dir, map[string]string{
		wordOf(a): "sound", sentenceOf(a): "trim",
		wordOf(b): "silent", sentenceOf(b): "broken",
		wordOf(c): "trim",
	})
	before := map[string]string{}
	for _, name := range filesIn(t, dir) {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		before[name] = string(raw)
	}
	st := newStudio()
	var progress []int
	survey, err := SurveyAudio(context.Background(), []notes.Note{a, b, c}, testVoice, dir, st.inspect, 2, func(done, total int) { progress = append(progress, done*10+total) })
	if err != nil {
		t.Fatal(err)
	}
	if len(survey.Clips) != 5 || survey.Missing != 1 || survey.Untouched() != 1 {
		t.Fatalf("survey = %+v", survey)
	}
	if trimmed := survey.Trimmed(); len(trimmed) != 2 || trimmed[0].Cut() != 2*time.Second {
		t.Errorf("trimmed = %+v", trimmed)
	}
	if got := survey.Silent(); len(got) != 1 || got[0].ID != b.ID || got[0].Field != "WordAudio" {
		t.Errorf("silent = %+v", got)
	}
	if got := survey.Unreadable(); len(got) != 1 || got[0].Field != "ExampleAudio" || got[0].Err == nil {
		t.Errorf("unreadable = %+v", got)
	}
	if survey.Cut() != 4*time.Second {
		t.Errorf("cut = %v", survey.Cut())
	}
	if !slices.IsSorted(progress) || len(progress) != 5 {
		t.Errorf("progress = %v", progress)
	}
	positions := []int{}
	for _, c := range survey.Clips {
		positions = append(positions, c.Position)
	}
	if !slices.IsSorted(positions) {
		t.Errorf("clips should come in deck order: %v", positions)
	}
	for _, name := range filesIn(t, dir) {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		if before[name] != string(raw) {
			t.Errorf("%s was changed", name)
		}
	}
	if len(filesIn(t, dir)) != len(before) || st.callsFor(wordOf(a)) != 0 {
		t.Error("a survey changes nothing and speaks nothing")
	}
}

func TestSurveyAudioCountsASharedClipOnceAndSkipsUnwrittenNotes(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	b.Example = a.Example
	draft := notes.Note{ID: "x", Position: 3, Arabic: "x"}
	surveyClips(t, dir, map[string]string{wordOf(a): "sound", sentenceOf(a): "sound", wordOf(b): "sound"})
	survey, err := SurveyAudio(context.Background(), []notes.Note{a, b, draft}, testVoice, dir, newStudio().inspect, 0, nil)
	if err != nil || len(survey.Clips) != 3 || survey.Missing != 0 {
		t.Fatalf("survey = %+v, err %v", survey, err)
	}
}

func TestSurveyAudioStopsWhenTheContextIsCancelled(t *testing.T) {
	dir := t.TempDir()
	n := clipNote("كِتَاب", 1)
	surveyClips(t, dir, map[string]string{wordOf(n): "sound", sentenceOf(n): "sound"})
	ctx, cancel := context.WithCancel(context.Background())
	inspect := func(ctx context.Context, path string) (sound.Result, error) {
		cancel()
		return sound.Result{}, ctx.Err()
	}
	if _, err := SurveyAudio(ctx, []notes.Note{n}, testVoice, dir, inspect, 1, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestSurveyClipCutIsZeroUnlessTheClipIsTrimmed(t *testing.T) {
	trimmed := SurveyClip{Result: sound.Result{Duration: 5 * time.Second, Start: time.Second, End: 4 * time.Second}}
	if trimmed.Cut() != 2*time.Second {
		t.Errorf("cut = %v", trimmed.Cut())
	}
	for name, c := range map[string]SurveyClip{
		"silent": {Result: sound.Result{Silent: true, Duration: time.Second}},
		"whole":  {Result: sound.Result{Duration: time.Second, End: time.Second}},
		"error":  {Result: sound.Result{Duration: 5 * time.Second, Start: time.Second, End: 4 * time.Second}, Err: errors.New("x")},
	} {
		if c.Cut() != 0 {
			t.Errorf("%s: cut = %v", name, c.Cut())
		}
	}
}
