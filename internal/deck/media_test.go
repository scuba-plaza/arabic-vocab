package deck

import (
	"context"
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
	fake := &fakeVoice{heard: map[string]string{"هٰذَا قَلَمٌ.": "هذا علم"}}
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
}

func TestTranscriptMatchesIgnoresVowelsAndPunctuation(t *testing.T) {
	if !TranscriptMatches("أَنَا مِنْ هُنَا.", "انا من هنا") {
		t.Error("an unvowelled transcript of the same sentence should match")
	}
	if TranscriptMatches("أَنَا مِنْ هُنَا.", "انا من هناك") {
		t.Error("a different word should not match")
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
