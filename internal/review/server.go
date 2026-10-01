package review

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

//go:embed web
var web embed.FS

func Serve(ctx context.Context, ns []notes.Note, items []Item, opts Options, started func(url string)) (Summary, error) {
	s := newSession(ctx, ns, items, opts)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Summary{}, err
	}
	token := rand.Text()
	finished := make(chan struct{})
	var once sync.Once
	srv := &http.Server{
		Handler:           s.handler(token, ln.Addr().String(), func() { once.Do(func() { close(finished) }) }),
		ReadHeaderTimeout: 10 * time.Second,
	}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()
	started(fmt.Sprintf("http://%s/%s/", ln.Addr(), token))
	var serveErr error
	select {
	case <-ctx.Done():
	case <-finished:
	case serveErr = <-failed:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
	s.stopAll()
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return s.summary(), serveErr
}

func (s *session) handler(token, host string, finish func()) http.Handler {
	mux := http.NewServeMux()
	base := "/" + token + "/"
	for name, kind := range map[string]string{
		"index.html": "text/html; charset=utf-8",
		"app.css":    "text/css; charset=utf-8",
		"app.js":     "text/javascript; charset=utf-8",
	} {
		pattern := "GET " + base + name
		if name == "index.html" {
			pattern = "GET " + base + "{$}"
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			raw, err := web.ReadFile("web/" + name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", kind)
			w.Write(raw)
		})
	}
	mux.HandleFunc("GET "+base+"font.ttf", func(w http.ResponseWriter, r *http.Request) {
		if s.opts.FontPath == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "font/ttf")
		http.ServeFile(w, r, s.opts.FontPath)
	})
	mux.HandleFunc("GET "+base+"audio", s.audio)
	mux.HandleFunc("GET "+base+"api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.view(index(r)))
	})
	mux.HandleFunc("GET "+base+"api/voices", func(w http.ResponseWriter, r *http.Request) {
		voices, err := s.voices(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, result{Message: "the voices could not be listed: " + err.Error(), Tone: "bad", Show: index(r)})
			return
		}
		writeJSON(w, http.StatusOK, voices)
	})
	mux.HandleFunc("POST "+base+"api/{action}", func(w http.ResponseWriter, r *http.Request) {
		s.act(w, r, finish)
	})
	return guard(host, mux)
}

func guard(host string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(host)
	allowed := map[string]bool{host: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); r.Method != http.MethodGet && origin != "" && origin != "http://"+r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func index(r *http.Request) int {
	i, err := strconv.Atoi(r.URL.Query().Get("i"))
	if err != nil {
		return -1
	}
	return i
}

func (s *session) audio(w http.ResponseWriter, r *http.Request) {
	field := clipField(r.URL.Query().Get("clip"))
	path := ""
	s.mu.Lock()
	if e, err := s.entry(index(r)); err == nil && field != "" && s.opts.Clip != nil {
		path = s.opts.Clip(s.ns[e.Index], field)
	}
	s.mu.Unlock()
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	http.ServeFile(w, r, path)
}

func (s *session) act(w http.ResponseWriter, r *http.Request, finish func()) {
	i := index(r)
	var (
		res result
		err error
	)
	switch r.PathValue("action") {
	case "keep":
		res, err = s.keep(i)
	case "save":
		var n notes.Note
		if n, err = decode[notes.Note](http.MaxBytesReader(w, r.Body, 1<<20)); err == nil {
			res, err = s.save(i, n)
		} else {
			err = userErrorf("the note could not be read: %v", err)
		}
	case "ending":
		var body endingRequest
		if body, err = decode[endingRequest](http.MaxBytesReader(w, r.Body, 1<<10)); err == nil {
			res, err = s.ending(i, body.Source)
		} else {
			err = userErrorf("the request could not be read: %v", err)
		}
	case "remake", "remove":
		var body clipRequest
		if body, err = decode[clipRequest](http.MaxBytesReader(w, r.Body, 1<<10)); err != nil {
			err = userErrorf("the request could not be read: %v", err)
		} else if field := clipField(body.Clip); field == "" {
			err = userErrorf("there is no %q clip on a note", body.Clip)
		} else if r.PathValue("action") == "remake" {
			res, err = s.remake(i, field)
		} else {
			res, err = s.removeClip(i, field)
		}
	case "voice":
		var body voiceRequest
		if body, err = decode[voiceRequest](http.MaxBytesReader(w, r.Body, 1<<10)); err == nil {
			res, err = s.setVoice(i, body.Voice)
		} else {
			err = userErrorf("the request could not be read: %v", err)
		}
	case "use":
		res, err = s.use(i)
	case "drop":
		res, err = s.drop(i)
	case "ask":
		res, err = s.ask(i)
	case "stop":
		res, err = s.stop(i)
	case "undo":
		res, err = s.undo()
	case "finish":
		writeJSON(w, http.StatusOK, s.summary())
		finish()
		return
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		code := http.StatusInternalServerError
		if isUserError(err) {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, result{Message: err.Error(), Tone: "bad", Show: i})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type endingRequest struct {
	Source string `json:"source"`
}

type clipRequest struct {
	Clip string `json:"clip"`
}

type voiceRequest struct {
	Voice string `json:"voice"`
}

func decode[T any](r io.Reader) (T, error) {
	var v T
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}
