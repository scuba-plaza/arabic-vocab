package sound

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if err := Available(); err != nil {
		t.Skip(err)
	}
}

func makeMP3(t *testing.T, dir, name string, lead, speech, tail time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
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
		t.Fatalf("making %s: %v\n%s", name, err, out)
	}
	return path
}

func length(t *testing.T, path string) time.Duration {
	t.Helper()
	pcm, err := decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return duration(len(pcm), analysisRate)
}

func level(t *testing.T, path string) float64 {
	t.Helper()
	pcm, err := decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	var n int
	for _, s := range pcm {
		if s > 200 || s < -200 {
			sum += float64(s) * float64(s)
			n++
		}
	}
	if n == 0 {
		return -200
	}
	return sum / float64(n)
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestTrimCutsSilenceFromBothEndsWithoutTouchingTheSound(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	path := makeMP3(t, dir, "clip.mp3", 2*time.Second, time.Second, 2*time.Second)
	before := level(t, path)
	size := mustSize(t, path)

	res, err := Trim(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Silent || !res.Trimmed() {
		t.Fatalf("result = %+v", res)
	}
	within(t, "reported head", res.Head(), 1900*time.Millisecond, ms(60))
	within(t, "reported tail", res.Tail(), 1900*time.Millisecond, ms(60))
	within(t, "length after the cut", length(t, path), time.Second+2*Default.Pad, ms(60))
	if after := level(t, path); after < before*0.98 || after > before*1.02 {
		t.Errorf("the sound changed: mean power %.0f before, %.0f after", before, after)
	}
	if mustSize(t, path) >= size/2 {
		t.Errorf("the file only shrank from %d to %d bytes", size, mustSize(t, path))
	}
	if got := leftovers(t, dir); len(got) != 1 || got[0] != "clip.mp3" {
		t.Errorf("files left in the directory: %v", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("the trimmed clip should stay readable by everyone, got %v", st.Mode().Perm())
	}
}

func TestTrimTreatsAnyKindOfEmptyClipAsSilent(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	silent := makeMP3(t, dir, "silent.mp3", 3*time.Second, 0, 0)
	empty := filepath.Join(dir, "empty.mp3")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{silent, empty} {
		before, _ := os.ReadFile(path)
		res, err := Trim(context.Background(), path)
		if err != nil || !res.Silent || res.Trimmed() {
			t.Errorf("%s: %+v, %v", filepath.Base(path), res, err)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Errorf("%s: a silent clip must not be rewritten", filepath.Base(path))
		}
	}
}

func TestTrimLeavesClipsWithoutSurplusSilenceAlone(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	for name, parts := range map[string][3]time.Duration{
		"bare.mp3":     {0, time.Second, 0},
		"natural.mp3":  {ms(100), time.Second, ms(100)},
		"shorter.mp3":  {ms(60), ms(600), ms(60)},
		"sentence.mp3": {ms(120), 3 * time.Second, ms(120)},
	} {
		path := makeMP3(t, dir, name, parts[0], parts[1], parts[2])
		before, _ := os.ReadFile(path)
		stat, _ := os.Stat(path)
		res, err := Trim(context.Background(), path)
		if err != nil || res.Silent || res.Trimmed() {
			t.Errorf("%s: %+v, %v", name, res, err)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Errorf("%s: an untrimmed clip must stay byte for byte the same", name)
		}
		if now, _ := os.Stat(path); !now.ModTime().Equal(stat.ModTime()) {
			t.Errorf("%s: the file was rewritten", name)
		}
	}
}

func TestTrimTrimsOnlyTheSideThatNeedsIt(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	lead := makeMP3(t, dir, "lead.mp3", 3*time.Second, time.Second, ms(100))
	res, err := Trim(context.Background(), lead)
	if err != nil || !res.Trimmed() || res.Tail() != 0 {
		t.Fatalf("lead: %+v, %v", res, err)
	}
	within(t, "lead: length", length(t, lead), time.Second+2*Default.Pad, ms(60))

	tail := makeMP3(t, dir, "tail.mp3", ms(100), time.Second, 3*time.Second)
	if res, err = Trim(context.Background(), tail); err != nil || !res.Trimmed() || res.Head() != 0 {
		t.Fatalf("tail: %+v, %v", res, err)
	}
	within(t, "tail: length", length(t, tail), time.Second+2*Default.Pad, ms(60))
}

func TestTrimSettlesAfterOnePass(t *testing.T) {
	needFFmpeg(t)
	path := makeMP3(t, t.TempDir(), "clip.mp3", 2500*time.Millisecond, 1500*time.Millisecond, 1700*time.Millisecond)
	if res, err := Trim(context.Background(), path); err != nil || !res.Trimmed() {
		t.Fatalf("first pass: %+v, %v", res, err)
	}
	first, _ := os.ReadFile(path)
	res, err := Trim(context.Background(), path)
	if err != nil || res.Trimmed() || res.Silent {
		t.Fatalf("second pass: %+v, %v", res, err)
	}
	if second, _ := os.ReadFile(path); !bytes.Equal(first, second) {
		t.Error("a second pass changed the file")
	}
}

func TestTrimKeepsTheBitrateAndPlainMP3Framing(t *testing.T) {
	needFFmpeg(t)
	path := makeMP3(t, t.TempDir(), "clip.mp3", 2*time.Second, time.Second, 2*time.Second)
	if _, err := Trim(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.HasPrefix(raw, []byte("ID3")) || bytes.Contains(raw[:min(len(raw), 400)], []byte("Xing")) || bytes.Contains(raw[:min(len(raw), 400)], []byte("Info")) {
		t.Errorf("the trimmed clip should be bare MP3 frames, starts with %q", raw[:8])
	}
	if raw[0] != 0xff || raw[1]&0xe0 != 0xe0 {
		t.Errorf("the file does not start with an MP3 frame: % x", raw[:4])
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=bit_rate,sample_rate", "-of", "default=nw=1", path).Output()
	if err != nil {
		t.Skip("ffprobe is not available")
	}
	if !strings.Contains(string(out), "bit_rate=32000") || !strings.Contains(string(out), "sample_rate=24000") {
		t.Errorf("the clip was re-encoded: %s", out)
	}
}

func TestTrimReportsProblemsWithTheFile(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	if _, err := Trim(context.Background(), filepath.Join(dir, "missing.mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
	garbage := filepath.Join(dir, "garbage.mp3")
	if err := os.WriteFile(garbage, bytes.Repeat([]byte("not audio at all "), 200), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Trim(context.Background(), garbage)
	if err == nil || !strings.Contains(err.Error(), "decoding garbage.mp3") || res != (Result{}) {
		t.Errorf("a file that is not audio: %+v, %v", res, err)
	}
	if after, _ := os.ReadFile(garbage); len(after) != 17*200 {
		t.Error("a broken file must not be touched")
	}
}

func TestTrimStopsWhenTheContextIsCancelled(t *testing.T) {
	needFFmpeg(t)
	path := makeMP3(t, t.TempDir(), "clip.mp3", 2*time.Second, time.Second, 2*time.Second)
	before, _ := os.ReadFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Trim(ctx, path); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("a cancelled trim must leave the file alone")
	}
}

func TestTrimWorksOnManyClipsAtOnce(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 8; i++ {
		paths = append(paths, makeMP3(t, dir, fmt.Sprintf("clip%d.mp3", i), time.Duration(1+i%3)*time.Second, 800*time.Millisecond, time.Duration(1+i%2)*time.Second))
	}
	var wg sync.WaitGroup
	errs := make([]error, len(paths))
	for i, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Trim(context.Background(), p)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("clip %d: %v", i, err)
		}
		within(t, fmt.Sprintf("clip %d length", i), length(t, paths[i]), 800*time.Millisecond+2*Default.Pad, ms(60))
	}
	if got := leftovers(t, dir); len(got) != len(paths) {
		t.Errorf("files left in the directory: %v", got)
	}
}

func TestAvailableNamesTheMissingTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Available(); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("err = %v", err)
	}
}

func mustSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}
