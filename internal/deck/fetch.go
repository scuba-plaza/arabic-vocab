package deck

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Source struct {
	Name     string
	URL      string
	Path     string
	ZipEntry string
	MaxLines int
}

func KaikkiSource(p Paths) Source {
	return Source{Name: "Wiktionary Arabic (kaikki.org)", URL: "https://kaikki.org/dictionary/Arabic/kaikki.org-dictionary-Arabic.jsonl", Path: p.Kaikki()}
}

func Sources(p Paths) []Source {
	return []Source{
		KaikkiSource(p),
		{Name: "OpenSubtitles frequency list", URL: "https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/ar/ar_50k.txt", Path: p.Subtitles()},
		{Name: "CAMeL MSA frequency list", URL: "https://github.com/CAMeL-Lab/Camel_Arabic_Frequency_Lists/releases/download/v1.0/MSA_freq_lists.tsv.zip", Path: p.MSA(), ZipEntry: "MSA_freq_lists.tsv", MaxLines: 200000},
		{Name: "Kelly CEFR list", URL: "https://raw.githubusercontent.com/kotoshu/frequency-list-kelly/main/data/ar.json", Path: p.Kelly()},
	}
}

func Fetch(ctx context.Context, client *http.Client, s Source, force bool) (bool, error) {
	if !force {
		if st, err := os.Stat(s.Path); err == nil && st.Size() > 0 {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "arabic-vocab")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("downloading %s: %w", s.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("downloading %s: %s", s.Name, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".fetch-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return false, fmt.Errorf("downloading %s: %w", s.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if s.ZipEntry == "" {
		if err := os.Rename(tmp.Name(), s.Path); err != nil {
			return false, err
		}
		return true, os.Chmod(s.Path, 0o644)
	}
	return true, extract(tmp.Name(), s)
}

func extract(zipPath string, s Source) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("opening %s archive: %w", s.Name, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) != s.ZipEntry {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.Create(s.Path + ".partial")
		if err != nil {
			return err
		}
		w := bufio.NewWriter(out)
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		lines := 0
		for sc.Scan() && (s.MaxLines == 0 || lines < s.MaxLines) {
			w.WriteString(sc.Text())
			w.WriteByte('\n')
			lines++
		}
		if err := sc.Err(); err != nil {
			out.Close()
			return err
		}
		if err := w.Flush(); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Rename(s.Path+".partial", s.Path)
	}
	return fmt.Errorf("%s archive has no %s", s.Name, s.ZipEntry)
}

func Describe(s Source) string {
	host := s.URL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	if strings.Contains(s.Name, host) {
		return s.Name
	}
	return fmt.Sprintf("%s (%s)", s.Name, host)
}
