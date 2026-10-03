package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

type scriptedVoice struct {
	mu      sync.Mutex
	kinds   map[string][]string
	calls   map[string]int
	failing map[string]error
}

func newScriptedVoice() *scriptedVoice {
	return &scriptedVoice{kinds: map[string][]string{}, calls: map[string]int{}, failing: map[string]error{}}
}

func (v *scriptedVoice) speak(_ context.Context, text, path string) error {
	v.mu.Lock()
	n := v.calls[text]
	v.calls[text]++
	plan := v.kinds[text]
	failure := v.failing[text]
	v.mu.Unlock()
	if failure != nil {
		return failure
	}
	kind := "sound"
	if len(plan) > 0 {
		kind = plan[min(n, len(plan)-1)]
	}
	return os.WriteFile(path, []byte(kind), 0o644)
}

func (v *scriptedVoice) callsFor(text string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls[text]
}

func listen(_ context.Context, path string) (sound.Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sound.Result{}, err
	}
	if string(raw) == "silent" {
		return sound.Result{Silent: true}, nil
	}
	return sound.Result{Duration: time.Second, End: time.Second}, nil
}

func clipNote() notes.Note {
	return notes.Note{ID: "كِتَاب", Position: 1, Arabic: "كِتَاب", Pos: "noun", English: "book", Example: "هٰذَا <b>كِتَابٌ</b>.", ExampleEn: "This is a book."}
}

type clipFixture struct {
	t      *testing.T
	paths  *deck.Paths
	voice  *scriptedVoice
	clips  *clips
	note   notes.Note
	word   string
	phrase string
}

func newClipFixture(t *testing.T, checks ...notes.Check) *clipFixture {
	t.Helper()
	dir := t.TempDir()
	paths := &deck.Paths{Deck: filepath.Join(dir, "deck"), Cache: filepath.Join(dir, "cache")}
	if err := os.MkdirAll(paths.Media(), 0o755); err != nil {
		t.Fatal(err)
	}
	n := clipNote()
	f := &clipFixture{t: t, paths: paths, voice: newScriptedVoice(), note: n, word: deck.AudioTexts(n)[0].Text, phrase: deck.AudioTexts(n)[1].Text}
	f.clips = newClips(paths, deck.Voice{Name: "v", Rate: 0.9}, nil, checks)
	f.clips.speak, f.clips.inspect = f.voice.speak, listen
	return f
}

func (f *clipFixture) file(text string) string {
	return f.paths.MediaFile(deck.AudioFile(f.clips.voice.Key(), text))
}

func (f *clipFixture) storedChecks() []notes.Check {
	f.t.Helper()
	got, err := notes.ReadJSONL[notes.Check](f.paths.AudioQA())
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *clipFixture) storedManifest() []deck.ManifestEntry {
	f.t.Helper()
	got, err := notes.ReadJSONL[deck.ManifestEntry](f.paths.Manifest())
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *clipFixture) flagged(field string) notes.Check {
	return notes.Check{ID: f.note.ID, Version: deck.AudioCheckVersion, Digest: f.note.Digest(), Issues: []notes.Issue{deck.SilentIssue(field, 4)}}
}

func TestRemakeStoresTheClipAndClearsItsFlag(t *testing.T) {
	f := newClipFixture(t)
	f.clips.checks = []notes.Check{f.flagged("ExampleAudio")}
	issue, err := f.clips.remake(context.Background(), f.note, "ExampleAudio")
	if err != nil || issue != nil {
		t.Fatalf("issue %+v, err %v", issue, err)
	}
	if raw, _ := os.ReadFile(f.file(f.phrase)); string(raw) != "sound" {
		t.Errorf("clip = %q", raw)
	}
	manifest := f.storedManifest()
	if len(manifest) != 1 || manifest[0].Text != f.phrase || !manifest[0].Inspected || manifest[0].File != filepath.Base(f.file(f.phrase)) {
		t.Errorf("manifest = %+v", manifest)
	}
	if got := f.storedChecks(); len(got) != 0 {
		t.Errorf("the flag should be gone from disk: %+v", got)
	}
	if got := f.clips.path(f.note, "ExampleAudio"); got != f.file(f.phrase) {
		t.Errorf("the review page should find the new clip at %q, got %q", f.file(f.phrase), got)
	}
}

func TestRemakeThatStaysSilentFlagsTheClipAndKeepsNothing(t *testing.T) {
	f := newClipFixture(t)
	f.voice.kinds[f.phrase] = []string{"silent"}
	issue, err := f.clips.remake(context.Background(), f.note, "ExampleAudio")
	if err != nil || issue == nil || issue.Kind != deck.KindSilent || issue.Field != "ExampleAudio" || issue.Severity != notes.Major {
		t.Fatalf("issue %+v, err %v", issue, err)
	}
	if f.voice.callsFor(f.phrase) != deck.DefaultClipAttempts {
		t.Errorf("a silent clip is asked for %d times, got %d", deck.DefaultClipAttempts, f.voice.callsFor(f.phrase))
	}
	if _, err := os.Stat(f.file(f.phrase)); err == nil {
		t.Error("a silent clip must not be kept")
	}
	if got := f.storedChecks(); len(got) != 1 || got[0].ID != f.note.ID || len(got[0].Issues) != 1 || !reflect.DeepEqual(got[0].Issues[0], *issue) {
		t.Errorf("checks on disk = %+v", got)
	}
	if got := f.storedManifest(); len(got) != 0 {
		t.Errorf("manifest = %+v", got)
	}
	if got := f.clips.path(f.note, "ExampleAudio"); got != "" {
		t.Errorf("there is no clip to play: %q", got)
	}
}

func TestRemakeThatStaysSilentKeepsTheClipYouHad(t *testing.T) {
	f := newClipFixture(t)
	if _, err := f.clips.remake(context.Background(), f.note, "WordAudio"); err != nil {
		t.Fatal(err)
	}
	f.voice.kinds[f.word] = []string{"silent"}
	f.voice.calls[f.word] = 0
	issue, err := f.clips.remake(context.Background(), f.note, "WordAudio")
	if issue != nil || !errors.Is(err, deck.ErrSilent) || !strings.Contains(err.Error(), "the clip you had was kept") {
		t.Fatalf("issue %+v, err %v", issue, err)
	}
	if raw, _ := os.ReadFile(f.file(f.word)); string(raw) != "sound" {
		t.Errorf("the clip you had was lost: %q", raw)
	}
	if got := f.storedChecks(); len(got) != 0 {
		t.Errorf("a note with a working clip is not flagged: %+v", got)
	}
	if got := f.storedManifest(); len(got) != 1 {
		t.Errorf("manifest = %+v", got)
	}
}

func TestRemakeReportsErrorsWithoutTouchingTheFlags(t *testing.T) {
	f := newClipFixture(t)
	f.clips.checks = []notes.Check{f.flagged("WordAudio")}
	quota := errors.New("rpc error: code = ResourceExhausted")
	f.voice.failing[f.word] = quota
	if issue, err := f.clips.remake(context.Background(), f.note, "WordAudio"); issue != nil || !errors.Is(err, quota) {
		t.Fatalf("issue %+v, err %v", issue, err)
	}
	if len(f.clips.checks) != 1 {
		t.Errorf("flags = %+v", f.clips.checks)
	}
	if _, err := os.Stat(f.paths.AudioQA()); err == nil {
		t.Error("nothing should have been written")
	}
	if _, err := f.clips.remake(context.Background(), f.note, "FormsAudio"); err == nil || !strings.Contains(err.Error(), "has no forms to speak") {
		t.Errorf("a clip the note does not have: %v", err)
	}
}

func TestRemovingAClipDropsItsFileEntryAndFlag(t *testing.T) {
	f := newClipFixture(t)
	if _, err := f.clips.remake(context.Background(), f.note, "WordAudio"); err != nil {
		t.Fatal(err)
	}
	f.clips.checks = []notes.Check{f.flagged("WordAudio")}
	if err := f.clips.remove(f.note, "WordAudio"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.file(f.word)); err == nil {
		t.Error("the file should be deleted")
	}
	if got := f.storedManifest(); len(got) != 0 {
		t.Errorf("manifest = %+v", got)
	}
	if got := f.storedChecks(); len(got) != 0 {
		t.Errorf("checks = %+v", got)
	}
	if err := f.clips.remove(f.note, "WordAudio"); err != nil {
		t.Errorf("removing a clip that is already gone is fine: %v", err)
	}
}

func TestRemakingOneClipLeavesTheFlagOfAnotherAlone(t *testing.T) {
	f := newClipFixture(t)
	check := f.flagged("ExampleAudio")
	check.Issues = append(check.Issues, deck.SilentIssue("WordAudio", 4))
	f.clips.checks = []notes.Check{check}
	if _, err := f.clips.remake(context.Background(), f.note, "WordAudio"); err != nil {
		t.Fatal(err)
	}
	got := f.storedChecks()
	if len(got) != 1 || len(got[0].Issues) != 1 || got[0].Issues[0].Field != "ExampleAudio" {
		t.Errorf("checks = %+v", got)
	}
}

func TestRemakeNeedsFFmpegBeforeAnythingIsSpoken(t *testing.T) {
	f := newClipFixture(t)
	f.clips.speak = nil
	t.Setenv("PATH", t.TempDir())
	if _, err := f.clips.remake(context.Background(), f.note, "WordAudio"); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("err = %v", err)
	}
}

func TestClipsCanBeRemadeAtTheSameTime(t *testing.T) {
	f := newClipFixture(t)
	f.clips.checks = []notes.Check{f.flagged("ExampleAudio")}
	var wg sync.WaitGroup
	for _, field := range []string{"WordAudio", "ExampleAudio", "WordAudio", "ExampleAudio"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.clips.remake(context.Background(), f.note, field); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	manifest := f.storedManifest()
	texts := []string{}
	for _, m := range manifest {
		texts = append(texts, m.Text)
	}
	slices.Sort(texts)
	want := []string{f.word, f.phrase}
	slices.Sort(want)
	if !slices.Equal(texts, want) || len(f.storedChecks()) != 0 {
		t.Errorf("manifest %v, checks %+v", texts, f.storedChecks())
	}
}
