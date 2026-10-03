package sound

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

const testRate = 16000

func ms(n int) time.Duration {
	return time.Duration(n) * time.Millisecond
}

func quiet(d time.Duration) []int16 {
	return make([]int16, samples(d, testRate))
}

func tone(d time.Duration, rmsDB float64) []int16 {
	amplitude := math.Pow(10, rmsDB/20) * math.Sqrt2 * math.MaxInt16
	out := make([]int16, samples(d, testRate))
	for i := range out {
		out[i] = int16(math.Round(amplitude * math.Sin(2*math.Pi*440*float64(i)/testRate)))
	}
	return out
}

func hiss(d time.Duration, amplitude int, seed int64) []int16 {
	r := rand.New(rand.NewSource(seed))
	out := make([]int16, samples(d, testRate))
	for i := range out {
		out[i] = int16(r.Intn(2*amplitude+1) - amplitude)
	}
	return out
}

func join(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func within(t *testing.T, name string, got, want, slack time.Duration) {
	t.Helper()
	if got < want-slack || got > want+slack {
		t.Errorf("%s = %v, want %v ± %v", name, got, want, slack)
	}
}

func TestAnalyzeTreatsNothingAudibleAsSilent(t *testing.T) {
	cases := map[string][]int16{
		"no samples":          nil,
		"digital silence":     quiet(3 * time.Second),
		"a few samples":       quiet(ms(2)),
		"faint hiss":          hiss(2*time.Second, 4, 1),
		"tone below floor":    tone(time.Second, -60),
		"hiss around a tone":  join(hiss(time.Second, 3, 2), tone(time.Second, -58), hiss(time.Second, 3, 3)),
		"one loud click":      join(quiet(time.Second), []int16{30000}, quiet(time.Second)),
		"one loud window":     join(quiet(time.Second), tone(ms(10), -10), quiet(time.Second)),
		"burst under minimum": join(quiet(time.Second), tone(ms(40), -10), quiet(time.Second)),
		"two separate clicks": join(tone(ms(30), -10), quiet(time.Second), tone(ms(30), -10)),
	}
	for name, pcm := range cases {
		res := Analyze(pcm, testRate, Default)
		if !res.Silent || res.Trimmed() || res.Head() != 0 || res.Tail() != 0 {
			t.Errorf("%s: %+v, want silent and untouched", name, res)
		}
		if want := duration(len(pcm), testRate); res.Duration != want {
			t.Errorf("%s: duration %v, want %v", name, res.Duration, want)
		}
	}
}

func TestAnalyzeNeedsASustainedSoundToCountAsOne(t *testing.T) {
	for _, tc := range []struct {
		burst  time.Duration
		silent bool
	}{{ms(10), true}, {ms(30), true}, {ms(40), true}, {ms(50), false}, {ms(60), false}, {time.Second, false}} {
		res := Analyze(join(quiet(time.Second), tone(tc.burst, -10), quiet(time.Second)), testRate, Default)
		if res.Silent != tc.silent {
			t.Errorf("a %v burst: silent = %v, want %v", tc.burst, res.Silent, tc.silent)
		}
	}
}

func TestAnalyzeTrimsBothEndsAndKeepsAPad(t *testing.T) {
	pcm := join(quiet(2*time.Second), tone(time.Second, -20), quiet(2*time.Second))
	res := Analyze(pcm, testRate, Default)
	if res.Silent || !res.Trimmed() {
		t.Fatalf("result = %+v", res)
	}
	within(t, "start", res.Start, 1900*time.Millisecond, ms(10))
	within(t, "end", res.End, 3100*time.Millisecond, ms(10))
	within(t, "head", res.Head(), 1900*time.Millisecond, ms(10))
	within(t, "tail", res.Tail(), 1900*time.Millisecond, ms(10))
	if res.Duration != 5*time.Second {
		t.Errorf("duration = %v", res.Duration)
	}
}

func TestAnalyzeTrimsOnlyTheSideThatHasSilence(t *testing.T) {
	lead := Analyze(join(quiet(3*time.Second), tone(time.Second, -20)), testRate, Default)
	within(t, "lead only: start", lead.Start, 2900*time.Millisecond, ms(10))
	if lead.End != lead.Duration || lead.Tail() != 0 || !lead.Trimmed() {
		t.Errorf("lead only: %+v", lead)
	}
	tail := Analyze(join(tone(time.Second, -20), quiet(3*time.Second)), testRate, Default)
	within(t, "tail only: end", tail.End, 1100*time.Millisecond, ms(10))
	if tail.Start != 0 || tail.Head() != 0 || !tail.Trimmed() {
		t.Errorf("tail only: %+v", tail)
	}
}

func TestAnalyzeLeavesAudioWithoutSurplusSilenceAlone(t *testing.T) {
	cases := map[string][]int16{
		"sound only":                 tone(time.Second, -20),
		"natural pad":                join(quiet(ms(100)), tone(time.Second, -20), quiet(ms(100))),
		"just under the trim limit":  join(quiet(ms(149)), tone(time.Second, -20), quiet(ms(149))),
		"sound right at both ends":   join(tone(time.Second, -20)),
		"sound close to both ends":   join(quiet(ms(30)), tone(time.Second, -20), quiet(ms(30))),
		"shorter than the pad":       join(quiet(ms(60)), tone(ms(200), -20), quiet(ms(60))),
		"single window of lead-in":   join(quiet(ms(10)), tone(time.Second, -20)),
		"a clip as short as minimum": tone(ms(50), -20),
	}
	for name, pcm := range cases {
		res := Analyze(pcm, testRate, Default)
		if res.Silent || res.Trimmed() || res.Start != 0 || res.End != res.Duration {
			t.Errorf("%s: %+v, want it left alone", name, res)
		}
	}
}

func TestAnalyzeTrimLimitIsInclusive(t *testing.T) {
	at := Analyze(join(quiet(ms(150)), tone(time.Second, -20)), testRate, Default)
	within(t, "150ms of lead-in", at.Start, ms(50), ms(1))
	below := Analyze(join(quiet(ms(140)), tone(time.Second, -20)), testRate, Default)
	if below.Trimmed() {
		t.Errorf("140ms of lead-in: %+v", below)
	}
	over := Analyze(join(quiet(ms(160)), tone(time.Second, -20)), testRate, Default)
	within(t, "160ms of lead-in", over.Start, ms(60), ms(1))
}

func TestAnalyzeKeepsPausesInsideTheSpeech(t *testing.T) {
	pcm := join(quiet(2*time.Second), tone(ms(500), -20), quiet(1500*time.Millisecond), tone(ms(500), -20), quiet(2*time.Second))
	res := Analyze(pcm, testRate, Default)
	within(t, "start", res.Start, 1900*time.Millisecond, ms(10))
	within(t, "end", res.End, 4600*time.Millisecond, ms(10))
}

func TestAnalyzeIgnoresClicksOutsideTheSpeech(t *testing.T) {
	pcm := join(tone(ms(10), -10), quiet(3*time.Second), tone(time.Second, -20), quiet(3*time.Second), tone(ms(20), -10))
	res := Analyze(pcm, testRate, Default)
	within(t, "start", res.Start, 2910*time.Millisecond, ms(10))
	within(t, "end", res.End, 4110*time.Millisecond, ms(10))
}

func TestAnalyzeHearsQuietSpeechButNotQuietNoise(t *testing.T) {
	speech := Analyze(join(quiet(2*time.Second), tone(time.Second, -42), quiet(2*time.Second)), testRate, Default)
	if speech.Silent || !speech.Trimmed() {
		t.Errorf("speech at -42 dB: %+v", speech)
	}
	noise := Analyze(join(quiet(time.Second), tone(time.Second, -55), quiet(time.Second)), testRate, Default)
	if !noise.Silent {
		t.Errorf("a tone at -55 dB: %+v", noise)
	}
}

func TestAnalyzeThresholdIsConfigurable(t *testing.T) {
	pcm := join(quiet(2*time.Second), tone(time.Second, -40), quiet(2*time.Second))
	strict := Default
	strict.ThresholdDB = -30
	if !Analyze(pcm, testRate, strict).Silent {
		t.Error("a -30 dB threshold should not hear a -40 dB tone")
	}
	lax := Default
	lax.ThresholdDB = -70
	noisy := join(hiss(2*time.Second, 40, 7), tone(time.Second, -20), hiss(2*time.Second, 40, 8))
	if !Analyze(noisy, testRate, Default).Trimmed() || Analyze(noisy, testRate, lax).Trimmed() {
		t.Error("only the lax threshold should mistake the hiss for sound")
	}
}

func TestAnalyzeWithoutPadCutsAtTheSound(t *testing.T) {
	cfg := Default
	cfg.Pad, cfg.MinTrim = 0, 0
	res := Analyze(join(quiet(2*time.Second), tone(time.Second, -20), quiet(2*time.Second)), testRate, cfg)
	within(t, "start", res.Start, 2*time.Second, ms(10))
	within(t, "end", res.End, 3*time.Second, ms(10))
}

func TestAnalyzeHandlesLengthsThatDoNotFillAWindow(t *testing.T) {
	ragged := Analyze(join(quiet(2*time.Second), tone(time.Second, -20), make([]int16, 12345)), testRate, Default)
	if ragged.Silent {
		t.Fatalf("ragged tail: %+v", ragged)
	}
	within(t, "ragged tail: end", ragged.End, 3100*time.Millisecond, ms(10))

	odd := Analyze(join(quiet(ms(2500)), tone(ms(333), -20), make([]int16, 4321)), testRate, Default)
	within(t, "odd lengths: start", odd.Start, 2400*time.Millisecond, ms(10))
	within(t, "odd lengths: end", odd.End, 2933*time.Millisecond, ms(10))

	custom := Default
	custom.Window = ms(7)
	other := Analyze(join(quiet(2*time.Second), tone(time.Second, -20), quiet(2*time.Second)), testRate, custom)
	within(t, "7ms windows: start", other.Start, 1900*time.Millisecond, ms(14))
	within(t, "7ms windows: end", other.End, 3100*time.Millisecond, ms(14))
}

func TestAnalyzeDoesNotDependOnTheSampleRate(t *testing.T) {
	for _, rate := range []int{8000, 16000, 22050, 24000, 44100} {
		n := func(d time.Duration) int { return samples(d, rate) }
		pcm := make([]int16, n(5*time.Second))
		for i := n(2 * time.Second); i < n(3*time.Second); i++ {
			pcm[i] = int16(math.Round(0.1 * math.MaxInt16 * math.Sin(2*math.Pi*440*float64(i)/float64(rate))))
		}
		res := Analyze(pcm, rate, Default)
		within(t, "start", res.Start, 1900*time.Millisecond, ms(15))
		within(t, "end", res.End, 3100*time.Millisecond, ms(15))
		within(t, "duration", res.Duration, 5*time.Second, ms(1))
	}
}

func TestAnalyzeRejectsASampleRateOfZero(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a rate of zero should be a programming error")
		}
	}()
	Analyze([]int16{1, 2, 3}, 0, Default)
}

func TestResultMethods(t *testing.T) {
	silent := Result{Silent: true, Duration: time.Second, Start: 5, End: 7}
	if silent.Trimmed() || silent.Tail() != 0 {
		t.Errorf("a silent result is never trimmed: %+v", silent)
	}
	r := Result{Duration: 5 * time.Second, Start: time.Second, End: 4 * time.Second}
	if !r.Trimmed() || r.Head() != time.Second || r.Tail() != time.Second {
		t.Errorf("result = %+v", r)
	}
	if (Result{Duration: time.Second, End: time.Second}).Trimmed() {
		t.Error("keeping everything is not a trim")
	}
}

func cutPCM(pcm []int16, res Result) []int16 {
	return pcm[samples(res.Start, testRate):samples(res.End, testRate)]
}

func loud(pcm []int16) int {
	n := 0
	for _, s := range pcm {
		if s > 500 || s < -500 {
			n++
		}
	}
	return n
}

func TestAnalyzeNeverCutsSoundAndSettlesAfterOneTrim(t *testing.T) {
	r := rand.New(rand.NewSource(20260103))
	for i := 0; i < 400; i++ {
		lead, tail := ms(r.Intn(3500)), ms(r.Intn(3500))
		level := -45 + 40*r.Float64()
		pieces := [][]int16{quiet(lead)}
		speech := 0
		for p := 0; p < 1+r.Intn(4); p++ {
			if p > 0 {
				gap := ms(20 + r.Intn(1500))
				pieces = append(pieces, quiet(gap))
				speech += samples(gap, testRate)
			}
			burst := ms(100 + r.Intn(900))
			pieces = append(pieces, tone(burst, level))
			speech += samples(burst, testRate)
		}
		pieces = append(pieces, quiet(tail))
		pcm := join(pieces...)
		soundStart := duration(samples(lead, testRate), testRate)
		soundEnd := soundStart + duration(speech, testRate)

		res := Analyze(pcm, testRate, Default)
		if res.Silent {
			t.Fatalf("case %d: speech at %.0f dB was called silent", i, level)
		}
		if res.Start > soundStart || res.End < soundEnd {
			t.Fatalf("case %d: kept %v–%v but the sound runs %v–%v", i, res.Start, res.End, soundStart, soundEnd)
		}
		kept := cutPCM(pcm, res)
		if loud(kept) != loud(pcm) {
			t.Fatalf("case %d: the cut removed sound (%d of %d loud samples kept)", i, loud(kept), loud(pcm))
		}

		keptLead, keptTail := soundStart-res.Start, res.End-soundEnd
		switch {
		case res.Start == 0 && lead > Default.Pad+Default.MinTrim:
			t.Errorf("case %d: %v of lead-in should have been trimmed", i, lead)
		case res.Start > 0 && lead <= Default.Pad+Default.MinTrim:
			t.Errorf("case %d: %v of lead-in should have been left alone", i, lead)
		case res.Start > 0:
			within(t, "kept lead-in", keptLead, Default.Pad, ms(10))
		}
		switch {
		case res.End == res.Duration && tail > Default.Pad+Default.MinTrim+ms(10):
			t.Errorf("case %d: %v of tail should have been trimmed", i, tail)
		case res.End < res.Duration && tail <= Default.Pad+Default.MinTrim:
			t.Errorf("case %d: %v of tail should have been left alone", i, tail)
		case res.End < res.Duration:
			within(t, "kept tail", keptTail, Default.Pad, ms(10))
		}

		again := Analyze(kept, testRate, Default)
		if again.Silent || again.Trimmed() {
			t.Fatalf("case %d: a second pass changed the clip: %+v", i, again)
		}
	}
}
