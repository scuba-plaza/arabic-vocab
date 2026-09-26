package deck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/scuba-plaza/arabic-tts/config"
)

const DefaultRate = 0.9

type Settings struct {
	Voice string  `json:"voice"`
	Rate  float64 `json:"rate"`
}

func DefaultSettings() Settings {
	return Settings{Voice: config.DefaultVoice, Rate: DefaultRate}
}

func LoadSettings(path string) (Settings, error) {
	s := DefaultSettings()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if s.Voice == "" {
		s.Voice = config.DefaultVoice
	}
	if s.Rate == 0 {
		s.Rate = DefaultRate
	}
	return s, nil
}

func SaveSettings(path string, s Settings) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func (s Settings) AudioVoice() Voice {
	return Voice{Name: s.Voice, Rate: s.Rate}
}
