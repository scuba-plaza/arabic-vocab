package notes

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Form struct {
	Label  string `json:"label"`
	Arabic string `json:"arabic"`
}

type Note struct {
	ID         string   `json:"id"`
	Position   int      `json:"position"`
	Arabic     string   `json:"arabic"`
	Pos        string   `json:"pos"`
	Gender     string   `json:"gender,omitempty"`
	VerbForm   string   `json:"verb_form,omitempty"`
	Root       string   `json:"root,omitempty"`
	Forms      []Form   `json:"forms,omitempty"`
	English    string   `json:"english"`
	Hint       string   `json:"hint,omitempty"`
	Example    string   `json:"example"`
	ExampleEn  string   `json:"example_en"`
	Production *bool    `json:"production,omitempty"`
	Reviewed   []string `json:"reviewed,omitempty"`
	CEFR       string   `json:"cefr,omitempty"`
	Source     string   `json:"source,omitempty"`
	Comment    string   `json:"comment,omitempty"`
}

func (n *Note) Authored() bool {
	return n.English != "" && n.Example != "" && n.ExampleEn != ""
}

func (n *Note) FormsText() string {
	var parts []string
	for _, f := range n.Forms {
		parts = append(parts, f.Arabic)
	}
	return strings.Join(parts, "، ")
}

func (n *Note) Digest() string {
	h := sha256.New()
	for _, s := range []string{n.Arabic, n.FormsText(), n.Example} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type Severity string

const (
	Major Severity = "major"
	Minor Severity = "minor"
)

type Issue struct {
	Field    string   `json:"field"`
	Kind     string   `json:"kind"`
	Severity Severity `json:"severity"`
	Word     string   `json:"word,omitempty"`
	Detail   string   `json:"detail"`
}

type Check struct {
	ID     string  `json:"id"`
	Digest string  `json:"digest"`
	Issues []Issue `json:"issues,omitempty"`
}

type AudioCheck struct {
	ID         string `json:"id"`
	Field      string `json:"field"`
	Text       string `json:"text"`
	File       string `json:"file"`
	Transcript string `json:"transcript"`
	Match      bool   `json:"match"`
}

func ReadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeJSONL[T](f, path)
}

func DecodeJSONL[T any](r io.Reader, name string) ([]T, error) {
	var out []T
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", name, line, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

func WriteJSONL[T any](path string, items []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	for _, item := range items {
		raw, err := marshal(item)
		if err != nil {
			tmp.Close()
			return err
		}
		w.Write(raw)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func marshal(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(b.String(), "\n")), nil
}

func Sort(ns []Note) {
	slices.SortStableFunc(ns, func(a, b Note) int { return a.Position - b.Position })
}

func Validate(ns []Note) error {
	ids := map[string]bool{}
	positions := map[int]string{}
	for _, n := range ns {
		if n.ID == "" {
			return fmt.Errorf("a note has no id")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate note id %s", n.ID)
		}
		ids[n.ID] = true
		if other, ok := positions[n.Position]; ok {
			return fmt.Errorf("notes %s and %s share position %d", other, n.ID, n.Position)
		}
		positions[n.Position] = n.ID
		if strings.Count(n.Example, "<b>") != strings.Count(n.Example, "</b>") {
			return fmt.Errorf("note %s: unbalanced <b> in example", n.ID)
		}
	}
	return nil
}
