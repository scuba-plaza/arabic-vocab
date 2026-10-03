package sound

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const analysisRate = 16000

func Available() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg was not found on PATH")
	}
	return nil
}

func Inspect(ctx context.Context, path string) (Result, error) {
	return inspect(ctx, path, Default)
}

func Trim(ctx context.Context, path string) (Result, error) {
	return trim(ctx, path, Default)
}

func inspect(ctx context.Context, path string, cfg Config) (Result, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if st.Size() == 0 {
		return Result{Silent: true}, nil
	}
	pcm, err := decode(ctx, path)
	if err != nil {
		return Result{}, err
	}
	return Analyze(pcm, analysisRate, cfg), nil
}

func trim(ctx context.Context, path string, cfg Config) (Result, error) {
	res, err := inspect(ctx, path, cfg)
	if err != nil || !res.Trimmed() {
		return res, err
	}
	if err := cut(ctx, path, res); err != nil {
		return Result{}, err
	}
	return res, nil
}

const maxDetailLines = 3

func lastLines(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	left := 0
	if len(lines) > maxDetailLines {
		left = len(lines) - maxDetailLines
		lines = lines[left:]
	}
	out := strings.Join(lines, "; ")
	if left > 0 {
		out = fmt.Sprintf("%s (%d earlier lines left out)", out, left)
	}
	return out
}

func run(ctx context.Context, what string, stdout *bytes.Buffer, args ...string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", append([]string{"-hide_banner", "-nostdin", "-v", "error"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if detail := lastLines(stderr.String()); detail != "" {
			return fmt.Errorf("%s: %w: %s", what, err, detail)
		}
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func decode(ctx context.Context, path string) ([]int16, error) {
	var out bytes.Buffer
	err := run(ctx, "decoding "+filepath.Base(path), &out,
		"-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(analysisRate), "-f", "s16le", "-")
	if err != nil {
		return nil, err
	}
	raw := out.Bytes()
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return pcm, nil
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

const frame = 30 * time.Millisecond

func cut(ctx context.Context, path string, res Result) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".trim-*"+filepath.Ext(path))
	if err != nil {
		return err
	}
	tmp := file.Name()
	file.Close()
	defer os.Remove(tmp)
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	args := []string{"-y", "-i", path}
	if start := res.Start - frame; start > 0 {
		args = append(args, "-ss", seconds(start))
	}
	if end := res.End + frame; end < res.Duration {
		args = append(args, "-to", seconds(end))
	}
	args = append(args, "-c", "copy", "-write_xing", "0", "-id3v2_version", "0", "-map_metadata", "-1", "-f", "mp3", tmp)
	if err := run(ctx, "trimming "+filepath.Base(path), nil, args...); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
