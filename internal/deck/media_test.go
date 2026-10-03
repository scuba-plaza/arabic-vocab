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
}

func (f *fakeVoice) speak(_ context.Context, text, path string) error {
	f.mu.Lock()
	f.spoken = append(f.spoken, text)
	f.mu.Unlock()
	return os.WriteFile(path, []byte(text), 0o644)
}

func TestGenerateAudioSynthesizesEachClipOnce(t *testing.T) {
	dir := t.TempDir()
	first := note("كِتَاب", 1)
	first.Forms = []notes.Form{{Label: "pl.", Arabic: "كُتُب"}}
	second := note("قَلَم", 2)
	second.Example = "هٰذَا <b>قَلَمٌ</b>."
	fake := &fakeVoice{}
	opts := AudioOptions{Voice: Voice{Name: "v", Rate: 0.9}, MediaDir: dir}

	res, err := GenerateAudio(context.Background(), []notes.Note{first, second}, nil, fake.speak, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synthesized != 5 || len(res.Manifest) != 5 || res.Reused != 0 {
		t.Fatalf("synthesized %d, manifest %d, reused %d", res.Synthesized, len(res.Manifest), res.Reused)
	}
	if !slices.Contains(fake.spoken, "هٰذَا قَلَمْ.") {
		t.Errorf("the example should be spoken with a pausal ending: %q", fake.spoken)
	}

	fake.spoken = nil
	again, err := GenerateAudio(context.Background(), []notes.Note{first, second}, res.Manifest, fake.speak, opts)
	if err != nil {
		t.Fatal(err)
	}
	if again.Synthesized != 0 || again.Reused != 5 || len(fake.spoken) != 0 {
		t.Errorf("second run should reuse every clip: %+v", again)
	}

	pkg, summary, err := BuildPackage([]notes.Note{first, second}, nil, BuildOptions{Audio: AudioIndex(again.Manifest), MediaDir: dir})
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

func TestGenerateAudioPicksUpClipsAlreadyOnDisk(t *testing.T) {
	dir := t.TempDir()
	n := note("كِتَاب", 1)
	opts := AudioOptions{Voice: Voice{Name: "v", Rate: 0.9}, MediaDir: dir}
	fake := &fakeVoice{}
	if _, err := GenerateAudio(context.Background(), []notes.Note{n}, nil, fake.speak, opts); err != nil {
		t.Fatal(err)
	}
	fake.spoken = nil
	res, err := GenerateAudio(context.Background(), []notes.Note{n}, nil, fake.speak, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Synthesized != 0 || res.Reused != 2 || len(res.Manifest) != 2 || len(fake.spoken) != 0 {
		t.Errorf("clips on disk without a manifest entry should be adopted: %+v", res)
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

func TestGenerateAudioKeepsFinishedClipsWhenSynthesisFails(t *testing.T) {
	dir := t.TempDir()
	a, b := note("كِتَاب", 1), note("قَلَم", 2)
	quota := errors.New("rpc error: code = ResourceExhausted")
	speak := func(_ context.Context, text, path string) error {
		if strings.Contains(text, "قَلَم") {
			return quota
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
	opts := AudioOptions{Voice: Voice{Name: "v", Rate: 0.9}, MediaDir: dir, Concurrency: 1}
	res, err := GenerateAudio(context.Background(), []notes.Note{a, b}, nil, speak, opts)
	if !errors.Is(err, quota) {
		t.Fatalf("err = %v", err)
	}
	if res.Synthesized == 0 || len(res.Manifest) != res.Synthesized {
		t.Errorf("finished clips should stay in the manifest: %+v", res)
	}
}
