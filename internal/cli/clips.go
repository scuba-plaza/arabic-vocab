package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/scuba-plaza/arabic-tts/config"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/review"
	"github.com/scuba-plaza/arabic-vocab/internal/sound"
)

var lockWait = 10 * time.Second

type clips struct {
	mu       sync.Mutex
	paths    *deck.Paths
	voice    deck.Voice
	manifest []deck.ManifestEntry
	files    map[string]string
	seen     time.Time
	options  []review.VoiceOption
	speak    deck.Speaker
	inspect  deck.Inspector
	closers  []io.Closer
}

func newClips(paths *deck.Paths, voice deck.Voice, manifest []deck.ManifestEntry) *clips {
	c := &clips{paths: paths, voice: voice, manifest: manifest, files: deck.AudioIndex(manifest), inspect: sound.Trim}
	c.seen = modTime(paths.Manifest())
	return c
}

func modTime(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func (c *clips) refresh() {
	at := modTime(c.paths.Manifest())
	if at.Equal(c.seen) {
		return
	}
	manifest, err := notes.ReadJSONL[deck.ManifestEntry](c.paths.Manifest())
	if err != nil {
		return
	}
	c.manifest, c.files, c.seen = manifest, deck.AudioIndex(manifest), at
}

func clipText(n notes.Note, field string) (string, error) {
	for _, at := range deck.AudioTexts(n) {
		if at.Field == field {
			return at.Text, nil
		}
	}
	return "", fmt.Errorf("%s has no %s to speak", n.Arabic, deck.ClipLabel(field))
}

func (c *clips) path(n notes.Note, field string) string {
	text, err := clipText(n, field)
	if err != nil {
		return ""
	}
	c.mu.Lock()
	c.refresh()
	file := c.files[text]
	c.mu.Unlock()
	if file == "" {
		return ""
	}
	path := c.paths.MediaFile(file)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func (c *clips) owners(text string, n notes.Note, field string) ([]notes.Note, []deck.ClipOwner) {
	all, _ := notes.ReadJSONL[notes.Note](c.paths.Notes())
	at := slices.IndexFunc(all, func(x notes.Note) bool { return x.ID == n.ID })
	if at < 0 {
		all = append(all, n)
		at = len(all) - 1
	} else {
		all[at] = n
	}
	owners := deck.ClipOwners(all, text)
	if self := (deck.ClipOwner{Index: at, Field: field}); !slices.Contains(owners, self) {
		owners = append(owners, self)
	}
	return all, owners
}

func (c *clips) lock(ctx context.Context) (func(), error) {
	return deck.LockAudio(ctx, c.paths.AudioLock(), lockWait)
}

func (c *clips) commit(n notes.Note, field, text string, entry *deck.ManifestEntry, silentAfter int) error {
	manifest, err := notes.ReadJSONL[deck.ManifestEntry](c.paths.Manifest())
	if err != nil {
		return err
	}
	checks, err := notes.ReadJSONL[notes.Check](c.paths.AudioQA())
	if err != nil {
		return err
	}
	all, owners := c.owners(text, n, field)
	manifest = slices.DeleteFunc(manifest, func(m deck.ManifestEntry) bool { return m.Text == text })
	if entry != nil {
		manifest = append(manifest, *entry)
	}
	for _, o := range owners {
		if silentAfter > 0 {
			checks = deck.WithAudioIssue(checks, all[o.Index], deck.SilentIssue(o.Field, silentAfter))
		} else {
			checks = deck.WithoutAudioIssue(checks, all[o.Index], o.Field)
		}
	}
	if err := notes.WriteJSONL(c.paths.Manifest(), manifest); err != nil {
		return err
	}
	if err := notes.WriteJSONL(c.paths.AudioQA(), checks); err != nil {
		return err
	}
	c.manifest, c.files, c.seen = manifest, deck.AudioIndex(manifest), modTime(c.paths.Manifest())
	return nil
}

func (c *clips) remove(n notes.Note, field string) error {
	text, err := clipText(n, field)
	if err != nil {
		return err
	}
	unlock, err := c.lock(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refresh()
	file := c.files[text]
	if file == "" {
		file = deck.AudioFile(c.voice.Key(), text)
	}
	if err := os.Remove(c.paths.MediaFile(file)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return c.commit(n, field, text, nil, 0)
}

func (c *clips) remake(ctx context.Context, n notes.Note, field string) (*notes.Issue, error) {
	text, err := clipText(n, field)
	if err != nil {
		return nil, err
	}
	speak, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	file := deck.AudioFile(c.voice.Key(), text)
	path := c.paths.MediaFile(file)
	_, statErr := os.Stat(path)
	had := statErr == nil
	out, err := deck.MakeClip(ctx, speak, c.inspect, text, path, deck.DefaultClipAttempts)
	if err != nil {
		return nil, err
	}
	if out.Silent && had {
		return nil, fmt.Errorf("%w; the clip you had was kept", deck.SilentError(out.Attempts))
	}
	unlock, err := c.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if out.Silent {
		issue := deck.SilentIssue(field, out.Attempts)
		return &issue, c.commit(n, field, text, nil, out.Attempts)
	}
	entry := deck.ManifestEntry{Text: text, Voice: c.voice.Name, Rate: c.voice.Rate, File: file, Inspected: true}
	return nil, c.commit(n, field, text, &entry, 0)
}

func (c *clips) flags(ns []notes.Note) map[string][]notes.Issue {
	checks, err := notes.ReadJSONL[notes.Check](c.paths.AudioQA())
	if err != nil {
		return nil
	}
	byID := map[string]notes.Check{}
	for _, check := range checks {
		byID[check.ID] = check
	}
	out := map[string][]notes.Issue{}
	for _, n := range ns {
		if check, ok := byID[n.ID]; ok {
			out[n.ID] = deck.OpenAudioIssues(n, check)
		}
	}
	return out
}

func (c *clips) connect(ctx context.Context) (deck.Speaker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.speak != nil {
		return c.speak, nil
	}
	if err := sound.Available(); err != nil {
		return nil, fmt.Errorf("%w; ffmpeg is needed to cut the silence off the clips", err)
	}
	creds, err := resolve()
	if err != nil {
		return nil, err
	}
	speak, client, err := newSpeaker(ctx, creds, c.voice)
	if err != nil {
		return nil, err
	}
	c.speak, c.closers = speak, append(c.closers, client)
	return c.speak, nil
}

func (c *clips) voiceOptions(ctx context.Context) ([]review.VoiceOption, error) {
	c.mu.Lock()
	cached := c.options
	c.mu.Unlock()
	if len(cached) > 0 {
		return cached, nil
	}
	creds, err := resolve()
	if err != nil {
		return nil, err
	}
	all, err := listVoices(ctx, creds)
	if err != nil {
		return nil, err
	}
	out := make([]review.VoiceOption, 0, len(all))
	for _, v := range all {
		out = append(out, review.VoiceOption{Name: v.Name, Tier: v.Tier, Gender: v.Gender})
	}
	c.mu.Lock()
	c.options = out
	c.mu.Unlock()
	return out, nil
}

func (c *clips) setVoice(name string) error {
	if !strings.HasPrefix(name, config.DefaultLanguage+"-") {
		return fmt.Errorf("%q is not an %s voice", name, config.DefaultLanguage)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	settings, err := deck.LoadSettings(c.paths.Settings())
	if err != nil {
		return err
	}
	settings.Voice = name
	if err := deck.SaveSettings(c.paths.Settings(), settings); err != nil {
		return err
	}
	c.voice = settings.AudioVoice()
	c.shut()
	return nil
}

func (c *clips) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shut()
}

func (c *clips) shut() {
	for _, client := range c.closers {
		client.Close()
	}
	c.closers, c.speak = nil, nil
}
