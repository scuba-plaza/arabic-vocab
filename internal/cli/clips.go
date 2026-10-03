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
	manifest []deck.ManifestEntry
	files    map[string]string
	options  []review.VoiceOption
	speak    deck.Speaker
	closers  []io.Closer
}

func newClips(paths *deck.Paths, voice deck.Voice, manifest []deck.ManifestEntry) *clips {
	return &clips{paths: paths, voice: voice, manifest: manifest, files: deck.AudioIndex(manifest)}
}

func clipText(n notes.Note, field string) (string, error) {
	for _, at := range deck.AudioTexts(n) {
		if at.Field == field {
			return at.Text, nil
		}
	}
	return "", fmt.Errorf("%s has no %s to speak", n.Arabic, clipLabels[field])
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
	return c.save()
}

func (c *clips) remake(ctx context.Context, n notes.Note, field string) error {
	text, err := clipText(n, field)
	if err != nil {
		return err
	}
	speak, err := c.connect(ctx)
	if err != nil {
		return err
	}
	file := deck.AudioFile(c.voice.Key(), text)
	if err := speak(ctx, text, c.paths.MediaFile(file)); err != nil {
		return err
	}
	return c.store(deck.ManifestEntry{Text: text, Voice: c.voice.Name, Rate: c.voice.Rate, File: file})
}

func (c *clips) connect(ctx context.Context) (deck.Speaker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.speak != nil {
		return c.speak, nil
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

func (c *clips) save() error {
	c.files = deck.AudioIndex(c.manifest)
	return notes.WriteJSONL(c.paths.Manifest(), c.manifest)
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
