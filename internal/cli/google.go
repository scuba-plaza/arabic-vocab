package cli

import (
	"context"
	"errors"
	"io"

	"github.com/scuba-plaza/arabic-tts/config"
	"github.com/scuba-plaza/arabic-tts/gcp"
	"github.com/scuba-plaza/arabic-tts/stt"
	"github.com/scuba-plaza/arabic-tts/tts"

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
)

func newSpeaker(ctx context.Context, creds config.Credentials, voice deck.Voice) (deck.Speaker, io.Closer, error) {
	opts := tts.Options{Voice: voice.Name, Language: config.DefaultLanguage, SpeakingRate: voice.Rate, Concurrency: 1}
	if err := opts.Validate(); err != nil {
		return nil, nil, usageError{err}
	}
	client, err := gcp.NewTextToSpeechClient(ctx, creds)
	if err != nil {
		return nil, nil, err
	}
	speak := func(ctx context.Context, text, path string) error {
		o := opts
		o.Output = path
		_, err := tts.Synthesize(ctx, client, text, o)
		return err
	}
	return speak, client, nil
}

func listVoices(ctx context.Context, creds config.Credentials) ([]tts.VoiceInfo, error) {
	client, err := gcp.NewTextToSpeechClient(ctx, creds)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return tts.ListVoices(ctx, client, config.DefaultLanguage, "")
}

func newListener(ctx context.Context, creds config.Credentials) (deck.Listener, io.Closer, error) {
	client, err := gcp.NewSpeechClient(ctx, creds, g.region)
	if err != nil {
		return nil, nil, err
	}
	listen := func(ctx context.Context, path string) (string, error) {
		tr, err := stt.TranscribeFile(ctx, client, path, stt.Options{
			Project: creds.ProjectID, Region: g.region, Language: config.DefaultLanguage,
			Model: config.DefaultSTTModel, Concurrency: 1,
		})
		if errors.Is(err, stt.ErrNoSpeech) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return tr.Text(), nil
	}
	return listen, client, nil
}
