package deck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

func TestMakeClipKeepsTheFirstClipThatHasSound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	st := newStudio()
	out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, DefaultClipAttempts)
	if err != nil || out.Silent || out.Attempts != 1 || out.Trimmed {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "sound" {
		t.Errorf("clip = %q", raw)
	}
	if got := filesIn(t, dir); !slices.Equal(got, []string{"ar-1.mp3"}) {
		t.Errorf("files = %v", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("a clip is readable by everyone like the notes, got %v", st.Mode().Perm())
	}
}

func TestMakeClipTriesAgainWhileTheClipIsSilent(t *testing.T) {
	for silent := 0; silent <= 3; silent++ {
		dir := t.TempDir()
		path := filepath.Join(dir, "ar-1.mp3")
		st := newStudio()
		answers := slices.Repeat([]string{"silent"}, silent)
		st.say("كِتَاب", append(answers, "sound")...)
		out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, DefaultClipAttempts)
		if err != nil || out.Silent || out.Attempts != silent+1 || st.callsFor("كِتَاب") != silent+1 {
			t.Errorf("%d silent answers: outcome %+v, %d calls, err %v", silent, out, st.callsFor("كِتَاب"), err)
		}
		if !exists(path) {
			t.Errorf("%d silent answers: no clip was kept", silent)
		}
	}
}

func TestMakeClipGivesUpAfterTheAttemptsAreUsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	st := newStudio()
	st.say("كِتَاب", "silent")
	out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, DefaultClipAttempts)
	if err != nil || !out.Silent || out.Attempts != 4 || st.callsFor("كِتَاب") != 4 {
		t.Fatalf("outcome %+v, %d calls, err %v", out, st.callsFor("كِتَاب"), err)
	}
	if got := filesIn(t, dir); len(got) != 0 {
		t.Errorf("a silent clip must leave nothing behind: %v", got)
	}
}

func TestMakeClipNeverTriesFewerThanOnce(t *testing.T) {
	for _, attempts := range []int{-5, 0, 1} {
		st := newStudio()
		st.say("كِتَاب", "silent")
		out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", filepath.Join(t.TempDir(), "a.mp3"), attempts)
		if err != nil || !out.Silent || out.Attempts != 1 || st.callsFor("كِتَاب") != 1 {
			t.Errorf("%d attempts: %+v, %d calls, err %v", attempts, out, st.callsFor("كِتَاب"), err)
		}
	}
}

func TestMakeClipReportsATrim(t *testing.T) {
	st := newStudio()
	st.say("كِتَاب", "trim")
	out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", filepath.Join(t.TempDir(), "a.mp3"), 4)
	if err != nil || out.Silent || !out.Trimmed {
		t.Errorf("outcome %+v, err %v", out, err)
	}
}

func TestMakeClipLeavesAnExistingClipAloneWhenEveryAttemptIsSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	os.WriteFile(path, []byte("old clip"), 0o644)
	st := newStudio()
	st.say("كِتَاب", "silent")
	out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, 3)
	if err != nil || !out.Silent {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "old clip" {
		t.Errorf("a failed remake destroyed the old clip: %q", raw)
	}

	st.say("كِتَاب", "sound")
	if out, err = MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, 3); err != nil || out.Silent {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "sound" {
		t.Errorf("a good remake should replace the old clip: %q", raw)
	}
	if got := filesIn(t, dir); !slices.Equal(got, []string{"ar-1.mp3"}) {
		t.Errorf("files = %v", got)
	}
}

func TestMakeClipDoesNotRetryErrors(t *testing.T) {
	quota := errors.New("rpc error: code = ResourceExhausted")
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	st := newStudio()
	st.failing["كِتَاب"] = quota
	out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, 4)
	if !errors.Is(err, quota) || !strings.Contains(err.Error(), `synthesizing "كِتَاب"`) || out.Silent || out.Attempts != 1 || st.callsFor("كِتَاب") != 1 {
		t.Errorf("a synthesis error: %+v, %d calls, err %v", out, st.callsFor("كِتَاب"), err)
	}
	if got := filesIn(t, dir); len(got) != 0 {
		t.Errorf("the partial file of the failed attempt must be removed: %v", got)
	}

	st = newStudio()
	st.say("كِتَاب", "broken")
	out, err = MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, 4)
	if err == nil || !strings.Contains(err.Error(), "inspecting the clip") || out.Silent || st.callsFor("كِتَاب") != 1 {
		t.Errorf("an inspection error: %+v, %d calls, err %v", out, st.callsFor("كِتَاب"), err)
	}
	if got := filesIn(t, dir); len(got) != 0 || exists(path) {
		t.Errorf("a clip that cannot be read must not be kept: %v", got)
	}
}

func TestMakeClipStopsWhenTheContextIsCancelled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	st := newStudio()
	st.say("كِتَاب", "silent")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := MakeClip(ctx, st.speak, st.inspect, "كِتَاب", path, 4)
	if !errors.Is(err, context.Canceled) || out.Attempts != 0 || st.callsFor("كِتَاب") != 0 {
		t.Errorf("already cancelled: %+v, %d calls, err %v", out, st.callsFor("كِتَاب"), err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	speak := func(ctx context.Context, text, path string) error {
		if err := st.speak(ctx, text, path); err != nil {
			return err
		}
		if st.callsFor(text) == 2 {
			cancel()
		}
		return nil
	}
	out, err = MakeClip(ctx, speak, st.inspect, "كِتَاب", path, 4)
	if !errors.Is(err, context.Canceled) || out.Silent || out.Attempts != 2 {
		t.Errorf("cancelled between attempts: %+v, err %v", out, err)
	}
	if got := filesIn(t, dir); len(got) != 0 {
		t.Errorf("files = %v", got)
	}
}

func TestSilentIssueAndError(t *testing.T) {
	is := SilentIssue("ExampleAudio", 4)
	if is.Field != "ExampleAudio" || is.Kind != KindSilent || is.Severity != notes.Major || is.Word != "" {
		t.Errorf("issue = %+v", is)
	}
	if want := "The sentence audio has no sound: it came back silent in all 4 attempts."; is.Detail != want {
		t.Errorf("detail = %q, want %q", is.Detail, want)
	}
	if got := SilentIssue("WordAudio", 1).Detail; got != "The word audio has no sound: it came back silent on its only attempt." {
		t.Errorf("detail = %q", got)
	}
	if IssueKey(is) != "ExampleAudio:silent" {
		t.Errorf("key = %q", IssueKey(is))
	}
	if err := SilentError(4); !errors.Is(err, ErrSilent) || err.Error() != "the clip has no sound after 4 attempts" {
		t.Errorf("err = %v", err)
	}
	if SilentError(1).Error() != "the clip has no sound after 1 attempt" {
		t.Errorf("err = %v", SilentError(1))
	}
}

func TestClipLabel(t *testing.T) {
	for field, want := range map[string]string{"WordAudio": "word", "FormsAudio": "forms", "ExampleAudio": "sentence", "other": "other"} {
		if got := ClipLabel(field); got != want {
			t.Errorf("ClipLabel(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestAudioChecksAreAddedReplacedAndClearedPerClip(t *testing.T) {
	a, b := note("كِتَاب", 1), note("قَلَم", 2)
	var checks []notes.Check
	checks = WithAudioIssue(checks, b, SilentIssue("WordAudio", 4))
	checks = WithAudioIssue(checks, a, SilentIssue("ExampleAudio", 4))
	checks = WithAudioIssue(checks, a, SilentIssue("WordAudio", 4))
	order := []string{a.ID, b.ID}
	slices.Sort(order)
	if len(checks) != 2 || checks[0].ID != order[0] || checks[1].ID != order[1] {
		t.Fatalf("checks should be sorted by note: %+v", checks)
	}
	at := slices.IndexFunc(checks, func(c notes.Check) bool { return c.ID == a.ID })
	if got := checks[at]; got.Version != AudioCheckVersion || got.Digest != a.Digest() || len(got.Issues) != 2 || got.Issues[0].Field != "ExampleAudio" || got.Issues[1].Field != "WordAudio" {
		t.Errorf("a's check = %+v", got)
	}

	again := WithAudioIssue(checks, a, SilentIssue("WordAudio", 2))
	if got := again[slices.IndexFunc(again, func(c notes.Check) bool { return c.ID == a.ID })]; len(got.Issues) != 2 || !strings.Contains(got.Issues[1].Detail, "2 attempts") {
		t.Errorf("an issue for the same clip is replaced, not repeated: %+v", got.Issues)
	}

	cleared := WithoutAudioIssue(again, a, "ExampleAudio")
	if got := cleared[slices.IndexFunc(cleared, func(c notes.Check) bool { return c.ID == a.ID })]; len(cleared) != 2 || len(got.Issues) != 1 || got.Issues[0].Field != "WordAudio" {
		t.Errorf("cleared = %+v", cleared)
	}
	cleared = WithoutAudioIssue(cleared, a, "WordAudio")
	if len(cleared) != 1 || cleared[0].ID != b.ID {
		t.Errorf("a note without issues has no record: %+v", cleared)
	}
	if same := WithoutAudioIssue(cleared, a, "WordAudio"); len(same) != 1 {
		t.Errorf("clearing what is not there changes nothing: %+v", same)
	}
	if same := WithoutAudioIssue(cleared, b, "FormsAudio"); len(same) != 1 || len(same[0].Issues) != 1 {
		t.Errorf("clearing another clip keeps the flag: %+v", same)
	}
}

func TestAudioCheckHelpersNeverChangeTheirInput(t *testing.T) {
	a := note("كِتَاب", 1)
	original := []notes.Check{{ID: a.ID, Version: AudioCheckVersion, Digest: a.Digest(), Issues: []notes.Issue{SilentIssue("WordAudio", 4), SilentIssue("ExampleAudio", 4)}}}
	snapshot := func() string { return checksJSON(original) }
	before := snapshot()
	WithAudioIssue(original, a, SilentIssue("FormsAudio", 4))
	WithoutAudioIssue(original, a, "WordAudio")
	PruneAudioChecks(original, nil)
	if snapshot() != before {
		t.Errorf("the input changed:\n%s\n%s", before, snapshot())
	}
}

func checksJSON(checks []notes.Check) string {
	var b strings.Builder
	for _, c := range checks {
		b.WriteString(c.ID + ":")
		for _, is := range c.Issues {
			b.WriteString(is.Field + ",")
		}
		b.WriteString(";")
	}
	return b.String()
}

func TestAudioChecksForAnOldVersionOfTheNoteAreStartedOver(t *testing.T) {
	a := note("كِتَاب", 1)
	stale := []notes.Check{{ID: a.ID, Version: AudioCheckVersion, Digest: "old text", Issues: []notes.Issue{SilentIssue("WordAudio", 4)}}}
	got := WithAudioIssue(stale, a, SilentIssue("ExampleAudio", 4))
	if len(got) != 1 || got[0].Digest != a.Digest() || len(got[0].Issues) != 1 || got[0].Issues[0].Field != "ExampleAudio" {
		t.Errorf("an issue about text that changed must not carry over: %+v", got)
	}
	if got := WithoutAudioIssue(stale, a, "ExampleAudio"); len(got) != 0 {
		t.Errorf("a stale record is dropped when touched: %+v", got)
	}
}

func TestPruneAudioChecksKeepsOnlyCurrentIssuesOfExistingNotes(t *testing.T) {
	a, b, c := note("كِتَاب", 1), note("قَلَم", 2), note("بَاب", 3)
	issue := []notes.Issue{SilentIssue("WordAudio", 4)}
	checks := []notes.Check{
		{ID: c.ID, Version: AudioCheckVersion, Digest: c.Digest(), Issues: issue},
		{ID: a.ID, Version: AudioCheckVersion, Digest: a.Digest(), Issues: issue},
		{ID: b.ID, Version: AudioCheckVersion, Digest: "changed", Issues: issue},
		{ID: "gone", Version: AudioCheckVersion, Digest: "x", Issues: issue},
		{ID: c.ID + "2", Version: AudioCheckVersion + 1, Digest: c.Digest(), Issues: issue},
		{ID: a.ID + "2", Version: AudioCheckVersion, Digest: a.Digest()},
	}
	got := PruneAudioChecks(checks, []notes.Note{a, b, c})
	order := []string{a.ID, c.ID}
	slices.Sort(order)
	if len(got) != 2 || got[0].ID != order[0] || got[1].ID != order[1] {
		t.Errorf("pruned = %+v", got)
	}
	if got := PruneAudioChecks(nil, []notes.Note{a}); got == nil || len(got) != 0 {
		t.Errorf("nothing to prune should still give an empty list, got %#v", got)
	}
}

func TestCurrentAudioNeedsTheSameVersionAndText(t *testing.T) {
	a := note("كِتَاب", 1)
	c := notes.Check{ID: a.ID, Version: AudioCheckVersion, Digest: a.Digest()}
	if !CurrentAudio(a, c) {
		t.Fatal("a fresh check should count")
	}
	c.Version++
	if CurrentAudio(a, c) {
		t.Error("another version should not count")
	}
	c.Version = AudioCheckVersion
	a.Example = "هٰذَا <b>كِتَابٌ</b>."
	if CurrentAudio(a, c) {
		t.Error("a note that changed should not count")
	}
}

func TestSilentFlagsCannotBeReviewedAway(t *testing.T) {
	a := note("كِتَاب", 1)
	c := notes.Check{ID: a.ID, Version: AudioCheckVersion, Digest: a.Digest(), Issues: []notes.Issue{SilentIssue("ExampleAudio", 4), SilentIssue("WordAudio", 4)}}
	if got := OpenAudioIssues(a, c); len(got) != 2 {
		t.Fatalf("open = %+v", got)
	}
	a.Reviewed = []string{"ExampleAudio:silent", "WordAudio:silent"}
	got := OpenAudioIssues(a, c)
	if len(got) != 2 || got[0].Field != "ExampleAudio" || got[1].Field != "WordAudio" {
		t.Errorf("a clip without sound stays flagged until it has sound, whatever the note says: %+v", got)
	}
	got[0].Field = "changed"
	if c.Issues[0].Field != "ExampleAudio" {
		t.Error("the check must not be changed through the result")
	}
}

func TestOpenAudioIssuesIgnoresChecksOfOtherVersionsOfTheNote(t *testing.T) {
	a := note("كِتَاب", 1)
	c := notes.Check{ID: a.ID, Version: AudioCheckVersion, Digest: a.Digest(), Issues: []notes.Issue{SilentIssue("WordAudio", 4)}}
	a.Example = "هٰذَا <b>كِتَابٌ</b>."
	if got := OpenAudioIssues(a, c); len(got) != 0 {
		t.Errorf("a flag about text that no longer exists is not open: %+v", got)
	}
	c.Digest = a.Digest()
	c.Version++
	if got := OpenAudioIssues(a, c); len(got) != 0 {
		t.Errorf("another version of the check: %+v", got)
	}
}

func TestMakeClipCreatesTheMediaDirectoryItNeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "media", "ar-1.mp3")
	st := newStudio()
	out, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, 4)
	if err != nil || out.Silent {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "sound" {
		t.Errorf("clip = %q", raw)
	}

	blocked := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocked, []byte("x"), 0o644)
	if _, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", filepath.Join(blocked, "ar-1.mp3"), 4); err == nil {
		t.Error("a media directory that cannot be created is an error")
	}
}

func TestMakeClipNamesTheClipThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	st := newStudio()
	st.say("كِتَاب", "broken")
	_, err := MakeClip(context.Background(), st.speak, st.inspect, "كِتَاب", path, 4)
	if !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "not an audio file") || strings.Contains(err.Error(), ".make-") {
		t.Errorf("err = %v", err)
	}
}

func TestMakeClipHidesItsTemporaryFileNameInErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ar-1.mp3")
	st := newStudio()
	inspect := func(_ context.Context, tmp string) (sound.Result, error) {
		return sound.Result{}, fmt.Errorf("decoding %s: ffmpeg said: Error opening input file %s.", filepath.Base(tmp), tmp)
	}
	_, err := MakeClip(context.Background(), st.speak, inspect, "كِتَاب", path, 4)
	if !errors.Is(err, ErrUnreadable) || strings.Contains(err.Error(), ".make-") {
		t.Fatalf("the error should talk about the clip, not about a throwaway file: %v", err)
	}
	if !strings.Contains(err.Error(), "decoding ar-1.mp3: ffmpeg said: Error opening input file "+path+".") {
		t.Errorf("err = %v", err)
	}
}

func TestClipOwnersListsEveryNoteAndFieldThatSpeaksATextOnce(t *testing.T) {
	a, b, c := clipNote("كِتَاب", 1), clipNote("قَلَم", 2), clipNote("بَاب", 3)
	b.Example = a.Example
	unwritten := notes.Note{ID: "x", Position: 4, Arabic: "كِتَاب"}
	ns := []notes.Note{a, b, c, unwritten}
	got := ClipOwners(ns, sentenceOf(a))
	if len(got) != 2 || got[0] != (ClipOwner{Index: 0, Field: "ExampleAudio"}) || got[1] != (ClipOwner{Index: 1, Field: "ExampleAudio"}) {
		t.Errorf("owners of the shared sentence: %+v", got)
	}
	if got := ClipOwners(ns, wordOf(c)); len(got) != 1 || got[0].Index != 2 || got[0].Field != "WordAudio" {
		t.Errorf("owners of one word: %+v", got)
	}
	d := clipNote("شَجَرَة", 5)
	d.Example = "<b>" + wordOf(a) + "</b>"
	if got := ClipOwners([]notes.Note{a, d}, wordOf(a)); len(got) < 1 || got[0].Field != "WordAudio" {
		t.Errorf("owners = %+v", got)
	}
	if got := ClipOwners(ns, "nobody says this"); len(got) != 0 {
		t.Errorf("owners = %+v", got)
	}
}
