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

	"github.com/scuba-plaza/arabic-vocab/internal/deck"
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
	remake    func(notes.Note, string) (*notes.Issue, error)
	removed   []string
	removeErr error
	voice     string
	voiceErr  error
	listErr   error
}

func newHarness(t *testing.T) *harness {
	return newFilteredHarness(t, Filter{Minor: true})
}

func newFilteredHarness(t *testing.T, filter Filter) *harness {
	return newHarnessWith(t, filter, nil)
}

func newHarnessWith(t *testing.T, filter Filter, arrange func(*fixture)) *harness {
	h := &harness{t: t, f: newFixture()}
	if arrange != nil {
		arrange(&h.f)
	}
	items, _ := h.f.items(filter)
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
		Remake: func(_ context.Context, n notes.Note, field string) (*notes.Issue, error) {
			if h.remake == nil {
				return nil, nil
			}
			return h.remake(n, field)
		},
		Remove: func(n notes.Note, field string) error {
			h.removed = append(h.removed, n.ID+" "+field)
			return h.removeErr
		},
		Voice: "ar-XA-Chirp3-HD-Kore",
		Voices: func(context.Context) ([]VoiceOption, error) {
			if h.listErr != nil {
				return nil, h.listErr
			}
			return []VoiceOption{
				{Name: "ar-XA-Chirp3-HD-Kore", Tier: "Chirp3-HD", Gender: "Female"},
				{Name: "ar-XA-Wavenet-B", Tier: "Wavenet", Gender: "Male"},
			}, nil
		},
		SetVoice: func(name string) error {
			if h.voiceErr != nil {
				return h.voiceErr
			}
			h.voice = name
			return nil
		},
		All:      filter.All,
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
	if _, res = h.post("keep", 2, nil); res.Show != 1 || !slices.Equal(h.f.ns[3].Reviewed, []string{"الْبَابَ"}) {
		t.Fatalf("keep: %+v, reviewed %q", res, h.f.ns[3].Reviewed)
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
	if want := []clipView{{Kind: "word"}, {Kind: "sentence", Ready: true}}; !slices.Equal(h.state(2).Entry.Clips, want) {
		t.Errorf("clips %+v", h.state(2).Entry.Clips)
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

func TestClipsAreRemadeAndRemoved(t *testing.T) {
	h := newHarness(t)
	if !h.state(2).Audio {
		t.Fatal("the page should offer the audio buttons")
	}
	var remade []string
	h.remake = func(n notes.Note, field string) (*notes.Issue, error) {
		remade = append(remade, n.ID+" "+field)
		return nil, nil
	}
	code, res := h.post("remake", 2, clipRequest{Clip: "sentence"})
	if code != http.StatusOK || res.Show != 2 || !strings.Contains(res.Message, "Made the sentence audio of بَاب again; listen to it") {
		t.Fatalf("remake: %d %+v", code, res)
	}
	if !slices.Equal(remade, []string{"بَاب ExampleAudio"}) {
		t.Errorf("remade %q", remade)
	}
	if code, res = h.post("remake", 0, clipRequest{Clip: "word"}); code != http.StatusOK || !strings.Contains(res.Message, "word audio") {
		t.Errorf("remaking a word clip: %d %+v", code, res)
	}
	h.remake = func(notes.Note, string) (*notes.Issue, error) { return nil, errors.New("quota exceeded") }
	if code, res = h.post("remake", 2, clipRequest{Clip: "sentence"}); code != http.StatusInternalServerError || !strings.Contains(res.Message, "quota exceeded") {
		t.Errorf("a failing remake: %d %+v", code, res)
	}
	if code, res = h.post("remake", 2, clipRequest{Clip: "nothing"}); code != http.StatusBadRequest || !strings.Contains(res.Message, `no "nothing" clip`) {
		t.Errorf("an unknown clip: %d %+v", code, res)
	}
	if code, res = h.post("remove", 2, clipRequest{Clip: "sentence"}); code != http.StatusOK || !strings.Contains(res.Message, "Removed the sentence audio") {
		t.Fatalf("remove: %d %+v", code, res)
	}
	if !slices.Equal(h.removed, []string{"بَاب ExampleAudio"}) {
		t.Errorf("removed %q", h.removed)
	}
	h.removeErr = errors.New("permission denied")
	if code, res = h.post("remove", 2, clipRequest{Clip: "word"}); code != http.StatusInternalServerError || !strings.Contains(res.Message, "permission denied") {
		t.Errorf("a failing remove: %d %+v", code, res)
	}
}

func TestTheVoiceIsChosenOnceForEveryNewClip(t *testing.T) {
	h := newHarness(t)
	if v := h.state(0); v.Voice != "ar-XA-Chirp3-HD-Kore" {
		t.Fatalf("voice = %q", v.Voice)
	}
	w := h.do("GET", "api/voices?i=0", nil, nil)
	var list voicesView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK {
		t.Fatalf("voices: %d %s", w.Code, w.Body)
	}
	if list.Voice != "ar-XA-Chirp3-HD-Kore" || len(list.Voices) != 2 || list.Voices[1].Gender != "Male" {
		t.Fatalf("voices %+v", list)
	}
	code, res := h.post("voice", 0, voiceRequest{Voice: "ar-XA-Wavenet-B"})
	if code != http.StatusOK || res.Show != 0 || !strings.Contains(res.Message, "ar-XA-Wavenet-B speaks every clip") {
		t.Fatalf("voice: %d %+v", code, res)
	}
	if h.voice != "ar-XA-Wavenet-B" || h.state(0).Voice != "ar-XA-Wavenet-B" {
		t.Fatalf("the choice should be kept: %q", h.voice)
	}
	h.voice = ""
	if _, res = h.post("voice", 0, voiceRequest{Voice: "ar-XA-Wavenet-B"}); res.Message != "" || h.voice != "" {
		t.Errorf("the same voice again should change nothing: %+v", res)
	}
	if code, res = h.post("voice", 0, voiceRequest{Voice: ""}); code != http.StatusBadRequest || !strings.Contains(res.Message, "no voice") {
		t.Errorf("an empty voice: %d %+v", code, res)
	}
	h.voiceErr = errors.New("deck.json is read-only")
	if code, res = h.post("voice", 0, voiceRequest{Voice: "ar-XA-Standard-A"}); code != http.StatusInternalServerError || !strings.Contains(res.Message, "read-only") {
		t.Errorf("a failing save: %d %+v", code, res)
	}
	if h.state(0).Voice != "ar-XA-Wavenet-B" {
		t.Error("a failed save should leave the voice alone")
	}
	h.listErr = errors.New("no credentials found")
	h2 := newHarness(t)
	h2.listErr = h.listErr
	if w = h2.do("GET", "api/voices?i=0", nil, nil); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no credentials found") {
		t.Errorf("a failing listing: %d %s", w.Code, w.Body)
	}
}

func TestWithAllEveryNoteIsListed(t *testing.T) {
	h := newFilteredHarness(t, Filter{Minor: true, All: true})
	v := h.state(4)
	if !v.All || len(v.List) != 5 || v.List[4].Flags != 0 || v.List[4].Tone != "plain" {
		t.Fatalf("list %+v", v.List)
	}
	if e := v.Entry; len(e.Flags) != 0 || e.Stale {
		t.Fatalf("an unflagged note: %+v", e)
	}
	if !h.state(2).Entry.Stale {
		t.Error("a note that changed after the check should say so")
	}
	code, res := h.post("keep", 4, nil)
	if code != http.StatusOK || h.saves != 0 || strings.Contains(res.Message, "flags") {
		t.Fatalf("keeping a note without flags: %d %+v, saves %d", code, res, h.saves)
	}
	if st := h.state(4).Entry.State; st != "kept" {
		t.Errorf("state = %s", st)
	}
	if _, res = h.post("undo", -1, nil); res.Show != 4 || h.state(4).Entry.State != "open" {
		t.Errorf("undo: %+v", res)
	}
}

func spanText(list []span) string {
	var b strings.Builder
	for _, s := range list {
		b.WriteString(s.T)
	}
	return b.String()
}

func TestServeRunsUntilFinished(t *testing.T) {
	f := newFixture()
	items, _ := f.items(Filter{Minor: true})
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

func withSilentClips(f *fixture) {
	f.audio = []notes.Check{audioCheckFor(f.ns[4], silent("ExampleAudio"), silent("WordAudio"))}
}

func flagTitles(v entryView) []string {
	var out []string
	for _, f := range v.Flags {
		out = append(out, f.Title+" / "+f.Where)
	}
	return out
}

func TestClipWithoutSoundIsFlaggedLikeAnyOtherProblem(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, withSilentClips)
	v := h.state(3)
	if len(v.List) != 4 || v.List[3].Arabic != "عَشَرَة" || v.List[3].Tone != "bad" || v.List[3].Flags != 2 {
		t.Fatalf("list %+v", v.List)
	}
	got := flagTitles(*v.Entry)
	slices.Sort(got)
	if want := []string{"The clip has no sound / sentence · major", "The clip has no sound / word · major"}; !slices.Equal(got, want) {
		t.Fatalf("flags %v, want %v", got, want)
	}
	var sentence flagView
	for _, f := range v.Entry.Flags {
		if f.Where == "sentence · major" {
			sentence = f
		}
	}
	if sentence.Tone != "bad" || sentence.Symbol != "✗" || !strings.Contains(sentence.Explain, "came back silent in all 4 attempts") || !strings.Contains(sentence.Explain, "Remake") {
		t.Errorf("flag %+v", sentence)
	}
	if len(sentence.Rows) != 0 {
		t.Errorf("there is nothing to compare for a clip: %+v", sentence.Rows)
	}
}

func TestRemakingAClipClearsOrRenewsItsFlag(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, withSilentClips)
	h.remake = func(n notes.Note, field string) (*notes.Issue, error) { return nil, nil }
	code, res := h.post("remake", 3, clipRequest{Clip: "sentence"})
	if code != http.StatusOK || res.Tone != "audio" || !strings.Contains(res.Message, "listen to it") {
		t.Fatalf("a good remake: %d %+v", code, res)
	}
	v := h.state(3)
	if len(v.Entry.Flags) != 1 || v.List[3].Flags != 1 || !strings.Contains(v.Entry.Flags[0].Where, "word") {
		t.Fatalf("only the sentence flag should have cleared: %v", flagTitles(*v.Entry))
	}

	again := deck.SilentIssue("ExampleAudio", 4)
	h.remake = func(n notes.Note, field string) (*notes.Issue, error) { return &again, nil }
	code, res = h.post("remake", 3, clipRequest{Clip: "sentence"})
	if code != http.StatusOK || res.Tone != "bad" || !strings.Contains(res.Message, "came back without any sound") {
		t.Fatalf("a remake that is still silent: %d %+v", code, res)
	}
	if v = h.state(3); len(v.Entry.Flags) != 2 || v.List[3].Flags != 2 {
		t.Errorf("the flag should be back, once: %v", flagTitles(*v.Entry))
	}
	if code, _ = h.post("remake", 3, clipRequest{Clip: "sentence"}); code != http.StatusOK {
		t.Fatal(code)
	}
	if v = h.state(3); len(v.Entry.Flags) != 2 {
		t.Errorf("a repeated failure must not pile up flags: %v", flagTitles(*v.Entry))
	}
}

func TestARemakeThatFailsOutrightLeavesTheFlagsAlone(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, withSilentClips)
	h.remake = func(n notes.Note, field string) (*notes.Issue, error) { return nil, errors.New("quota exceeded") }
	if code, res := h.post("remake", 3, clipRequest{Clip: "sentence"}); code != http.StatusInternalServerError || !strings.Contains(res.Message, "quota exceeded") {
		t.Fatalf("%d %+v", code, res)
	}
	if v := h.state(3); len(v.Entry.Flags) != 2 {
		t.Errorf("flags = %v", flagTitles(*v.Entry))
	}
}

func TestRemovingAClipClearsItsFlag(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, withSilentClips)
	if code, res := h.post("remove", 3, clipRequest{Clip: "word"}); code != http.StatusOK {
		t.Fatalf("%d %+v", code, res)
	}
	v := h.state(3)
	if len(v.Entry.Flags) != 1 || !strings.Contains(v.Entry.Flags[0].Where, "sentence") {
		t.Errorf("flags = %v", flagTitles(*v.Entry))
	}
}

func TestKeepingANoteNeverClosesItsClipFlags(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, func(f *fixture) {
		f.audio = []notes.Check{audioCheckFor(f.ns[3], silent("ExampleAudio"), silent("WordAudio"))}
	})
	code, res := h.post("keep", 2, nil)
	if code != http.StatusOK || !strings.Contains(res.Message, "its other flags stay out of the next build, but a clip without sound stays flagged until it is made again") {
		t.Fatalf("keep: %d %+v", code, res)
	}
	if got := h.f.ns[3].Reviewed; !slices.Equal(got, []string{"الْبَابَ"}) {
		t.Errorf("a silent clip must not be written into the note's reviewed flags, got %q", got)
	}
	if _, res = h.post("undo", -1, nil); res.Show != 2 || h.state(2).Entry.State != "open" || len(h.f.ns[3].Reviewed) != 0 {
		t.Errorf("undo: %+v, reviewed %q", res, h.f.ns[3].Reviewed)
	}
}

func withOnlyASilentClip(f *fixture) {
	f.audio = []notes.Check{audioCheckFor(f.ns[2], silent("WordAudio"))}
}

func TestKeepingANoteWhoseOnlyFlagIsASilentClipStoresNothing(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, withOnlyASilentClip)
	v := h.state(2)
	if v.Entry.Note.Arabic != "قَدِيم" || len(v.Entry.Flags) != 1 {
		t.Fatalf("entry %+v", v.Entry)
	}
	code, res := h.post("keep", 2, nil)
	if code != http.StatusOK || !strings.Contains(res.Message, "a clip without sound stays flagged until it is made again, so Remake it or edit the text") || strings.Contains(res.Message, "stay out of the next build") {
		t.Fatalf("keep: %d %+v", code, res)
	}
	if got := h.f.ns[2].Reviewed; len(got) != 0 {
		t.Errorf("reviewed = %q", got)
	}
	if h.saves != 0 {
		t.Errorf("there is nothing to store about this note, got %d saves", h.saves)
	}
	if h.state(2).Entry.State != "kept" {
		t.Errorf("the note leaves this session's queue: %s", h.state(2).Entry.State)
	}
	items, _ := h.f.items(Filter{Minor: true})
	found := false
	for _, it := range items {
		if h.f.ns[it.Index].ID == "قَدِيم" {
			found = true
		}
	}
	if !found {
		t.Error("the next review must show the clip again")
	}
}

func TestSilentFlagsStayInTheQueueEvenWhenTheNoteSaysItWasReviewed(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, func(f *fixture) {
		f.ns[2].Reviewed = []string{"WordAudio:silent"}
		f.audio = []notes.Check{audioCheckFor(f.ns[2], silent("WordAudio"))}
	})
	v := h.state(2)
	if v.Entry.Note.Arabic != "قَدِيم" || len(v.Entry.Flags) != 1 || !strings.Contains(v.Entry.Flags[0].Title, "no sound") {
		t.Errorf("entry %+v", v.Entry)
	}
}

func TestRemakingASharedClipRefreshesEveryNoteThatPlaysIt(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, func(f *fixture) {
		f.audio = []notes.Check{audioCheckFor(f.ns[4], silent("ExampleAudio")), audioCheckFor(f.ns[3], silent("ExampleAudio"))}
	})
	other := h.state(2).Entry
	if other.Note.Arabic != "بَاب" || len(other.Flags) != 2 {
		t.Fatalf("the other note should have its vowel flag and the clip: %+v", other)
	}
	h.remake = func(n notes.Note, field string) (*notes.Issue, error) { return nil, nil }
	h.s.opts.Flags = func(ns []notes.Note) map[string][]notes.Issue { return map[string][]notes.Issue{} }
	if code, res := h.post("remake", 3, clipRequest{Clip: "sentence"}); code != http.StatusOK {
		t.Fatalf("%d %+v", code, res)
	}
	if v := h.state(2); len(v.Entry.Flags) != 1 || strings.Contains(v.Entry.Flags[0].Title, "no sound") || v.List[2].Flags != 1 {
		t.Errorf("the clip was made again for every note that plays it: %v", flagTitles(*v.Entry))
	}
	if v := h.state(3); len(v.Entry.Flags) != 0 {
		t.Errorf("flags = %v", flagTitles(*v.Entry))
	}

	again := deck.SilentIssue("ExampleAudio", 4)
	h.remake = func(n notes.Note, field string) (*notes.Issue, error) { return &again, nil }
	h.s.opts.Flags = func(ns []notes.Note) map[string][]notes.Issue {
		return map[string][]notes.Issue{"بَاب": {deck.SilentIssue("ExampleAudio", 4)}, "عَشَرَة": {again}}
	}
	if code, _ := h.post("remake", 3, clipRequest{Clip: "sentence"}); code != http.StatusOK {
		t.Fatal(code)
	}
	if v := h.state(2); len(v.Entry.Flags) != 2 {
		t.Errorf("a clip that stays silent flags every note that plays it: %v", flagTitles(*v.Entry))
	}
	if v := h.state(3); len(v.Entry.Flags) != 1 {
		t.Errorf("flags = %v", flagTitles(*v.Entry))
	}
}

func TestRemovingASharedClipRefreshesEveryNoteThatPlaysIt(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, func(f *fixture) {
		f.audio = []notes.Check{audioCheckFor(f.ns[4], silent("ExampleAudio")), audioCheckFor(f.ns[3], silent("ExampleAudio"))}
	})
	h.s.opts.Flags = func(ns []notes.Note) map[string][]notes.Issue { return map[string][]notes.Issue{} }
	if code, res := h.post("remove", 3, clipRequest{Clip: "sentence"}); code != http.StatusOK {
		t.Fatalf("%d %+v", code, res)
	}
	if v := h.state(2); len(v.Entry.Flags) != 1 || strings.Contains(v.Entry.Flags[0].Title, "no sound") {
		t.Errorf("flags = %v", flagTitles(*v.Entry))
	}
}

func TestClaudeIsNotAskedToFixAClipFlag(t *testing.T) {
	h := newHarnessWith(t, Filter{Minor: true}, withSilentClips)
	h.rewrite = func(n notes.Note) (notes.Note, error) { return n, nil }
	if code, _ := h.post("ask", 3, nil); code != http.StatusOK {
		t.Fatal(code)
	}
	h.settle(3)
	if len(h.feedbacks) != 1 || strings.Contains(h.feedbacks[0], "Audio") || !strings.Contains(h.feedbacks[0], "Nothing was flagged") {
		t.Errorf("feedback = %q", h.feedbacks)
	}
}
