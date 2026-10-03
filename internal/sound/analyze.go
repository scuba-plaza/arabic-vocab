package sound

import (
	"math"
	"time"
)

type Config struct {
	ThresholdDB float64
	Window      time.Duration
	MinSound    time.Duration
	Pad         time.Duration
	MinTrim     time.Duration
}

var Default = Config{
	ThresholdDB: -50,
	Window:      10 * time.Millisecond,
	MinSound:    50 * time.Millisecond,
	Pad:         100 * time.Millisecond,
	MinTrim:     50 * time.Millisecond,
}

type Result struct {
	Silent   bool
	Duration time.Duration
	Start    time.Duration
	End      time.Duration
}

func (r Result) Head() time.Duration {
	return r.Start
}

func (r Result) Tail() time.Duration {
	if r.Silent {
		return 0
	}
	return r.Duration - r.End
}

func (r Result) Trimmed() bool {
	return !r.Silent && (r.Start > 0 || r.End < r.Duration)
}

func duration(samples, rate int) time.Duration {
	return time.Duration(samples) * time.Second / time.Duration(rate)
}

func samples(d time.Duration, rate int) int {
	return int(d * time.Duration(rate) / time.Second)
}

func Analyze(pcm []int16, rate int, cfg Config) Result {
	if rate <= 0 {
		panic("sound: the sample rate must be positive")
	}
	res := Result{Duration: duration(len(pcm), rate)}
	window := max(1, samples(cfg.Window, rate))
	minRun := max(1, int((cfg.MinSound+cfg.Window-1)/cfg.Window))
	limit := math.Pow(10, cfg.ThresholdDB/10) * math.MaxInt16 * math.MaxInt16

	first, last, run := -1, -1, 0
	for w := 0; w*window < len(pcm); w++ {
		lo, hi := w*window, min((w+1)*window, len(pcm))
		var sum float64
		for _, s := range pcm[lo:hi] {
			sum += float64(s) * float64(s)
		}
		if sum/float64(hi-lo) <= limit {
			run = 0
			continue
		}
		run++
		if run < minRun {
			continue
		}
		if first < 0 {
			first = w - run + 1
		}
		last = w
	}
	if first < 0 {
		res.Silent = true
		return res
	}

	start := first * window
	end := min((last+1)*window, len(pcm))
	pad := samples(cfg.Pad, rate)
	minTrim := samples(cfg.MinTrim, rate)
	if head := start - pad; head >= minTrim && head > 0 {
		start = head
	} else {
		start = 0
	}
	if tail := len(pcm) - (end + pad); tail >= minTrim && tail > 0 {
		end += pad
	} else {
		end = len(pcm)
	}
	res.Start, res.End = duration(start, rate), duration(end, rate)
	return res
}
