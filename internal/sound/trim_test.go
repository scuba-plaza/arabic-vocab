package sound

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if err := Available(); err != nil {
		if os.Getenv("REQUIRE_FFMPEG") != "" {
			t.Fatalf("REQUIRE_FFMPEG is set: %v", err)
		}
		t.Skip(err)
	}
}

type clipSpec struct {
	lead, speech, tail time.Duration
	rate               int
	bitrate            string
	xing               bool
	pauses             bool
}

func (c clipSpec) args() []string {
	rate := c.rate
	if rate == 0 {
		rate = 24000
	}
	bitrate := c.bitrate
	if bitrate == "" {
		bitrate = "32k"
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-y"}
	if c.speech == 0 {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("anullsrc=r=%d:cl=mono", rate), "-t", fmt.Sprintf("%.3f", (c.lead+c.tail).Seconds()))
	} else {
		filters := "volume=0.3"
		if c.lead > 0 {
			filters += fmt.Sprintf(",adelay=%d:all=1", c.lead.Milliseconds())
		}
		if c.tail > 0 {
			filters += fmt.Sprintf(",apad=pad_dur=%.3f", c.tail.Seconds())
		}
		source := fmt.Sprintf("sine=frequency=440:duration=%.3f:sample_rate=%d", c.speech.Seconds(), rate)
		if c.pauses {
			source = fmt.Sprintf("aevalsrc='0.5*sin(2*PI*(140+30*sin(2*PI*1.5*t))*t)*(0.55+0.45*sin(2*PI*4.5*t))*lt(mod(t,0.9),0.6)':s=%d:d=%.3f", rate, c.speech.Seconds())
		}
		args = append(args, "-f", "lavfi", "-i", source, "-af", filters)
	}
	args = append(args, "-ac", "1", "-ar", fmt.Sprint(rate), "-b:a", bitrate, "-id3v2_version", "0")
	if !c.xing {
		args = append(args, "-write_xing", "0")
	}
	return args
}

func makeClip(t *testing.T, dir, name string, spec clipSpec) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if out, err := exec.Command("ffmpeg", append(spec.args(), path)...).CombinedOutput(); err != nil {
		t.Fatalf("making %s: %v\n%s", name, err, out)
	}
	return path
}

func makeMP3(t *testing.T, dir, name string, lead, speech, tail time.Duration) string {
	t.Helper()
	return makeClip(t, dir, name, clipSpec{lead: lead, speech: speech, tail: tail})
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

func pads(t *testing.T, path string) (head, tail time.Duration) {
	t.Helper()
	pcm, err := decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	res := Analyze(pcm, analysisRate, Config{ThresholdDB: Default.ThresholdDB, Window: Default.Window, MinSound: Default.MinSound, MinTrim: time.Nanosecond})
	if res.Silent {
		t.Fatalf("%s has no sound", filepath.Base(path))
	}
	return res.Head(), res.Tail()
}

func pauses(t *testing.T, path string, atLeast time.Duration) []time.Duration {
	t.Helper()
	pcm, err := decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	window := samples(Default.Window, analysisRate)
	limit := math.Pow(10, Default.ThresholdDB/10) * math.MaxInt16 * math.MaxInt16
	var out []time.Duration
	run, heard := 0, false
	for lo := 0; lo+window <= len(pcm); lo += window {
		var sum float64
		for _, s := range pcm[lo : lo+window] {
			sum += float64(s) * float64(s)
		}
		if sum/float64(window) <= limit {
			run++
			continue
		}
		if heard && duration(run*window, analysisRate) >= atLeast {
			out = append(out, duration(run*window, analysisRate))
		}
		run, heard = 0, true
	}
	return out
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

func paddedLength(t *testing.T, name string, got, speech time.Duration) {
	t.Helper()
	lo, hi := speech+2*Default.Pad-ms(30), speech+2*(Default.Pad+frame)+ms(40)
	if got < lo || got > hi {
		t.Errorf("%s = %v, want between %v and %v", name, got, lo, hi)
	}
}

func checkPads(t *testing.T, name string, path string) {
	t.Helper()
	head, tail := pads(t, path)
	for side, got := range map[string]time.Duration{"head": head, "tail": tail} {
		if got < Default.Pad-ms(5) || got > Default.Pad+frame+ms(25) {
			t.Errorf("%s: the %s keeps %v of silence, want about %v", name, side, got, Default.Pad)
		}
	}
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
	paddedLength(t, "length after the cut", length(t, path), time.Second)
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

func TestTrimLeavesThePadItPromisesOnBothSides(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	specs := map[string]clipSpec{
		"24 kHz plain frames":        {lead: 2 * time.Second, speech: time.Second, tail: 2 * time.Second},
		"24 kHz with a Xing header":  {lead: 2 * time.Second, speech: time.Second, tail: 2 * time.Second, xing: true},
		"44.1 kHz plain frames":      {lead: 1500 * time.Millisecond, speech: 1300 * time.Millisecond, tail: 1700 * time.Millisecond, rate: 44100, bitrate: "64k"},
		"44.1 kHz with Xing":         {lead: 1500 * time.Millisecond, speech: 1300 * time.Millisecond, tail: 1700 * time.Millisecond, rate: 44100, bitrate: "64k", xing: true},
		"short speech, long silence": {lead: 3 * time.Second, speech: 400 * time.Millisecond, tail: 3 * time.Second},
		"speech with pauses":         {lead: 2 * time.Second, speech: 1500 * time.Millisecond, tail: 2 * time.Second, pauses: true},
	}
	for name, spec := range specs {
		path := makeClip(t, dir, strings.ReplaceAll(name, " ", "-")+".mp3", spec)
		if _, err := Trim(context.Background(), path); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkPads(t, name, path)
		paddedLength(t, name+": length", length(t, path), spec.speech)
	}
}

func TestTrimOfOneSideKeepsTheOtherSideAsItWas(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	for name, xing := range map[string]bool{"plain frames": false, "xing header": true} {
		lead := makeClip(t, dir, "lead-"+strings.ReplaceAll(name, " ", "-")+".mp3", clipSpec{lead: 3 * time.Second, speech: time.Second, tail: ms(100), xing: xing})
		_, tailBefore := pads(t, lead)
		whole, _ := os.ReadFile(lead)
		res, err := Trim(context.Background(), lead)
		if err != nil || !res.Trimmed() || res.Tail() != 0 {
			t.Fatalf("%s lead: %+v, %v", name, res, err)
		}
		head, tailAfter := pads(t, lead)
		if cut, _ := os.ReadFile(lead); !bytes.HasSuffix(whole, cut[len(cut)-min(len(cut), 3000):]) {
			t.Errorf("%s: the end of the file should be exactly as it was when only the head is cut", name)
		}
		if tailAfter < tailBefore-ms(5) || tailAfter > tailBefore+ms(5) {
			t.Errorf("%s: the tail was %v and is now %v although only the head needed cutting", name, tailBefore, tailAfter)
		}
		if head < Default.Pad-ms(5) || head > Default.Pad+frame+ms(25) {
			t.Errorf("%s: the head keeps %v", name, head)
		}

		tail := makeClip(t, dir, "tail-"+strings.ReplaceAll(name, " ", "-")+".mp3", clipSpec{lead: ms(100), speech: time.Second, tail: 3 * time.Second, xing: xing})
		headBefore, _ := pads(t, tail)
		if res, err = Trim(context.Background(), tail); err != nil || !res.Trimmed() || res.Head() != 0 {
			t.Fatalf("%s tail: %+v, %v", name, res, err)
		}
		headAfter, tailAfter := pads(t, tail)
		if headAfter < headBefore-ms(30) || headAfter > headBefore+ms(60) {
			t.Errorf("%s: the head was %v and is now %v although only the tail needed cutting", name, headBefore, headAfter)
		}
		if tailAfter < Default.Pad-ms(5) || tailAfter > Default.Pad+frame+ms(25) {
			t.Errorf("%s: the tail keeps %v", name, tailAfter)
		}
	}
}

func TestTrimOnlyCutsTheEndsAndNotThePausesInsideASpeech(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	path := makeClip(t, dir, "phrase.mp3", clipSpec{lead: 2 * time.Second, speech: 1500 * time.Millisecond, tail: 2 * time.Second, pauses: true})
	before := level(t, path)
	if got := pauses(t, path, ms(200)); len(got) != 1 {
		t.Fatalf("the fixture should hold one long pause, found %v", got)
	}
	if _, err := Trim(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	got := pauses(t, path, ms(200))
	if len(got) != 1 {
		t.Fatalf("pauses after the cut: %v, want the one inside the speech", got)
	}
	within(t, "the pause inside", got[0], ms(300), ms(60))
	paddedLength(t, "length", length(t, path), 1500*time.Millisecond)
	if after := level(t, path); after < before*0.97 || after > before*1.03 {
		t.Errorf("the sound changed: mean power %.0f before, %.0f after", before, after)
	}
}

func TestTrimKeepsRealSpeechIntact(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "speech.mp3")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "flite=text='the quick brown fox jumps over the lazy dog':voice=slt",
		"-af", "adelay=2500:all=1,apad=pad_dur=2.5", "-ac", "1", "-ar", "24000", "-b:a", "32k", "-id3v2_version", "0", "-write_xing", "0", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot synthesize speech: %v\n%s", err, out)
	}
	before, lengthBefore := level(t, path), length(t, path)
	res, err := Trim(context.Background(), path)
	if err != nil || res.Silent || !res.Trimmed() {
		t.Fatalf("%+v, %v", res, err)
	}
	checkPads(t, "real speech", path)
	if after := level(t, path); after < before*0.97 || after > before*1.03 {
		t.Errorf("the speech changed: mean power %.0f before, %.0f after", before, after)
	}
	if got := length(t, path); got > lengthBefore-4*time.Second {
		t.Errorf("the clip is still %v long, it was %v", got, lengthBefore)
	}
}

func TestInspectReportsWhatTrimWouldDoWithoutDoingIt(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	long := makeMP3(t, dir, "long.mp3", 2*time.Second, time.Second, 2*time.Second)
	silent := makeMP3(t, dir, "silent.mp3", 3*time.Second, 0, 0)
	before, _ := os.ReadFile(long)
	stat, _ := os.Stat(long)

	seen, err := Inspect(context.Background(), long)
	if err != nil || !seen.Trimmed() {
		t.Fatalf("Inspect = %+v, %v", seen, err)
	}
	if after, _ := os.ReadFile(long); !bytes.Equal(before, after) {
		t.Error("Inspect must not change the file")
	}
	if now, _ := os.Stat(long); !now.ModTime().Equal(stat.ModTime()) {
		t.Error("Inspect must not rewrite the file")
	}
	if got := leftovers(t, dir); len(got) != 2 {
		t.Errorf("files in the directory: %v", got)
	}
	done, err := Trim(context.Background(), long)
	if err != nil || done != seen {
		t.Errorf("Trim = %+v, %v; Inspect said %+v", done, err, seen)
	}
	if res, err := Inspect(context.Background(), silent); err != nil || !res.Silent {
		t.Errorf("a silent clip: %+v, %v", res, err)
	}
	if _, err := Inspect(context.Background(), filepath.Join(dir, "missing.mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
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
		"sentence.mp3": {ms(90), 3 * time.Second, ms(90)},
	} {
		for _, xing := range []bool{false, true} {
			path := makeClip(t, dir, fmt.Sprintf("%v-%s", xing, name), clipSpec{lead: parts[0], speech: parts[1], tail: parts[2], xing: xing})
			before, _ := os.ReadFile(path)
			stat, _ := os.Stat(path)
			res, err := Trim(context.Background(), path)
			if err != nil || res.Silent || res.Trimmed() {
				t.Errorf("%s (xing %v): %+v, %v", name, xing, res, err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Errorf("%s (xing %v): an untrimmed clip must stay byte for byte the same", name, xing)
			}
			if now, _ := os.Stat(path); !now.ModTime().Equal(stat.ModTime()) {
				t.Errorf("%s (xing %v): the file was rewritten", name, xing)
			}
		}
	}
}

func TestTrimSettlesAfterOnePass(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	for _, xing := range []bool{false, true} {
		path := makeClip(t, dir, fmt.Sprintf("clip-%v.mp3", xing), clipSpec{lead: 2500 * time.Millisecond, speech: 1500 * time.Millisecond, tail: 1700 * time.Millisecond, xing: xing})
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
}

func TestTrimKeepsTheBitrateAndPlainMP3Framing(t *testing.T) {
	needFFmpeg(t)
	path := makeClip(t, t.TempDir(), "clip.mp3", clipSpec{lead: 2 * time.Second, speech: time.Second, tail: 2 * time.Second, xing: true})
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

func TestFFmpegErrorsAreShortened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for ffmpeg is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ni=1\nwhile [ $i -le 400 ]; do echo \"bad frame $i\" >&2; i=$((i+1)); done\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	file := filepath.Join(t.TempDir(), "clip.mp3")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Trim(context.Background(), file)
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bad frame 400") || !strings.Contains(msg, "bad frame 398") || strings.Contains(msg, "bad frame 397") || !strings.Contains(msg, "397 earlier lines left out") {
		t.Errorf("the error should keep the last few lines only:\n%s", msg)
	}
	if len(msg) > 300 {
		t.Errorf("the error is %d characters long", len(msg))
	}
}

func TestLastLines(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"  \n":                 "",
		"one":                  "one",
		"one\ntwo\nthree":      "one; two; three",
		"a\nb\nc\nd":           "b; c; d (1 earlier lines left out)",
		"a\nb\nc\nd\ne\nf\n\n": "d; e; f (3 earlier lines left out)",
		"first\r\nsecond\r\n":  "first\r; second",
	}
	for in, want := range cases {
		if got := lastLines(in); got != want {
			t.Errorf("lastLines(%q) = %q, want %q", in, got, want)
		}
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
		paths = append(paths, makeClip(t, dir, fmt.Sprintf("clip%d.mp3", i), clipSpec{lead: time.Duration(1+i%3) * time.Second, speech: 800 * time.Millisecond, tail: time.Duration(1+i%2) * time.Second, xing: i%2 == 0}))
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
		paddedLength(t, fmt.Sprintf("clip %d length", i), length(t, paths[i]), 800*time.Millisecond)
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

func TestMissingFFmpegFailsInsteadOfSkippingWhenItIsRequired(t *testing.T) {
	if os.Getenv("SOUND_TEST_CHILD") != "" {
		t.Setenv("PATH", t.TempDir())
		needFFmpeg(t)
		return
	}
	for required, want := range map[string]string{"": "SKIP", "1": "FAIL"} {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestMissingFFmpegFailsInsteadOfSkippingWhenItIsRequired$", "-test.v")
		cmd.Env = append(os.Environ(), "SOUND_TEST_CHILD=1", "REQUIRE_FFMPEG="+required)
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "--- "+want) {
			t.Errorf("REQUIRE_FFMPEG=%q: the test should %s without ffmpeg:\n%s", required, want, out)
		}
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
