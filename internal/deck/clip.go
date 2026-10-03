package deck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

const (
	DefaultClipAttempts = 4
	AudioCheckVersion   = 1
	KindSilent          = "silent"
)

var (
	ErrSilent     = errors.New("the clip has no sound")
	ErrUnreadable = errors.New("the clip cannot be read")
)

type Inspector func(ctx context.Context, path string) (sound.Result, error)

type ClipOutcome struct {
	Silent   bool
	Attempts int
	Trimmed  bool
}

func ClipLabel(field string) string {
	switch field {
	case "WordAudio":
		return "word"
	case "FormsAudio":
		return "forms"
	case "ExampleAudio":
		return "sentence"
	}
	return field
}

func MakeClip(ctx context.Context, speak Speaker, inspect Inspector, text, path string, attempts int) (ClipOutcome, error) {
	attempts = max(attempts, 1)
	var out ClipOutcome
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return out, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".make-*"+filepath.Ext(path))
	if err != nil {
		return out, err
	}
	tmp := file.Name()
	file.Close()
	defer os.Remove(tmp)
	if err := os.Chmod(tmp, 0o644); err != nil {
		return out, err
	}
	for out.Attempts < attempts {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out.Attempts++
		if err := speak(ctx, text, tmp); err != nil {
			return out, fmt.Errorf("synthesizing %q: %w", text, err)
		}
		res, err := inspect(ctx, tmp)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			detail := strings.NewReplacer(tmp, path, filepath.Base(tmp), filepath.Base(path)).Replace(err.Error())
			return out, fmt.Errorf("inspecting the clip of %q for %s: %w: %s", text, path, ErrUnreadable, detail)
		}
		if res.Silent {
			continue
		}
		out.Trimmed = res.Trimmed()
		return out, os.Rename(tmp, path)
	}
	out.Silent = true
	return out, nil
}

func SilentIssue(field string, attempts int) notes.Issue {
	tries := fmt.Sprintf("in all %d attempts", attempts)
	if attempts == 1 {
		tries = "on its only attempt"
	}
	return notes.Issue{
		Field:    field,
		Kind:     KindSilent,
		Severity: notes.Major,
		Detail:   fmt.Sprintf("The %s audio has no sound: it came back silent %s.", ClipLabel(field), tries),
	}
}

type ClipOwner struct {
	Index int
	Field string
}

func ClipOwners(ns []notes.Note, text string) []ClipOwner {
	var out []ClipOwner
	for i, n := range ns {
		if !n.Authored() {
			continue
		}
		for _, at := range AudioTexts(n) {
			if at.Text == text {
				out = append(out, ClipOwner{Index: i, Field: at.Field})
			}
		}
	}
	return out
}

func attemptsText(n int) string {
	if n == 1 {
		return "1 attempt"
	}
	return fmt.Sprintf("%d attempts", n)
}

func SilentError(attempts int) error {
	return fmt.Errorf("%w after %s", ErrSilent, attemptsText(attempts))
}

func CurrentAudio(n notes.Note, c notes.Check) bool {
	return c.Version == AudioCheckVersion && c.Digest == n.Digest()
}

func sortChecks(checks []notes.Check) {
	for i := range checks {
		issues := slices.Clone(checks[i].Issues)
		slices.SortFunc(issues, func(a, b notes.Issue) int {
			return strings.Compare(a.Field+"\x00"+a.Kind, b.Field+"\x00"+b.Kind)
		})
		checks[i].Issues = issues
	}
	slices.SortFunc(checks, func(a, b notes.Check) int { return strings.Compare(a.ID, b.ID) })
}

func PruneAudioChecks(checks []notes.Check, ns []notes.Note) []notes.Check {
	byID := map[string]notes.Note{}
	for _, n := range ns {
		byID[n.ID] = n
	}
	out := []notes.Check{}
	for _, c := range checks {
		if n, ok := byID[c.ID]; ok && CurrentAudio(n, c) && len(c.Issues) > 0 {
			out = append(out, c)
		}
	}
	sortChecks(out)
	return out
}

func WithAudioIssue(checks []notes.Check, n notes.Note, issue notes.Issue) []notes.Check {
	out := slices.Clone(checks)
	fresh := notes.Check{ID: n.ID, Version: AudioCheckVersion, Digest: n.Digest()}
	at := slices.IndexFunc(out, func(c notes.Check) bool { return c.ID == n.ID })
	if at >= 0 && CurrentAudio(n, out[at]) {
		fresh.Issues = slices.Clone(out[at].Issues)
	}
	fresh.Issues = slices.DeleteFunc(fresh.Issues, func(is notes.Issue) bool { return is.Field == issue.Field && is.Kind == issue.Kind })
	fresh.Issues = append(fresh.Issues, issue)
	if at >= 0 {
		out[at] = fresh
	} else {
		out = append(out, fresh)
	}
	sortChecks(out)
	return out
}

func WithoutAudioIssue(checks []notes.Check, n notes.Note, field string) []notes.Check {
	at := slices.IndexFunc(checks, func(c notes.Check) bool { return c.ID == n.ID })
	if at < 0 {
		return checks
	}
	out := slices.Clone(checks)
	if !CurrentAudio(n, out[at]) {
		return slices.Delete(out, at, at+1)
	}
	kept := slices.DeleteFunc(slices.Clone(out[at].Issues), func(is notes.Issue) bool { return is.Field == field })
	if len(kept) == 0 {
		return slices.Delete(out, at, at+1)
	}
	out[at].Issues = kept
	return out
}
