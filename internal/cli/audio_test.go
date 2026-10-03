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
		if os.Getenv("REQUIRE_FFMPEG") != "" {
			t.Fatalf("REQUIRE_FFMPEG is set: %v", err)
		}
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
	if err == nil || !strings.Contains(err.Error(), "1 clip had no sound in any of 4 attempts and is flagged for 'arabic-vocab review'") || !strings.Contains(err.Error(), "tries again") {
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

	var printed bytes.Buffer
	s.print(&printed, paths)
	if strings.Contains(printed.String(), "✓ audio") || !strings.Contains(printed.String(), "1 clip came back silent and wait for review") {
		t.Errorf("the audio step is not done while a clip is silent:\n%s", printed.String())
	}

	n.Reviewed = []string{"ExampleAudio:silent"}
	if err := notes.WriteJSONL(paths.Notes(), []notes.Note{n}); err != nil {
		t.Fatal(err)
	}
	s = read()
	if s.major != 1 || s.silent != 1 || s.missing != 0 || s.next() != "review" {
		t.Errorf("a silent clip cannot be reviewed away, only made again: %+v, next %q", s, s.next())
	}

	flag.Digest = "an older text"
	if err := notes.WriteJSONL(paths.AudioQA(), []notes.Check{flag}); err != nil {
		t.Fatal(err)
	}
	if s = read(); s.silent != 0 || s.missing != 1 || s.next() != "audio" {
		t.Errorf("a flag about older text no longer explains the missing clip: %+v, next %q", s, s.next())
	}
}

func (d *audioDeck) preload(kinds map[string]string) {
	d.t.Helper()
	if err := os.MkdirAll(d.paths.Media(), 0o755); err != nil {
		d.t.Fatal(err)
	}
	for text, kind := range kinds {
		if err := os.WriteFile(d.clip(text), d.voice.clips[kind], 0o644); err != nil {
			d.t.Fatal(err)
		}
	}
}

func (d *audioDeck) snapshot() map[string]string {
	d.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(d.dir, func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		out[strings.TrimPrefix(path, d.dir)] = string(raw)
		return err
	})
	if err != nil {
		d.t.Fatal(err)
	}
	return out
}

func TestAudioDryRunReportsWhatWouldChangeAndChangesNothing(t *testing.T) {
	d := newAudioDeck(t)
	d.preload(map[string]string{d.word(0): "clean", d.sentence(0): "padded", d.word(1): "silent"})
	before := d.snapshot()

	out, err := d.run("audio", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"listened to 3 clips; 1 more clip is not made yet",
		"1 are fine as they are",
		"1 would be cut, 3.8 s of silence in all; the longest cuts:",
		"3.8 s  1\tكِتَاب\tsentence\t",
		"1 clip has no sound and would be made again:",
		"2\tقَلَم\tword",
		"nothing was changed; 'arabic-vocab audio' makes these changes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	after := d.snapshot()
	if len(before) != len(after) {
		t.Errorf("files before %d, after %d", len(before), len(after))
	}
	for name, content := range before {
		if after[name] != content {
			t.Errorf("%s was changed", name)
		}
	}
	if len(d.voice.calls) != 0 {
		t.Errorf("a dry run speaks nothing: %v", d.voice.calls)
	}
	if _, err := os.Stat(d.paths.AudioLock()); err == nil {
		t.Error("a dry run changes nothing, so it needs no lock")
	}
}

func TestAudioDryRunNeedsNeitherGoogleNorAQuietDeck(t *testing.T) {
	d := newAudioDeck(t)
	d.preload(map[string]string{d.word(0): "clean"})
	old := newSpeaker
	newSpeaker = func(context.Context, config.Credentials, deck.Voice) (deck.Speaker, io.Closer, error) {
		t.Error("a dry run must not connect to Google")
		return nil, nil, errors.New("no")
	}
	defer func() { newSpeaker = old }()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	if _, err := d.run("audio", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	unlock, err := deck.LockAudio(context.Background(), d.paths.AudioLock(), 0)
	if err != nil {
		t.Skip(err)
	}
	defer unlock()
	if _, err := d.run("audio", "--dry-run"); err != nil {
		t.Errorf("a dry run may look while another run works: %v", err)
	}
}

func TestAudioDryRunNeedsFFmpeg(t *testing.T) {
	d := newAudioDeck(t)
	t.Setenv("PATH", t.TempDir())
	_, err := d.run("audio", "--dry-run")
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "ffmpeg is needed to listen to the clips") {
		t.Fatalf("err = %v", err)
	}
}

func TestAudioDryRunWithNothingToCut(t *testing.T) {
	d := newAudioDeck(t)
	d.preload(map[string]string{d.word(0): "clean", d.sentence(0): "clean", d.word(1): "clean", d.sentence(1): "clean"})
	out, err := d.run("audio", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "listened to 4 clips\n  4 are fine as they are\n") || strings.Contains(out, "would be") {
		t.Errorf("report:\n%s", out)
	}
}

func TestAudioWaitsBrieflyForAnotherRunAndThenSaysSo(t *testing.T) {
	d := newAudioDeck(t)
	was := lockWait
	lockWait = 150 * time.Millisecond
	defer func() { lockWait = was }()
	unlock, err := deck.LockAudio(context.Background(), d.paths.AudioLock(), 0)
	if err != nil {
		t.Skip(err)
	}
	_, err = d.audio()
	if !errors.Is(err, deck.ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(d.voice.calls) != 0 || len(d.manifest()) != 0 {
		t.Error("nothing may be made while another run holds the lock")
	}
	unlock()
	if _, err := d.audio(); err != nil {
		t.Fatalf("after the other run finished: %v", err)
	}
}

func TestAudioDoesNotSaveAVoiceThatOnlyProducesSilence(t *testing.T) {
	d := newAudioDeck(t)
	for _, text := range []string{d.word(0), d.sentence(0), d.word(1), d.sentence(1)} {
		d.voice.script[text] = []string{"silent"}
	}
	_, err := d.run("audio", "--credentials", d.creds, "--voice", "ar-XA-Wavenet-A")
	if err == nil || !strings.Contains(err.Error(), "4 clips had no sound") {
		t.Fatalf("err = %v", err)
	}
	if saved, _ := deck.LoadSettings(d.paths.Settings()); saved.Voice == "ar-XA-Wavenet-A" {
		t.Error("a voice that only produced silence must not become the deck's voice")
	}
	if _, statErr := os.Stat(d.paths.Settings()); statErr == nil {
		t.Error("deck.json should be left alone")
	}

	d.voice.script[d.sentence(1)] = []string{"padded"}
	d.voice.calls = map[string]int{}
	_, err = d.run("audio", "--credentials", d.creds, "--voice", "ar-XA-Wavenet-A")
	if err == nil {
		t.Fatal("three clips are still silent")
	}
	if saved, _ := deck.LoadSettings(d.paths.Settings()); saved.Voice != "ar-XA-Wavenet-A" {
		t.Errorf("a voice that speaks at least some clips is kept: %q", saved.Voice)
	}
}

func TestAudioReplacesAClipItCannotReadAndReportsOnesThatStayBroken(t *testing.T) {
	d := newAudioDeck(t)
	d.voice.clips["broken"] = bytes.Repeat([]byte("not audio at all "), 100)
	if err := os.MkdirAll(d.paths.Media(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.clip(d.word(0)), d.voice.clips["broken"], 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := d.audio(); err != nil || out != "" {
		t.Fatalf("a clip on disk that cannot be read is made again: out %q, err %v", out, err)
	}
	if got := d.manifest(); len(got) != 4 {
		t.Fatalf("manifest = %+v", got)
	}
	if res, err := sound.Inspect(context.Background(), d.clip(d.word(0))); err != nil || res.Silent {
		t.Errorf("the replacement should be readable: %+v, %v", res, err)
	}

	d2 := newAudioDeck(t)
	d2.voice.clips["broken"] = bytes.Repeat([]byte("not audio at all "), 100)
	d2.voice.script[d2.sentence(1)] = []string{"broken"}
	out, err := d2.audio()
	if err == nil || !strings.Contains(err.Error(), "1 clip could not be read after it was made") || !strings.Contains(err.Error(), "tries again") {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out, "2\tقَلَم\tsentence\t") || !strings.Contains(out, d2.paths.Media()) || strings.Contains(out, ".make-") {
		t.Errorf("the failure should name the clip with its full path: %q", out)
	}
	if got := d2.manifest(); len(got) != 3 {
		t.Errorf("the other clips are still made: %+v", got)
	}
	if got := d2.checks(); len(got) != 0 {
		t.Errorf("an unreadable clip is not flagged for review: %+v", got)
	}
}

func TestAudioReportsSilentAndUnreadableClipsTogether(t *testing.T) {
	d := newAudioDeck(t)
	d.voice.clips["broken"] = bytes.Repeat([]byte("not audio at all "), 100)
	d.voice.script[d.word(0)] = []string{"silent"}
	d.voice.script[d.sentence(1)] = []string{"broken"}
	_, err := d.audio()
	want := "1 clip had no sound in any of 4 attempts and is flagged for 'arabic-vocab review' and 1 clip could not be read after it was made; the next 'arabic-vocab audio' tries again"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
}
