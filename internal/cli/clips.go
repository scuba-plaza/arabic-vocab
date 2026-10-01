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

	"github.com/scuba-plaza/arabic-tts/audio"
	"github.com/scuba-plaza/arabic-tts/config"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/review"
)

var clipLabels = map[string]string{"WordAudio": "word", "FormsAudio": "forms", "ExampleAudio": "sentence"}

type clips struct {
	mu       sync.Mutex
	paths    *deck.Paths
	voice    deck.Voice
	verify   bool
	manifest []deck.ManifestEntry
	files    map[string]string
	checks   []notes.AudioCheck
	options  []review.VoiceOption
	speak    deck.Speaker
	listen   deck.Listener
	closers  []io.Closer
}

func newClips(paths *deck.Paths, voice deck.Voice, manifest []deck.ManifestEntry, checks []notes.AudioCheck, verify bool) *clips {
	return &clips{
		paths: paths, voice: voice, verify: verify,
		manifest: manifest, files: deck.AudioIndex(manifest), checks: checks,
	}
}

func clipText(n notes.Note, field string) (string, error) {
	for _, at := range deck.AudioTexts(n) {
		if at.Field == field {
			return at.Text, nil
		}
	}
	return "", fmt.Errorf("%s has no %s to speak", n.Arabic, clipLabels[field])
}

func (c *clips) index() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.files
}

func (c *clips) path(n notes.Note, field string) string {
	text, err := clipText(n, field)
	if err != nil {
		return ""
	}
	c.mu.Lock()
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

func (c *clips) remove(n notes.Note, field string) error {
	text, err := clipText(n, field)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	file := c.files[text]
	if file == "" {
		file = deck.AudioFile(c.voice.Key(), text)
	}
	if err := os.Remove(c.paths.MediaFile(file)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	c.manifest = slices.DeleteFunc(c.manifest, func(m deck.ManifestEntry) bool { return m.Text == text })
	c.checks = slices.DeleteFunc(c.checks, func(a notes.AudioCheck) bool { return a.ID == n.ID && a.Text == text })
	return c.save()
}

func (c *clips) remake(ctx context.Context, n notes.Note, field string) (*notes.AudioCheck, error) {
	text, err := clipText(n, field)
	if err != nil {
		return nil, err
	}
	speak, listen, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	file := deck.AudioFile(c.voice.Key(), text)
	path := c.paths.MediaFile(file)
	if err := speak(ctx, text, path); err != nil {
		return nil, err
	}
	if err := c.store(deck.ManifestEntry{Text: text, Voice: c.voice.Name, Rate: c.voice.Rate, File: file}); err != nil {
		return nil, err
	}
	if field != "ExampleAudio" || listen == nil {
		return nil, nil
	}
	transcript, err := listen(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("the clip was made again, but speech recognition failed: %w", err)
	}
	check := notes.AudioCheck{
		ID: n.ID, Field: field, Text: text, File: file,
		Transcript: transcript, Match: deck.TranscriptMatches(text, transcript),
	}
	if err := c.record(check); err != nil {
		return nil, err
	}
	return &check, nil
}

func (c *clips) connect(ctx context.Context) (deck.Speaker, deck.Listener, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.speak != nil {
		return c.speak, c.listen, nil
	}
	creds, err := resolve()
	if err != nil {
		return nil, nil, err
	}
	speak, client, err := newSpeaker(ctx, creds, c.voice)
	if err != nil {
		return nil, nil, err
	}
	c.speak, c.closers = speak, append(c.closers, client)
	switch {
	case !c.verify:
	case audio.Available() != nil:
		infof("%v; remade clips will not be transcribed back, as --verify asks\n", audio.Available())
	default:
		listen, client, err := newListener(ctx, creds)
		if err != nil {
			infof("speech recognition is not available (%v); remade clips will not be transcribed back\n", err)
		} else {
			c.listen, c.closers = listen, append(c.closers, client)
		}
	}
	return c.speak, c.listen, nil
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

func (c *clips) store(m deck.ManifestEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := slices.IndexFunc(c.manifest, func(e deck.ManifestEntry) bool { return e.Text == m.Text }); i >= 0 {
		c.manifest[i] = m
	} else {
		c.manifest = append(c.manifest, m)
	}
	return c.save()
}

func (c *clips) record(check notes.AudioCheck) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	same := func(a notes.AudioCheck) bool { return a.ID == check.ID && a.Text == check.Text }
	if i := slices.IndexFunc(c.checks, same); i >= 0 {
		c.checks[i] = check
	} else {
		c.checks = append(c.checks, check)
	}
	return c.save()
}

func (c *clips) save() error {
	c.files = deck.AudioIndex(c.manifest)
	if err := notes.WriteJSONL(c.paths.Manifest(), c.manifest); err != nil {
		return err
	}
	return notes.WriteJSONL(c.paths.AudioQA(), c.checks)
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
	c.closers, c.speak, c.listen = nil, nil, nil
}
