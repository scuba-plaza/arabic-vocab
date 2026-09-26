package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
)

type harness struct {
	t         *testing.T
	f         fixture
	s         *session
	h         http.Handler
	saves     int
	saveErr   error
	finished  bool
	mu        sync.Mutex
	feedbacks []string
	rewrite   func(notes.Note) (notes.Note, error)
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, f: newFixture()}
	items, _ := h.f.items(true)
	dir := t.TempDir()
	clip := filepath.Join(dir, "ar-bab.mp3")
	font := filepath.Join(dir, "font.ttf")
	os.WriteFile(clip, []byte("ID3 clip"), 0o644)
	os.WriteFile(font, []byte("font"), 0o644)
	opts := Options{
		Save: func([]notes.Note) error {
			if h.saveErr != nil {
				return h.saveErr
			}
			h.saves++
			return nil
		},
		Rewrite: func(_ context.Context, n notes.Note, feedback string) (notes.Note, error) {
			h.mu.Lock()
			h.feedbacks = append(h.feedbacks, feedback)
			h.mu.Unlock()
			return h.rewrite(n)
		},
		Clip: func(n notes.Note, field string) string {
			if n.ID == "بَاب" && field == "ExampleAudio" {
				return clip
			}
			return ""
		},
		FontPath: font,
	}
	h.s = newSession(context.Background(), h.f.ns, items, opts)
	h.h = h.s.handler("tok", "127.0.0.1:9999", func() { h.finished = true })
	return h
}

func (h *harness) do(method, path string, body io.Reader, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:9999/tok/"+path, body)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, req)
	return w
}

func (h *harness) state(i int) stateView {
	h.t.Helper()
	w := h.do("GET", fmt.Sprintf("api/state?i=%d", i), nil, nil)
	if w.Code != http.StatusOK {
		h.t.Fatalf("state: %d %s", w.Code, w.Body)
	}
	var v stateView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *harness) post(action string, i int, body any) (int, result) {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	w := h.do("POST", fmt.Sprintf("api/%s?i=%d", action, i), r, map[string]string{"Content-Type": "application/json"})
	var res result
	json.Unmarshal(w.Body.Bytes(), &res)
	return w.Code, res
}

func (h *harness) settle(i int) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.s.mu.Lock()
		asking := h.s.entries[i].asking
		h.s.mu.Unlock()
		if !asking {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("Claude Code never answered")
}

func classes(spans []span) []string {
	var out []string
	for _, s := range spans {
		if s.C != "" {
			out = append(out, s.C)
		}
	}
	return out
}

func TestKeepMarksTheCardRightAndMovesOn(t *testing.T) {
	h := newHarness(t)
	code, res := h.post("keep", 0, nil)
	if code != http.StatusOK || res.Show != 1 || !strings.Contains(res.Message, "is right as it is") {
		t.Fatalf("keep: %d %+v", code, res)
	}
	if !slices.Equal(h.f.ns[0].Reviewed, []string{"وَجَدْتُ", "الْمَعْلُومَاتِ"}) || h.saves != 1 {
		t.Fatalf("reviewed %q after %d saves", h.f.ns[0].Reviewed, h.saves)
	}
	if _, res = h.post("keep", 2, nil); res.Show != 1 || !slices.Equal(h.f.ns[3].ReviewedAudio, []string{"ar-bab.mp3"}) {
		t.Fatalf("audio keep: %+v, reviewed audio %q", res, h.f.ns[3].ReviewedAudio)
	}
	if _, res = h.post("keep", 1, nil); res.Show != -1 {
		t.Fatalf("the last open note should lead to the summary, got %+v", res)
	}
	v := h.state(-1)
	if v.Entry != nil || v.Summary.Kept != 3 || v.Summary.Open != 0 || !strings.Contains(v.Next, "build") {
		t.Fatalf("summary view %+v", v)
	}
	if _, res = h.post("keep", 0, nil); res.Show != -1 || h.saves != 3 {
		t.Errorf("enter on a decided note only moves on: %+v, saves %d", res, h.saves)
	}
}

func TestUndoRestoresTheNote(t *testing.T) {
	h := newHarness(t)
	h.post("keep", 0, nil)
	code, res := h.post("undo", -1, nil)
	if code != http.StatusOK || res.Show != 0 || h.f.ns[0].Reviewed != nil || h.saves != 2 {
		t.Fatalf("undo: %d %+v, reviewed %q, saves %d", code, res, h.f.ns[0].Reviewed, h.saves)
	}
	if st := h.state(0).Entry.State; st != "open" {
		t.Errorf("state after undo = %s", st)
	}
	if code, res = h.post("undo", -1, nil); code != http.StatusBadRequest || !strings.Contains(res.Message, "nothing to undo") {
		t.Errorf("second undo: %d %+v", code, res)
	}
}

func TestSaveValidatesAndStoresEdits(t *testing.T) {
	h := newHarness(t)
	n := h.state(0).Entry.Note
	n.ID = "other"
	if code, res := h.post("save", 0, n); code != http.StatusBadRequest || !strings.Contains(res.Message, "id cannot change") {
		t.Fatalf("id change: %d %+v", code, res)
	}
	w := h.do("POST", "api/save?i=0", strings.NewReader(`{"id":"مَوْقِع","colour":"red"}`), nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatalf("unknown field: %d %s", w.Code, w.Body)
	}
	n = h.state(0).Entry.Note
	if _, res := h.post("save", 0, n); res.Message != "No changes" || h.saves != 0 {
		t.Fatalf("unchanged save: %+v, saves %d", res, h.saves)
	}
	n.Example = "<b>وَجَدْتُ</b> الْجَوَابَ."
	code, res := h.post("save", 0, n)
	if code != http.StatusOK || res.Show != 1 || h.f.ns[0].Example != n.Example || h.saves != 1 {
		t.Fatalf("save: %d %+v, example %q, saves %d", code, res, h.f.ns[0].Example, h.saves)
	}
	if st := h.state(0).Entry.State; st != "edited" {
		t.Errorf("state = %s", st)
	}
}

func TestClaudeCodeSuggestionsCanBeKeptOrDropped(t *testing.T) {
	h := newHarness(t)
	h.rewrite = func(n notes.Note) (notes.Note, error) {
		if n.ID == "كَمْ" {
			return n, errors.New("usage limit reached")
		}
		n.Example = "<b>وَجَدَ</b> الطَّالِبُ الْمَعْلُومَاتِ فِي الْمَوْقِعِ."
		n.ExampleEn = "The student found the information on the website."
		return n, nil
	}
	if code, _ := h.post("ask", 0, nil); code != http.StatusOK {
		t.Fatalf("ask: %d", code)
	}
	h.settle(0)
	e := h.state(0).Entry
	if e.Proposal == nil || len(e.Changes) != 2 || e.Changes[0].Label != "sentence" || e.Changes[1].Label != "translation" {
		t.Fatalf("proposal %+v, changes %+v", e.Proposal, e.Changes)
	}
	if !slices.Contains(classes(e.Changes[0].New), "ins target") || !slices.Contains(classes(e.Changes[0].Old), "del target") {
		t.Errorf("sentence diff classes: old %q new %q", classes(e.Changes[0].Old), classes(e.Changes[0].New))
	}
	if !strings.Contains(h.feedbacks[0], "وَجَدْتُ in the example: CATT reads وُجِدَتْ") {
		t.Errorf("feedback = %q", h.feedbacks[0])
	}
	h.post("drop", 0, nil)
	if h.state(0).Entry.Proposal != nil || h.saves != 0 {
		t.Fatal("drop should forget the suggestion without saving")
	}
	h.post("ask", 0, nil)
	h.settle(0)
	code, res := h.post("use", 0, nil)
	if code != http.StatusOK || res.Show != 1 || !strings.HasPrefix(h.f.ns[0].Example, "<b>وَجَدَ</b>") || h.saves != 1 {
		t.Fatalf("use: %d %+v, example %q, saves %d", code, res, h.f.ns[0].Example, h.saves)
	}
	if st := h.state(0).Entry.State; st != "rewritten" {
		t.Errorf("state = %s", st)
	}
	if code, _ := h.post("ask", 0, nil); code != http.StatusBadRequest {
		t.Errorf("asking about a rewritten note should be refused, got %d", code)
	}
	h.post("ask", 1, nil)
	h.settle(1)
	v := h.state(1)
	if !strings.Contains(v.Entry.Notice, "usage limit reached") || v.List[1].NoticeTone != "bad" {
		t.Errorf("failure notice: %q / %+v", v.Entry.Notice, v.List[1])
	}
}

func TestStopIgnoresALateAnswer(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	done := make(chan struct{})
	h.rewrite = func(n notes.Note) (notes.Note, error) {
		<-release
		defer close(done)
		n.English = "late"
		return n, nil
	}
	h.post("ask", 0, nil)
	if !h.state(0).List[0].Asking {
		t.Fatal("the note should show that Claude Code is working")
	}
	if _, res := h.post("stop", 0, nil); !strings.Contains(res.Message, "Stopped asking") {
		t.Fatalf("stop: %+v", res)
	}
	close(release)
	<-done
	time.Sleep(20 * time.Millisecond)
	if e := h.state(0).Entry; e.Asking || e.Proposal != nil {
		t.Fatalf("a late answer should be ignored: %+v", e)
	}
}

func TestFlagsAreExplainedWithHighlights(t *testing.T) {
	h := newHarness(t)
	e := h.state(0).Entry
	f := e.Flags[0]
	if f.Title != "CATT and CAMeL both read this word with other vowels" || f.Where != "sentence · major" || f.Tone != "bad" {
		t.Fatalf("flag %+v", f)
	}
	if len(f.Rows) != 3 || f.Rows[0].Label != "card" || !slices.Contains(classes(f.Rows[1].Spans), "mark-bad") {
		t.Fatalf("rows %+v", f.Rows)
	}
	if minor := e.Flags[1]; minor.Rows[2].Note != "agrees with the card" || minor.Tone != "warn" {
		t.Errorf("minor flag %+v", minor)
	}
	if got := classes(e.Card.Example); !slices.Contains(got, "flag-major target") || !slices.Contains(got, "flag-minor") {
		t.Errorf("example classes %q", got)
	}
	audio := h.state(2).Entry
	if !audio.Audio.Sentence || audio.Audio.Word {
		t.Errorf("audio %+v", audio.Audio)
	}
	heard := audio.Flags[0].Rows[1]
	if heard.Label != "heard" || !slices.Equal(classes(heard.Spans), []string{"mark-audio"}) || !strings.Contains(heard.Spans[len(heard.Spans)-1].T, "الان") {
		t.Errorf("heard row %+v", heard)
	}
}

func TestSaveFailureChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.saveErr = errors.New("disk full")
	code, res := h.post("keep", 0, nil)
	if code != http.StatusInternalServerError || !strings.Contains(res.Message, "disk full") || h.f.ns[0].Reviewed != nil {
		t.Fatalf("keep with a failing save: %d %+v, reviewed %q", code, res, h.f.ns[0].Reviewed)
	}
	if st := h.state(0).Entry.State; st != "open" {
		t.Errorf("state = %s", st)
	}
}

func TestServerOnlyAnswersItsOwnPage(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("GET", "http://evil.example/tok/api/state?i=0", nil)
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("foreign host: %d", w.Code)
	}
	if w := h.do("POST", "api/keep?i=0", nil, map[string]string{"Origin": "http://evil.example"}); w.Code != http.StatusForbidden || h.saves != 0 {
		t.Errorf("foreign origin: %d, saves %d", w.Code, h.saves)
	}
	req = httptest.NewRequest("GET", "http://127.0.0.1:9999/wrong/api/state?i=0", nil)
	w = httptest.NewRecorder()
	h.h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("wrong token: %d", w.Code)
	}
	page := h.do("GET", "", nil, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "app.js") || page.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("page: %d %v", page.Code, page.Header())
	}
	for path, want := range map[string]int{
		"app.js": http.StatusOK, "app.css": http.StatusOK, "font.ttf": http.StatusOK,
		"audio?i=2&clip=sentence": http.StatusOK, "audio?i=0&clip=sentence": http.StatusNotFound, "audio?i=2&clip=../x": http.StatusNotFound,
	} {
		if w := h.do("GET", path, nil, nil); w.Code != want {
			t.Errorf("%s: %d, want %d", path, w.Code, want)
		}
	}
}

func TestServeRunsUntilFinished(t *testing.T) {
	f := newFixture()
	items, _ := f.items(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	urls := make(chan string, 1)
	type outcome struct {
		sum Summary
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		sum, err := Serve(ctx, f.ns, items, Options{Save: func([]notes.Note) error { return nil }}, func(url string) { urls <- url })
		done <- outcome{sum, err}
	}()
	url := <-urls
	res, err := http.Post(url+"api/keep?i=0", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res, err = http.Post(url+"api/finish", "application/json", nil); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	out := <-done
	if out.err != nil || out.sum.Kept != 1 || out.sum.Open != 2 {
		t.Fatalf("Serve returned %+v, %v", out.sum, out.err)
	}
}
