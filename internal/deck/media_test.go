package deck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type fakeVoice struct {
	mu     sync.Mutex
	spoken []string
	heard  map[string]string
}

func (f *fakeVoice) speak(_ context.Context, text, path string) error {
	f.mu.Lock()
	f.spoken = append(f.spoken, text)
	f.mu.Unlock()
	return os.WriteFile(path, []byte(text), 0o644)
}

func (f *fakeVoice) listen(_ context.Context, path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if h, ok := f.heard[string(raw)]; ok {
		return h, nil
	}
	return string(raw), nil
}

func TestGenerateAudioSynthesizesOnceAndVerifiesExamples(t *testing.T) {
	dir := t.TempDir()
	good := note("كِتَاب", 1)
	good.Forms = []notes.Form{{Label: "pl.", Arabic: "كُتُب"}}
	bad := note("قَلَم", 2)
	bad.Example = "هٰذَا <b>قَلَمٌ</b>."
	fake := &fakeVoice{heard: map[string]string{"هٰذَا قَلَمْ.": "هذا علم"}}
	opts := AudioOptions{Voice: Voice{Name: "v", Rate: 0.9}, MediaDir: dir, Verify: true}

	res, err := GenerateAudio(context.Background(), []notes.Note{good, bad}, nil, nil, fake.speak, fake.listen, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synthesized != 5 || len(res.Manifest) != 5 {
		t.Fatalf("synthesized %d, manifest %d", res.Synthesized, len(res.Manifest))
	}
	if res.Mismatches != 1 || len(res.Checks) != 2 {
		t.Fatalf("checks %+v", res.Checks)
	}
	i := slices.IndexFunc(res.Checks, func(c notes.AudioCheck) bool { return c.ID == bad.ID })
	if res.Checks[i].Match {
		t.Error("a garbled transcript should not match")
	}

	if !slices.Contains(fake.spoken, "هٰذَا قَلَمْ.") {
		t.Errorf("the example should be spoken with a pausal ending: %q", fake.spoken)
	}

	fake.spoken = nil
	again, err := GenerateAudio(context.Background(), []notes.Note{good, bad}, res.Manifest, res.Checks, fake.speak, fake.listen, opts)
	if err != nil {
		t.Fatal(err)
	}
	if again.Synthesized != 0 || again.Reused != 5 || len(fake.spoken) != 0 {
		t.Errorf("second run should reuse every clip: %+v", again)
	}
	if again.Mismatches != 1 {
		t.Errorf("cached mismatch should still count, got %d", again.Mismatches)
	}

	pkg, summary, err := BuildPackage([]notes.Note{good, bad}, nil, again.Checks, BuildOptions{Audio: AudioIndex(again.Manifest), MediaDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AudioFiles != 5 || summary.MissingAudio != 0 {
		t.Errorf("summary %+v", summary)
	}
	if !slices.Contains(pkg.Notes[1].Tags, "check::audio") || slices.Contains(pkg.Notes[0].Tags, "check::audio") {
		t.Errorf("only the garbled note should be tagged: %v / %v", pkg.Notes[0].Tags, pkg.Notes[1].Tags)
	}
	if !strings.HasPrefix(pkg.Notes[0].Fields[fieldIndex("WordAudio")], "[sound:ar-") {
		t.Errorf("word audio field = %q", pkg.Notes[0].Fields[fieldIndex("WordAudio")])
	}

	i = slices.IndexFunc(again.Checks, func(c notes.AudioCheck) bool { return c.ID == bad.ID })
	if !strings.Contains(pkg.Notes[1].Fields[fieldIndex("Check")], again.Checks[i].File) {
		t.Errorf("the check line should name the clip: %q", pkg.Notes[1].Fields[fieldIndex("Check")])
	}
	bad.ReviewedAudio = []string{again.Checks[i].File}
	pkg, _, err = BuildPackage([]notes.Note{good, bad}, nil, again.Checks, BuildOptions{Audio: AudioIndex(again.Manifest), MediaDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(pkg.Notes[1].Tags, "check::audio") {
		t.Errorf("a reviewed clip should not be tagged: %v", pkg.Notes[1].Tags)
	}
}

func TestTranscriptMatches(t *testing.T) {
	cases := []struct {
		text, transcript string
		want             bool
	}{
		{"أَنَا مِنْ هُنَا.", "انا من هنا", true},
		{"أَنَا مِنْ هُنَا.", "انا من هناك", false},
		{"اِثْنَانِ وَاثْنَانِ أَرْبَعَة.", "2 + 2 = 4", true},
		{"عُمْرِي عِشْرُونَ سَنَة.", "عمري 20 سنه", true},
		{"اِنْتَظَرْتُ خَمْسَ دَقَائِقْ.", "انتظرت 5 دقائق.", true},
		{"اِنْتَظَرْتُ خَمْسَ دَقَائِقْ.", "انتظرت ٥ دقائق", true},
		{"عِنْدِي أَخٌ وَاحِدْ.", "عندي اخ 1", true},
		{"عُمْرُهُ خَمْسٌ وَعِشْرُونَ سَنَة.", "عمره 25 سنه", true},
		{"عِنْدِي خَمْسَةَ عَشَرَ كِتَابًا.", "عندي 15 كتابا", true},
		{"عِنْدِي خَمْسَةَ عَشَرَ كِتَابًا.", "عندي 5 كتابا", false},
		{"عُمْرِي عِشْرُونَ سَنَة.", "عمري 30 سنه", false},
		{"ذَهَبْتُ إِلَى السُّوقْ.", "فذهبت الى السوق.", false},
		{"كَانَ الْجَوُّ جَمِيلًا أَمْسْ.", "كان الجو جميلا امسي.", false},
	}
	for _, tc := range cases {
		if got := TranscriptMatches(tc.text, tc.transcript); got != tc.want {
			t.Errorf("TranscriptMatches(%q, %q) = %v", tc.text, tc.transcript, got)
		}
	}
}

func TestVoiceTestWritesPlayerPage(t *testing.T) {
	dir := t.TempDir()
	var calls int
	page, err := VoiceTest(context.Background(), []Voice{{Name: "a"}, {Name: "b"}}, dir, func(_ context.Context, v Voice, text, path string) error {
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

func TestGenerateAudioKeepsEarlierChecksWhenItFails(t *testing.T) {
	dir := t.TempDir()
	a, b := note("كِتَاب", 1), note("قَلَم", 2)
	previous := []notes.AudioCheck{{ID: a.ID, Field: "ExampleAudio", Text: "old", File: "ar-old.mp3", Match: false}}
	quota := errors.New("rpc error: code = ResourceExhausted")
	speak := func(_ context.Context, text, path string) error {
		if strings.Contains(text, "قَلَم") {
			return quota
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
	opts := AudioOptions{Voice: Voice{Name: "v", Rate: 0.9}, MediaDir: dir, Verify: true, Concurrency: 1}
	res, err := GenerateAudio(context.Background(), []notes.Note{a, b}, nil, previous, speak, (&fakeVoice{}).listen, opts)
	if !errors.Is(err, quota) {
		t.Fatalf("err = %v", err)
	}
	if len(res.Checks) != 1 || res.Checks[0].File != "ar-old.mp3" {
		t.Errorf("a failed run must keep the earlier checks, got %+v", res.Checks)
	}
	if res.Synthesized == 0 || len(res.Manifest) != res.Synthesized {
		t.Errorf("finished clips should stay in the manifest: %+v", res)
	}

	listenErr := errors.New("speech quota")
	calls := 0
	listen := func(_ context.Context, path string) (string, error) {
		calls++
		if calls == 2 {
			return "", listenErr
		}
		raw, err := os.ReadFile(path)
		return string(raw), err
	}
	ok := func(_ context.Context, text, path string) error { return os.WriteFile(path, []byte(text), 0o644) }
	c := note("بَاب", 3)
	res, err = GenerateAudio(context.Background(), []notes.Note{a, b, c}, res.Manifest, previous, ok, listen, opts)
	if !errors.Is(err, listenErr) {
		t.Fatalf("err = %v", err)
	}
	ids := map[string]bool{}
	for _, ch := range res.Checks {
		ids[ch.ID+" "+ch.File] = true
	}
	if len(res.Checks) != 2 || !ids[a.ID+" ar-old.mp3"] {
		t.Errorf("checks after a failed transcription = %+v", res.Checks)
	}
}
