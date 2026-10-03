package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-tts/config"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func needFFmpeg(t *testing.T) {
	t.Helper()
	if err := sound.Available(); err != nil {
		t.Skip(err)
	}
}

func makeMP3(t *testing.T, lead, speech, tail time.Duration) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.mp3")
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-y"}
	if speech == 0 {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono", "-t", fmt.Sprintf("%.3f", (lead+tail).Seconds()))
	} else {
		filters := "volume=0.3"
		if lead > 0 {
			filters += fmt.Sprintf(",adelay=%d:all=1", lead.Milliseconds())
		}
		if tail > 0 {
			filters += fmt.Sprintf(",apad=pad_dur=%.3f", tail.Seconds())
		}
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=%.3f:sample_rate=24000", speech.Seconds()), "-af", filters)
	}
	args = append(args, "-ac", "1", "-ar", "24000", "-b:a", "32k", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("making a fixture: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type studioVoice struct {
	mu      sync.Mutex
	clips   map[string][]byte
	script  map[string][]string
	calls   map[string]int
	failing map[string]error
}

func (v *studioVoice) speak(_ context.Context, text, path string) error {
	v.mu.Lock()
	n := v.calls[text]
	v.calls[text]++
	plan := v.script[text]
	failure := v.failing[text]
	v.mu.Unlock()
	if failure != nil {
		return failure
	}
	kind := "padded"
	if len(plan) > 0 {
		kind = plan[min(n, len(plan)-1)]
	}
	return os.WriteFile(path, v.clips[kind], 0o644)
}

func (v *studioVoice) callsFor(text string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls[text]
}

type audioDeck struct {
	t     *testing.T
	dir   string
	paths *deck.Paths
	voice *studioVoice
	creds string
	ns    []notes.Note
}

func newAudioDeck(t *testing.T) *audioDeck {
	t.Helper()
	needFFmpeg(t)
	dir := t.TempDir()
	d := &audioDeck{
		t: t, dir: dir,
		paths: &deck.Paths{Deck: filepath.Join(dir, "deck"), Cache: filepath.Join(dir, "cache")},
		voice: &studioVoice{
			clips: map[string][]byte{
				"padded": makeMP3(t, 2*time.Second, time.Second, 2*time.Second),
				"clean":  makeMP3(t, 100*time.Millisecond, time.Second, 100*time.Millisecond),
				"silent": makeMP3(t, 3*time.Second, 0, 0),
			},
			script: map[string][]string{}, calls: map[string]int{}, failing: map[string]error{},
		},
		creds: filepath.Join(dir, "key.json"),
	}
	if err := os.WriteFile(d.creds, []byte(`{"type":"service_account","project_id":"p","client_email":"e@p"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "ScheherazadeNew-Regular.ttf"), []byte("font"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := notes.Note{ID: "كِتَاب", Position: 1, Arabic: "كِتَاب", Pos: "noun", English: "book", Example: "هٰذَا <b>كِتَابٌ</b>.", ExampleEn: "This is a book."}
	second := notes.Note{ID: "قَلَم", Position: 2, Arabic: "قَلَم", Pos: "noun", English: "pen", Example: "هٰذَا <b>قَلَمٌ</b>.", ExampleEn: "This is a pen."}
	d.ns = []notes.Note{first, second}
	if err := notes.WriteJSONL(d.paths.Notes(), d.ns); err != nil {
		t.Fatal(err)
	}
	var checks []notes.Check
	for _, n := range d.ns {
		checks = append(checks, notes.Check{ID: n.ID, Version: deck.CheckVersion, Digest: n.Digest()})
	}
	if err := notes.WriteJSONL(d.paths.QA(), checks); err != nil {
		t.Fatal(err)
	}
	old := newSpeaker
	newSpeaker = func(context.Context, config.Credentials, deck.Voice) (deck.Speaker, io.Closer, error) {
		return d.voice.speak, nopCloser{}, nil
	}
	t.Cleanup(func() { newSpeaker = old })
	return d
}

func (d *audioDeck) word(i int) string     { return deck.AudioTexts(d.ns[i])[0].Text }
func (d *audioDeck) sentence(i int) string { return deck.AudioTexts(d.ns[i])[1].Text }

func (d *audioDeck) clip(text string) string {
	return d.paths.MediaFile(deck.AudioFile(deck.DefaultSettings().AudioVoice().Key(), text))
}

func (d *audioDeck) run(command string, args ...string) (string, error) {
	d.t.Helper()
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{command, "-q", "--deck-dir", d.paths.Deck, "--cache", d.paths.Cache}, args...))
	err := root.Execute()
	return out.String(), err
}

func (d *audioDeck) audio() (string, error) {
	d.t.Helper()
	return d.run("audio", "--credentials", d.creds, "--concurrency", "2")
}

func (d *audioDeck) checks() []notes.Check {
	d.t.Helper()
	got, err := notes.ReadJSONL[notes.Check](d.paths.AudioQA())
	if err != nil {
		d.t.Fatal(err)
	}
	return got
}

func (d *audioDeck) manifest() []deck.ManifestEntry {
	d.t.Helper()
	got, err := notes.ReadJSONL[deck.ManifestEntry](d.paths.Manifest())
	if err != nil {
		d.t.Fatal(err)
	}
	return got
}

func (d *audioDeck) status() string {
	d.t.Helper()
	out, err := d.run("status")
	if err != nil {
		d.t.Fatal(err)
	}
	return out
}

func TestAudioCutsTheSilenceOffEveryClip(t *testing.T) {
	d := newAudioDeck(t)
	out, err := d.audio()
	if err != nil || out != "" {
		t.Fatalf("out %q, err %v", out, err)
	}
	if got := d.manifest(); len(got) != 4 {
		t.Fatalf("manifest = %+v", got)
	}
	for _, e := range d.manifest() {
		if !e.Inspected {
			t.Errorf("%+v", e)
		}
		res, err := sound.Trim(context.Background(), d.paths.MediaFile(e.File))
		if err != nil || res.Silent || res.Trimmed() {
			t.Errorf("%s should already be trimmed: %+v, %v", e.File, res, err)
		}
		if res.Duration > 1500*time.Millisecond {
			t.Errorf("%s is still %v long", e.File, res.Duration)
		}
	}
	if got := d.checks(); len(got) != 0 {
		t.Errorf("checks = %+v", got)
	}
	status := d.status()
	if !strings.Contains(status, "✓ audio") || !strings.Contains(status, "all 4 clips") || !strings.Contains(status, "next: arabic-vocab build") {
		t.Errorf("status:\n%s", status)
	}
	if d.voice.callsFor(d.word(0)) != 1 {
		t.Error("a clip with sound is asked for once")
	}
}

func TestAudioLeavesCleanClipsUntouched(t *testing.T) {
	d := newAudioDeck(t)
	for _, text := range []string{d.word(0), d.sentence(0), d.word(1), d.sentence(1)} {
		d.voice.script[text] = []string{"clean"}
	}
	if _, err := d.audio(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{d.word(0), d.sentence(1)} {
		raw, err := os.ReadFile(d.clip(text))
		if err != nil || !bytes.Equal(raw, d.voice.clips["clean"]) {
			t.Errorf("a clip without surplus silence must stay exactly as it was made (%v)", err)
		}
	}
}

func TestAudioFlagsAClipThatStaysSilentAndSaysSo(t *testing.T) {
	d := newAudioDeck(t)
	d.voice.script[d.sentence(1)] = []string{"silent"}

	out, err := d.audio()
	if err == nil || !strings.Contains(err.Error(), "1 clip had no sound in any of 4 attempts") || !strings.Contains(err.Error(), "'arabic-vocab review'") || !strings.Contains(err.Error(), "tries them again") {
		t.Fatalf("err = %v", err)
	}
	if want := "2\tقَلَم\tsentence\tthe clip has no sound after 4 attempts\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if d.voice.callsFor(d.sentence(1)) != 4 {
		t.Errorf("calls = %d, want one try and three retries", d.voice.callsFor(d.sentence(1)))
	}
	if _, err := os.Stat(d.clip(d.sentence(1))); err == nil {
		t.Error("the silent clip must not be kept")
	}
	if got := d.manifest(); len(got) != 3 {
		t.Errorf("manifest = %+v", got)
	}
	checks := d.checks()
	if len(checks) != 1 || checks[0].ID != "قَلَم" || len(checks[0].Issues) != 1 || checks[0].Issues[0].Field != "ExampleAudio" || checks[0].Issues[0].Kind != deck.KindSilent {
		t.Fatalf("checks = %+v", checks)
	}

	status := d.status()
	for _, want := range []string{"→ review", "1 note with major flags", "3 of 4 clips made; 1 clip came back silent and wait for review", "next: arabic-vocab review"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "missing") {
		t.Errorf("a clip that came back silent is not simply missing:\n%s", status)
	}

	if _, err := d.run("build", "--out", filepath.Join(d.dir, "deck.apkg")); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(d.dir, "deck.apkg")); err != nil || st.Size() == 0 {
		t.Fatalf("the package was not written: %v", err)
	}
	pkg, summary, err := deck.BuildPackage(d.ns, nil, checks, deck.BuildOptions{MediaDir: d.paths.Media(), Audio: deck.AudioIndex(d.manifest())})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(pkg.Notes[1].Tags, "check::audio") || slices.Contains(pkg.Notes[0].Tags, "check::audio") || summary.Tagged["check::audio"] != 1 {
		t.Errorf("tags: %v / %v", pkg.Notes[0].Tags, pkg.Notes[1].Tags)
	}
}

func TestAudioTriesAFlaggedClipAgainOnTheNextRun(t *testing.T) {
	d := newAudioDeck(t)
	d.voice.script[d.sentence(1)] = []string{"silent"}
	if _, err := d.audio(); err == nil {
		t.Fatal("the first run should report the silent clip")
	}

	if _, err := d.audio(); err == nil || d.voice.callsFor(d.sentence(1)) != 8 {
		t.Fatalf("a clip that is still silent is flagged again after %d calls: %v", d.voice.callsFor(d.sentence(1)), err)
	}
	if len(d.checks()) != 1 {
		t.Errorf("checks = %+v", d.checks())
	}

	d.voice.script[d.sentence(1)] = []string{"silent", "silent", "padded"}
	d.voice.calls[d.sentence(1)] = 0
	out, err := d.audio()
	if err != nil || out != "" {
		t.Fatalf("out %q, err %v", out, err)
	}
	if d.voice.callsFor(d.sentence(1)) != 3 || d.voice.callsFor(d.word(0)) != 1 {
		t.Errorf("only the flagged clip is made again, and it stops once it has sound: %d / %d calls", d.voice.callsFor(d.sentence(1)), d.voice.callsFor(d.word(0)))
	}
	if got := d.checks(); len(got) != 0 {
		t.Errorf("the flag should be gone: %+v", got)
	}
	if status := d.status(); !strings.Contains(status, "all 4 clips") || strings.Contains(status, "silent") || !strings.Contains(status, "next: arabic-vocab build") {
		t.Errorf("status:\n%s", status)
	}
}

func TestAudioListensToClipsMadeBeforeTheCheckExisted(t *testing.T) {
	d := newAudioDeck(t)
	if err := os.MkdirAll(d.paths.Media(), 0o755); err != nil {
		t.Fatal(err)
	}
	old := map[string]string{d.word(0): "clean", d.sentence(0): "padded", d.word(1): "silent", d.sentence(1): "padded"}
	var manifest []deck.ManifestEntry
	voice := deck.DefaultSettings().AudioVoice()
	for text, kind := range old {
		if err := os.WriteFile(d.clip(text), d.voice.clips[kind], 0o644); err != nil {
			t.Fatal(err)
		}
		manifest = append(manifest, deck.ManifestEntry{Text: text, Voice: voice.Name, Rate: voice.Rate, File: deck.AudioFile(voice.Key(), text)})
	}
	if err := notes.WriteJSONL(d.paths.Manifest(), manifest); err != nil {
		t.Fatal(err)
	}
	cleanBefore, _ := os.ReadFile(d.clip(d.word(0)))

	if _, err := d.audio(); err != nil {
		t.Fatal(err)
	}
	if d.voice.callsFor(d.word(1)) != 1 || d.voice.callsFor(d.word(0)) != 0 || d.voice.callsFor(d.sentence(0)) != 0 {
		t.Errorf("only the silent clip should be made again: %v", d.voice.calls)
	}
	if after, _ := os.ReadFile(d.clip(d.word(0))); !bytes.Equal(cleanBefore, after) {
		t.Error("a good clip must not change")
	}
	res, err := sound.Trim(context.Background(), d.clip(d.sentence(0)))
	if err != nil || res.Trimmed() || res.Duration > 1500*time.Millisecond {
		t.Errorf("the padded clip should have been trimmed: %+v, %v", res, err)
	}
	for _, e := range d.manifest() {
		if !e.Inspected {
			t.Errorf("%+v was not marked", e)
		}
	}

	calls := len(d.voice.calls)
	if _, err := d.audio(); err != nil || len(d.voice.calls) != calls {
		t.Errorf("a second run has nothing to do: %v", err)
	}
}

func TestAudioKeepsFinishedClipsWhenTheVoiceFails(t *testing.T) {
	d := newAudioDeck(t)
	quota := errors.New("rpc error: code = ResourceExhausted")
	d.voice.failing[d.sentence(1)] = quota
	d.voice.script[d.word(0)] = []string{"silent"}
	_, err := d.run("audio", "--credentials", d.creds, "--concurrency", "1")
	if !errors.Is(err, quota) {
		t.Fatalf("err = %v", err)
	}
	if len(d.manifest()) == 0 {
		t.Error("the clips that were finished should be saved")
	}
	if got := d.checks(); len(got) != 1 || got[0].ID != "كِتَاب" {
		t.Errorf("what was learnt before the error is kept: %+v", got)
	}
}

func TestAudioNeedsFFmpeg(t *testing.T) {
	d := newAudioDeck(t)
	t.Setenv("PATH", t.TempDir())
	_, err := d.audio()
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "ffmpeg is needed to cut the silence off the clips") {
		t.Fatalf("err = %v", err)
	}
	if len(d.voice.calls) != 0 {
		t.Error("nothing should be spoken without ffmpeg")
	}
	if _, err := os.Stat(d.paths.Manifest()); err == nil {
		t.Error("nothing should be written")
	}
}

func TestStatusCountsSilentClipsApartFromMissingOnes(t *testing.T) {
	dir := t.TempDir()
	paths := &deck.Paths{Deck: filepath.Join(dir, "deck"), Cache: filepath.Join(dir, "cache")}
	n := notes.Note{ID: "كِتَاب", Position: 1, Arabic: "كِتَاب", Pos: "noun", English: "book", Example: "هٰذَا <b>كِتَابٌ</b>.", ExampleEn: "This is a book."}
	if err := notes.WriteJSONL(paths.Notes(), []notes.Note{n}); err != nil {
		t.Fatal(err)
	}
	if err := notes.WriteJSONL(paths.QA(), []notes.Check{{ID: n.ID, Version: deck.CheckVersion, Digest: n.Digest()}}); err != nil {
		t.Fatal(err)
	}
	voice := deck.DefaultSettings().AudioVoice()
	if err := os.MkdirAll(paths.Media(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MediaFile(deck.AudioFile(voice.Key(), deck.AudioTexts(n)[0].Text)), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	flag := notes.Check{ID: n.ID, Version: deck.AudioCheckVersion, Digest: n.Digest(), Issues: []notes.Issue{deck.SilentIssue("ExampleAudio", 4)}}
	if err := notes.WriteJSONL(paths.AudioQA(), []notes.Check{flag}); err != nil {
		t.Fatal(err)
	}
	read := func() deckStatus {
		s, err := readStatus(paths)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	s := read()
	if s.clips != 2 || s.silent != 1 || s.missing != 0 || s.major != 1 || s.next() != "review" {
		t.Fatalf("status = %+v, next %q", s, s.next())
	}

	n.Reviewed = []string{"ExampleAudio:silent"}
	if err := notes.WriteJSONL(paths.Notes(), []notes.Note{n}); err != nil {
		t.Fatal(err)
	}
	s = read()
	if s.major != 0 || s.silent != 1 || s.missing != 0 || s.next() != "build" {
		t.Errorf("once reviewed, a silent clip must not keep sending you back to audio: %+v, next %q", s, s.next())
	}

	flag.Digest = "an older text"
	if err := notes.WriteJSONL(paths.AudioQA(), []notes.Check{flag}); err != nil {
		t.Fatal(err)
	}
	if s = read(); s.silent != 0 || s.missing != 1 || s.next() != "audio" {
		t.Errorf("a flag about older text no longer explains the missing clip: %+v, next %q", s, s.next())
	}
}
