package deck

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

func TestGenerateAudioMakesEachClipOnceAndRemembersItWasInspected(t *testing.T) {
	dir := t.TempDir()
	first := clipNote("كِتَاب", 1)
	first.Forms = []notes.Form{{Label: "pl.", Arabic: "كُتُب"}}
	second := clipNote("قَلَم", 2)
	second.Example = "هٰذَا <b>قَلَمٌ</b>."
	ns := []notes.Note{first, second}
	st := newStudio()

	res := run(t, st, ns, nil, nil, options(dir))
	if res.Synthesized != 5 || res.Reused != 0 || res.Trimmed != 0 || len(res.Manifest) != 5 || len(res.Failed) != 0 || len(res.Checks) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if st.callsFor("هٰذَا قَلَمْ.") != 1 {
		t.Errorf("the example should be spoken once, with a pausal ending: %v", st.calls)
	}
	for _, e := range res.Manifest {
		if !e.Inspected || e.Voice != "v" || e.Rate != 0.9 || !exists(filepath.Join(dir, e.File)) {
			t.Errorf("manifest entry %+v", e)
		}
	}
	if got := filesIn(t, dir); !slices.Equal(got, manifestFiles(res.Manifest)) {
		t.Errorf("files %v do not match the manifest %v", got, manifestFiles(res.Manifest))
	}

	spoken, looked := len(st.calls), st.inspections()
	again := run(t, st, ns, res.Manifest, res.Checks, options(dir))
	if again.Synthesized != 0 || again.Reused != 5 || len(again.Manifest) != 5 {
		t.Errorf("second run should reuse every clip: %+v", again)
	}
	if st.inspections() != looked || len(st.calls) != spoken {
		t.Error("clips that were already inspected must be neither made nor looked at again")
	}

	pkg, summary, err := BuildPackage(ns, nil, again.Checks, BuildOptions{Audio: AudioIndex(again.Manifest), MediaDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AudioFiles != 5 || summary.MissingAudio != 0 {
		t.Errorf("summary %+v", summary)
	}
	if !strings.HasPrefix(pkg.Notes[0].Fields[fieldIndex("WordAudio")], "[sound:ar-") {
		t.Errorf("word audio field = %q", pkg.Notes[0].Fields[fieldIndex("WordAudio")])
	}
}

func TestGenerateAudioTriesSilentClipsAgainAndFlagsTheOnesThatStaySilent(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	st := newStudio()
	st.say(sentenceOf(b), "silent")

	res := run(t, st, []notes.Note{a, b}, nil, nil, options(dir))
	if got := st.callsFor(sentenceOf(b)); got != 4 {
		t.Errorf("a silent clip should be tried once and then three more times, got %d calls", got)
	}
	if res.Synthesized != 3 || len(res.Manifest) != 3 {
		t.Errorf("the other clips are unaffected: %+v", res)
	}
	if len(res.Failed) != 1 {
		t.Fatalf("failures = %+v", res.Failed)
	}
	f := res.Failed[0]
	if f.ID != b.ID || f.Position != 2 || f.Field != "ExampleAudio" || !errors.Is(f.Err, ErrSilent) || f.Err.Error() != "the clip has no sound after 4 attempts" {
		t.Errorf("failure = %+v (%v)", f, f.Err)
	}
	want := []notes.Check{{ID: b.ID, Version: AudioCheckVersion, Digest: b.Digest(), Issues: []notes.Issue{SilentIssue("ExampleAudio", 4)}}}
	if !checksEqual(res.Checks, want) {
		t.Errorf("checks = %+v, want %+v", res.Checks, want)
	}
	if exists(clipPath(dir, sentenceOf(b))) || slices.Contains(manifestFiles(res.Manifest), AudioFile(testVoice.Key(), sentenceOf(b))) {
		t.Error("a clip without sound must neither stay on disk nor be listed in the manifest")
	}
	if got := filesIn(t, dir); len(got) != 3 {
		t.Errorf("files left behind: %v", got)
	}
	if issues := OpenIssues(b, res.Checks[0]); len(issues) != 1 || issues[0].Severity != notes.Major || issues[0].Kind != KindSilent {
		t.Errorf("the flag should be open and major: %+v", issues)
	}
}

func checksEqual(a, b []notes.Check) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestGenerateAudioStopsTryingOnceAClipHasSound(t *testing.T) {
	for retries, calls := range map[int]int{0: 1, 1: 2, 2: 3, 3: 4} {
		dir := t.TempDir()
		n := clipNote("كِتَاب", 1)
		st := newStudio()
		outcomes := make([]string, retries)
		for i := range outcomes {
			outcomes[i] = "silent"
		}
		st.say(wordOf(n), append(outcomes, "sound")...)
		res := run(t, st, []notes.Note{n}, nil, nil, options(dir))
		if got := st.callsFor(wordOf(n)); got != calls || len(res.Failed) != 0 || len(res.Checks) != 0 || res.Synthesized != 2 {
			t.Errorf("%d silent answers: %d calls, result %+v", retries, got, res)
		}
		if !exists(clipPath(dir, wordOf(n))) {
			t.Errorf("%d silent answers: the clip that finally had sound should be kept", retries)
		}
	}
}

func TestGenerateAudioHonoursTheAttemptLimit(t *testing.T) {
	for attempts, want := range map[int]string{1: "the clip has no sound after 1 attempt", 2: "the clip has no sound after 2 attempts", 7: "the clip has no sound after 7 attempts"} {
		n := clipNote("كِتَاب", 1)
		st := newStudio()
		st.say(wordOf(n), "silent")
		opts := options(t.TempDir())
		opts.Attempts = attempts
		res := run(t, st, []notes.Note{n}, nil, nil, opts)
		if st.callsFor(wordOf(n)) != attempts || len(res.Failed) != 1 || res.Failed[0].Err.Error() != want {
			t.Errorf("%d attempts: %d calls, failures %+v", attempts, st.callsFor(wordOf(n)), res.Failed)
		}
	}
}

func TestGenerateAudioFlagsEveryNoteThatSharesASilentClip(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	b.Example = a.Example
	st := newStudio()
	st.say(sentenceOf(a), "silent")
	res := run(t, st, []notes.Note{a, b}, nil, nil, options(dir))
	if st.callsFor(sentenceOf(a)) != 4 {
		t.Errorf("a shared clip is made once, not once per note: %d calls", st.callsFor(sentenceOf(a)))
	}
	if len(res.Failed) != 2 || res.Failed[0].ID != a.ID || res.Failed[1].ID != b.ID || len(res.Checks) != 2 {
		t.Fatalf("both notes need the flag: %+v / %+v", res.Failed, res.Checks)
	}

	st.say(sentenceOf(a))
	fixed := run(t, st, []notes.Note{a, b}, res.Manifest, res.Checks, options(dir))
	if len(fixed.Failed) != 0 || len(fixed.Checks) != 0 || fixed.Synthesized != 1 {
		t.Errorf("one good clip should clear both flags: %+v", fixed)
	}
}

func TestGenerateAudioTriesFlaggedClipsAgainOnTheNextRun(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	ns := []notes.Note{a, b}
	st := newStudio()
	st.say(sentenceOf(b), "silent")
	first := run(t, st, ns, nil, nil, options(dir))

	again := run(t, st, ns, first.Manifest, first.Checks, options(dir))
	if st.callsFor(sentenceOf(b)) != 8 || len(again.Failed) != 1 || len(again.Checks) != 1 || again.Reused != 3 || again.Synthesized != 0 {
		t.Errorf("a clip that is still silent stays flagged: calls %d, %+v", st.callsFor(sentenceOf(b)), again)
	}

	st.say(sentenceOf(b))
	fixed := run(t, st, ns, again.Manifest, again.Checks, options(dir))
	if len(fixed.Failed) != 0 || len(fixed.Checks) != 0 || fixed.Synthesized != 1 || fixed.Reused != 3 || len(fixed.Manifest) != 4 {
		t.Errorf("a clip that comes back with sound clears its flag: %+v", fixed)
	}
	if st.callsFor(wordOf(a)) != 1 {
		t.Error("clips that were fine must not be made again")
	}
}

func TestGenerateAudioInspectsClipsThatAreAlreadyOnDisk(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	for text, content := range map[string]string{wordOf(a): "sound", sentenceOf(a): "sound", wordOf(b): "silent", sentenceOf(b): "trim"} {
		if err := os.WriteFile(clipPath(dir, text), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := newStudio()
	res := run(t, st, []notes.Note{a, b}, nil, nil, options(dir))
	if res.Reused != 3 || res.Synthesized != 1 || res.Trimmed != 1 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if st.callsFor(wordOf(b)) != 1 || st.callsFor(wordOf(a)) != 0 {
		t.Errorf("only the silent clip should be made again: %v", st.calls)
	}
	if raw, _ := os.ReadFile(clipPath(dir, wordOf(b))); string(raw) != "sound" {
		t.Errorf("the silent clip should have been replaced, found %q", raw)
	}
	for _, e := range res.Manifest {
		if !e.Inspected {
			t.Errorf("%+v was not marked as inspected", e)
		}
	}
	if len(res.Manifest) != 4 {
		t.Errorf("manifest = %+v", res.Manifest)
	}

	looked := st.inspections()
	run(t, st, []notes.Note{a, b}, res.Manifest, res.Checks, options(dir))
	if st.inspections() != looked {
		t.Error("the second run should trust the manifest")
	}
}

func TestGenerateAudioInspectsClipsRecordedByAnOlderManifest(t *testing.T) {
	dir := t.TempDir()
	n := clipNote("كِتَاب", 1)
	var old []ManifestEntry
	raw := `[{"text":"` + wordOf(n) + `","voice":"v","rate":0.9,"file":"` + AudioFile(testVoice.Key(), wordOf(n)) + `"},{"text":"` + sentenceOf(n) + `","voice":"v","rate":0.9,"file":"` + AudioFile(testVoice.Key(), sentenceOf(n)) + `"}]`
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(clipPath(dir, wordOf(n)), []byte("silent"), 0o644)
	os.WriteFile(clipPath(dir, sentenceOf(n)), []byte("trim"), 0o644)
	st := newStudio()
	res := run(t, st, []notes.Note{n}, old, nil, options(dir))
	if res.Synthesized != 1 || res.Reused != 1 || res.Trimmed != 1 || len(res.Manifest) != 2 {
		t.Errorf("result = %+v", res)
	}
	for _, e := range res.Manifest {
		if !e.Inspected {
			t.Errorf("%+v", e)
		}
	}
}

func TestGenerateAudioFlagsAnOldSilentClipThatCannotBeMadeAgain(t *testing.T) {
	dir := t.TempDir()
	n := clipNote("كِتَاب", 1)
	os.WriteFile(clipPath(dir, wordOf(n)), []byte("silent"), 0o644)
	st := newStudio()
	st.say(wordOf(n), "silent")
	old := []ManifestEntry{{Text: wordOf(n), Voice: "v", Rate: 0.9, File: AudioFile(testVoice.Key(), wordOf(n))}}
	res := run(t, st, []notes.Note{n}, old, nil, options(dir))
	if len(res.Failed) != 1 || res.Failed[0].Field != "WordAudio" || exists(clipPath(dir, wordOf(n))) {
		t.Fatalf("result = %+v", res)
	}
	if slices.Contains(manifestFiles(res.Manifest), AudioFile(testVoice.Key(), wordOf(n))) {
		t.Error("a clip that was removed must leave the manifest")
	}
}

func TestGenerateAudioCountsTrimmedNewClips(t *testing.T) {
	n := clipNote("كِتَاب", 1)
	st := newStudio()
	st.say(wordOf(n), "trim")
	res := run(t, st, []notes.Note{n}, nil, nil, options(t.TempDir()))
	if res.Trimmed != 1 || res.Synthesized != 2 {
		t.Errorf("result = %+v", res)
	}
}

func TestGenerateAudioDropsFlagsForNotesThatChanged(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	st := newStudio()
	st.say(sentenceOf(b), "silent")
	first := run(t, st, []notes.Note{a, b}, nil, nil, options(dir))
	if len(first.Checks) != 1 {
		t.Fatal("the clip should be flagged")
	}

	b.Example = "هٰذَا <b>قَلَمٌ</b>."
	st.say(sentenceOf(b))
	second := run(t, st, []notes.Note{a, b}, first.Manifest, first.Checks, options(dir))
	if len(second.Checks) != 0 || len(second.Failed) != 0 {
		t.Errorf("a flag about text that no longer exists is gone: %+v", second.Checks)
	}

	gone := run(t, newStudio(), []notes.Note{a}, second.Manifest, append(first.Checks, notes.Check{ID: "nobody"}), options(dir))
	if len(gone.Checks) != 0 {
		t.Errorf("flags of notes that are gone are dropped: %+v", gone.Checks)
	}
}

func TestGenerateAudioKeepsEarlierWorkWhenSynthesisFails(t *testing.T) {
	dir := t.TempDir()
	a, b, c := clipNote("كِتَاب", 1), clipNote("قَلَم", 2), clipNote("بَاب", 3)
	quota := errors.New("rpc error: code = ResourceExhausted")
	st := newStudio()
	st.failing[wordOf(b)] = quota
	earlier := []notes.Check{{ID: c.ID, Version: AudioCheckVersion, Digest: c.Digest(), Issues: []notes.Issue{SilentIssue("WordAudio", 4)}}}
	res, err := GenerateAudio(context.Background(), []notes.Note{a, b, c}, nil, earlier, st.speak, st.inspect, options(dir))
	if !errors.Is(err, quota) || res == nil {
		t.Fatalf("err = %v, res = %v", err, res)
	}
	if res.Synthesized == 0 || len(res.Manifest) != res.Synthesized {
		t.Errorf("finished clips should stay in the manifest: %+v", res)
	}
	if !checksEqual(res.Checks, earlier) {
		t.Errorf("a flag for a clip the run never reached must survive: %+v", res.Checks)
	}
	if got := filesIn(t, dir); len(got) != res.Synthesized {
		t.Errorf("the partial file of the failed clip must not stay: %v", got)
	}
}

func TestGenerateAudioReplacesAClipOnDiskThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	os.WriteFile(clipPath(dir, wordOf(a)), []byte("broken"), 0o644)
	st := newStudio()
	res, err := GenerateAudio(context.Background(), []notes.Note{a, b}, nil, nil, st.speak, st.inspect, options(dir))
	if err != nil {
		t.Fatalf("one broken clip must not stop the run: %v", err)
	}
	if len(res.Failed) != 0 || res.Synthesized != 4 || len(res.Manifest) != 4 {
		t.Errorf("a clip that cannot be read is made again: %+v", res)
	}
	if raw, _ := os.ReadFile(clipPath(dir, wordOf(a))); string(raw) != "sound" {
		t.Errorf("the broken clip should have been replaced, found %q", raw)
	}
}

func TestGenerateAudioReportsClipsThatStayUnreadableAndGoesOn(t *testing.T) {
	dir := t.TempDir()
	a, b, c := clipNote("كِتَاب", 1), clipNote("قَلَم", 2), clipNote("بَاب", 3)
	os.WriteFile(clipPath(dir, wordOf(a)), []byte("broken"), 0o644)
	st := newStudio()
	st.say(wordOf(a), "broken")
	st.say(sentenceOf(b), "broken")
	res, err := GenerateAudio(context.Background(), []notes.Note{a, b, c}, nil, nil, st.speak, st.inspect, options(dir))
	if err != nil {
		t.Fatalf("unreadable clips are reported, they do not end the run: %v", err)
	}
	if len(res.Failed) != 2 || res.Silent() != 0 || res.Unreadable() != 2 {
		t.Fatalf("failed = %+v", res.Failed)
	}
	first, second := res.Failed[0], res.Failed[1]
	if first.ID != a.ID || first.Field != "WordAudio" || !first.Unreadable || second.ID != b.ID || second.Field != "ExampleAudio" {
		t.Errorf("failures = %+v", res.Failed)
	}
	for _, f := range res.Failed {
		if !errors.Is(f.Err, ErrUnreadable) || !strings.Contains(f.Err.Error(), dir) {
			t.Errorf("the error should name the clip with its full path: %v", f.Err)
		}
	}
	if res.Synthesized != 4 || len(res.Manifest) != 4 {
		t.Errorf("the other clips are still made: %+v", res)
	}
	if len(res.Checks) != 0 {
		t.Errorf("an unreadable clip is no flag for review, it is made again next time: %+v", res.Checks)
	}
	if got := filesIn(t, dir); len(got) != 4 || exists(clipPath(dir, wordOf(a))) {
		t.Errorf("a clip that cannot be read must not stay on disk: %v", got)
	}
	for _, e := range res.Manifest {
		if e.Text == wordOf(a) || e.Text == sentenceOf(b) {
			t.Errorf("manifest still lists %+v", e)
		}
	}
}

func TestGenerateAudioStillStopsWhenTheContextIsCancelledWhileInspecting(t *testing.T) {
	dir := t.TempDir()
	n := clipNote("كِتَاب", 1)
	os.WriteFile(clipPath(dir, wordOf(n)), []byte("sound"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	inspect := func(ctx context.Context, path string) (sound.Result, error) {
		cancel()
		return sound.Result{}, ctx.Err()
	}
	if _, err := GenerateAudio(ctx, []notes.Note{n}, nil, nil, newStudio().speak, inspect, options(dir)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if !exists(clipPath(dir, wordOf(n))) {
		t.Error("a cancelled run must not delete clips")
	}
}

func TestGenerateAudioDoesNotKeepAManifestEntryForAClipItRemoved(t *testing.T) {
	dir := t.TempDir()
	n := clipNote("كِتَاب", 1)
	os.WriteFile(clipPath(dir, wordOf(n)), []byte("silent"), 0o644)
	other := ManifestEntry{Text: "elsewhere", Voice: "v", Rate: 0.9, File: "ar-elsewhere.mp3", Inspected: true}
	old := []ManifestEntry{{Text: wordOf(n), Voice: "v", Rate: 0.9, File: AudioFile(testVoice.Key(), wordOf(n))}, other}
	quota := errors.New("rpc error: code = ResourceExhausted")
	st := newStudio()
	st.failing[wordOf(n)] = quota
	res, err := GenerateAudio(context.Background(), []notes.Note{n}, old, nil, st.speak, st.inspect, options(dir))
	if !errors.Is(err, quota) || res == nil {
		t.Fatalf("err = %v", err)
	}
	if exists(clipPath(dir, wordOf(n))) {
		t.Fatal("the silent clip should have been removed before it was made again")
	}
	if got := manifestFiles(res.Manifest); !slices.Equal(got, []string{other.File}) {
		t.Errorf("an aborted run must not list a clip whose file is gone: %v", got)
	}
}

func TestGenerateAudioHandlesMediaDirectoriesWithPatternCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my [media] *clips*?")
	os.MkdirAll(dir, 0o755)
	old := time.Now().Add(-2 * LeftoverAge)
	for _, name := range []string{".make-ar-1.mp3", ".trim-ar-2.mp3"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("x"), 0o644)
		os.Chtimes(path, old, old)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	run(t, newStudio(), []notes.Note{clipNote("كِتَاب", 1)}, nil, nil, options(dir))
	got := filesIn(t, dir)
	if slices.Contains(got, ".make-ar-1.mp3") || slices.Contains(got, ".trim-ar-2.mp3") || !slices.Contains(got, "notes.txt") || len(got) != 3 {
		t.Errorf("files = %v", got)
	}
}

func TestGenerateAudioRemovesOldLeftoversButNotThoseOfARunInProgress(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * LeftoverAge)
	for _, name := range []string{".make-ar-1.mp3", ".trim-ar-2.mp3", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	for _, name := range []string{".make-ar-1.mp3", ".trim-ar-2.mp3"} {
		os.Chtimes(filepath.Join(dir, name), old, old)
	}
	os.WriteFile(filepath.Join(dir, ".make-ar-fresh.mp3"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, ".make-dir"), 0o755)
	run(t, newStudio(), []notes.Note{clipNote("كِتَاب", 1)}, nil, nil, options(dir))
	got := filesIn(t, dir)
	if slices.Contains(got, ".make-ar-1.mp3") || slices.Contains(got, ".trim-ar-2.mp3") {
		t.Errorf("leftovers of an interrupted run should be removed: %v", got)
	}
	if !slices.Contains(got, ".make-ar-fresh.mp3") || !slices.Contains(got, "notes.txt") || !slices.Contains(got, ".make-dir") {
		t.Errorf("a temporary file that is still being written, and everything else, stays: %v", got)
	}
}

func TestGenerateAudioSkipsNotesThatAreNotWrittenYet(t *testing.T) {
	draft := notes.Note{ID: "x", Position: 1, Arabic: "كِتَاب", Pos: "noun"}
	st := newStudio()
	res := run(t, st, []notes.Note{draft}, nil, nil, options(t.TempDir()))
	if res.Synthesized != 0 || len(st.calls) != 0 || len(res.Manifest) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestGenerateAudioReportsProgressOncePerClipItHadToHandle(t *testing.T) {
	dir := t.TempDir()
	a, b := clipNote("كِتَاب", 1), clipNote("قَلَم", 2)
	st := newStudio()
	st.say(sentenceOf(b), "silent")
	var seen [][2]int
	opts := options(dir)
	opts.Progress = func(done, total int) { seen = append(seen, [2]int{done, total}) }
	res := run(t, st, []notes.Note{a, b}, nil, nil, opts)
	if want := [][2]int{{1, 4}, {2, 4}, {3, 4}, {4, 4}}; !slices.Equal(seen, want) {
		t.Errorf("progress = %v, want %v", seen, want)
	}
	seen = nil
	run(t, st, []notes.Note{a}, res.Manifest, res.Checks, opts)
	if len(seen) != 0 {
		t.Errorf("clips that need nothing are not reported: %v", seen)
	}
}

func TestGenerateAudioGivesTheSameResultAtAnyConcurrency(t *testing.T) {
	var ns []notes.Note
	for i := 1; i <= 30; i++ {
		n := clipNote(strings.Repeat("ب", i)+"َ", i)
		n.Example = "قَرَأْتُ <b>" + strings.Repeat("ك", i) + "ًا</b>."
		ns = append(ns, n)
	}
	script := func() *studio {
		st := newStudio()
		for i, n := range ns {
			switch {
			case i%3 == 0:
				st.say(sentenceOf(n), "silent")
			case i%5 == 0:
				st.say(wordOf(n), "silent", "silent", "sound")
			case i%7 == 0:
				st.say(sentenceOf(n), "trim")
			}
		}
		return st
	}
	var results []*AudioResult
	for _, concurrency := range []int{1, 3, 16} {
		opts := options(t.TempDir())
		opts.Concurrency = concurrency
		res := run(t, script(), ns, nil, nil, opts)
		slices.SortFunc(res.Manifest, func(a, b ManifestEntry) int { return strings.Compare(a.File, b.File) })
		results = append(results, res)
	}
	for _, res := range results[1:] {
		if !checksEqual(res.Checks, results[0].Checks) || len(res.Failed) != len(results[0].Failed) || res.Synthesized != results[0].Synthesized || res.Trimmed != results[0].Trimmed {
			t.Fatalf("concurrency changed the outcome:\n%+v\n%+v", res, results[0])
		}
		if !slices.Equal(manifestFiles(res.Manifest), manifestFiles(results[0].Manifest)) {
			t.Fatal("concurrency changed the manifest")
		}
		for i := range res.Failed {
			if res.Failed[i] != results[0].Failed[i] && (res.Failed[i].ID != results[0].Failed[i].ID || res.Failed[i].Field != results[0].Failed[i].Field) {
				t.Fatalf("failure %d differs: %+v vs %+v", i, res.Failed[i], results[0].Failed[i])
			}
		}
	}
	if got := len(results[0].Failed); got != 10 {
		t.Errorf("every third sentence stays silent, got %d failures", got)
	}
	if results[0].Synthesized != 2*30-10 || results[0].Trimmed == 0 {
		t.Errorf("result = %+v", results[0])
	}
}

func TestCompareVoicesWritesPlayerPage(t *testing.T) {
	dir := t.TempDir()
	var calls int
	page, err := CompareVoices(context.Background(), []Voice{{Name: "a"}, {Name: "b"}}, dir, func(_ context.Context, v Voice, text, path string) error {
		calls++
		return os.WriteFile(path, []byte(text), 0o644)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2*len(VoiceSamples) {
		t.Errorf("calls = %d", calls)
	}
	raw, _ := os.ReadFile(page)
	if !strings.Contains(string(raw), `src="b/01.mp3"`) || !strings.Contains(string(raw), "عَلَّمَ") {
		t.Error("page should link every clip and show the samples")
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "14.mp3")); err != nil {
		t.Error(err)
	}
}
