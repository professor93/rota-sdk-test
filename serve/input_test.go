package serve_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"rotatest/internal/fake"
)

/* ------------------------------------------------ reading a live stream --- */

// stream is one streaming response, read line by line on a goroutine.
//
// A test about a run that stays open has to go on making requests while the
// run is happening — that is what "open" means — so the reading cannot be the
// one read of the whole body every other test here does.
type stream struct {
	t      *testing.T
	cancel context.CancelFunc
	lines  chan string

	mu   sync.Mutex
	text strings.Builder
}

// open starts a streaming request and reads it in the background. Anything
// but 200 is the test's own mistake, so it fails here rather than in the
// reading.
func (s *server) open(method, path, body string, hdr ...string) *stream {
	s.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, method, s.url+path, r)
	if err != nil {
		cancel()
		s.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		s.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	st := &stream{t: s.t, cancel: cancel, lines: make(chan string, 4096)}
	go func() {
		defer close(st.lines)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			st.mu.Lock()
			st.text.WriteString(line + "\n")
			st.mu.Unlock()
			st.lines <- line
		}
	}()
	s.t.Cleanup(func() { cancel(); resp.Body.Close() })
	return st
}

// all is everything read so far, for an error that has to say what did
// arrive instead.
func (st *stream) all() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.text.String()
}

// ev waits for the next event. A heartbeat is not one: it carries no
// sequence number because it is not something the run did, and a test
// counting events must not read it as one.
func (st *stream) ev() map[string]any {
	st.t.Helper()
	for {
		select {
		case line, ok := <-st.lines:
			if !ok {
				st.t.Fatalf("the stream ended before the event came; so far:\n%s", st.all())
			}
			if doc := event(line); doc != nil {
				return doc
			}
		case <-time.After(20 * time.Second):
			st.t.Fatalf("nothing more arrived on the stream; so far:\n%s", st.all())
		}
	}
}

// waitFor reads until an event of this type arrives.
func (st *stream) waitFor(kind string) map[string]any {
	st.t.Helper()
	for {
		if ev := st.ev(); ev["type"] == kind {
			return ev
		}
	}
}

// rest is every event left until the stream closes.
func (st *stream) rest() []map[string]any {
	st.t.Helper()
	var out []map[string]any
	for {
		select {
		case line, ok := <-st.lines:
			if !ok {
				return out
			}
			if doc := event(line); doc != nil {
				out = append(out, doc)
			}
		case <-time.After(20 * time.Second):
			st.t.Fatalf("the stream never ended; so far:\n%s", st.all())
		}
	}
}

// event reads one NDJSON line as an event, and nothing at all for a
// heartbeat.
func event(line string) map[string]any {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var doc map[string]any
	if json.Unmarshal([]byte(line), &doc) != nil {
		return nil
	}
	if doc["type"] == "ping" {
		return nil
	}
	return doc
}

// call is one request to a run's endpoints: the status, and the document.
func (s *server) call(method, path, body string) (int, map[string]any) {
	s.t.Helper()
	resp, raw := s.do(method, path, body)
	var doc map[string]any
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			s.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode, doc
}

// startInput opens a run that stays open on the claude account and returns
// the stream it is being read on and the id everything else reaches it by.
func (s *server) startInput(prompt string) (*stream, string) {
	s.t.Helper()
	st := s.open("POST", "/v1/accounts/2/run",
		`{"prompt":"`+prompt+`","stream":true,"input":true}`, "Accept", "application/x-ndjson")
	init := st.ev()
	if init["type"] != "init" {
		s.t.Fatalf("a stream opens with rota's own event: %v", init)
	}
	id, _ := init["run_id"].(string)
	if id == "" {
		s.t.Fatalf("an open run is addressable, so its opening event says its id: %v", init)
	}
	// Nothing is left running after the test, whatever it proved.
	s.t.Cleanup(func() { s.do("POST", "/v1/runs/"+id+"/close", "") })
	return st, id
}

/* -------------------------------------------------------------- the tests --- */

// A run started with input goes on taking messages by its id: each is
// accepted and then answered in the same conversation, an interrupt is
// acknowledged, and the stream says what became of every one of them.
func TestRun_InputRunTakesMessagesByID(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: fake.Echo()})
	st, id := s.startInput("p")

	code, doc := s.call("POST", "/v1/runs/"+id+"/messages", `{"text":"more"}`)
	msg, _ := doc["id"].(string)
	if code != http.StatusAccepted || msg == "" || doc["state"] != "accepted" {
		t.Fatalf("a message reaches a running run: %d %v", code, doc)
	}
	code, doc = s.call("POST", "/v1/runs/"+id+"/interrupt", "")
	stop, _ := doc["id"].(string)
	if code != http.StatusAccepted || stop == "" {
		t.Fatalf("an interrupt is acknowledged by id: %d %v", code, doc)
	}
	code, doc = s.call("POST", "/v1/runs/"+id+"/close", "")
	if code != http.StatusOK || doc["ok"] != true {
		t.Fatalf("close says there is nothing more: %d %v", code, doc)
	}

	events := st.rest()
	var texts []string
	accepted, answered, interrupted, idle := false, false, false, false
	for _, ev := range events {
		switch ev["type"] {
		case "text":
			text, _ := ev["text"].(string)
			texts = append(texts, text)
		case "input":
			if ev["id"] != msg {
				t.Fatalf("the only message sent is the one reported: %v", ev)
			}
			switch ev["state"] {
			case "accepted":
				accepted = true
			case "answered":
				answered = true
			default:
				t.Fatalf("no message failed: %v", ev)
			}
		case "interrupted":
			interrupted = true
		case "idle":
			idle = true
		}
	}
	if !has(texts, "echo: p") || !has(texts, "echo: more") {
		t.Fatalf("the prompt and the message are both answered in the same run: %q", texts)
	}
	if !accepted || !answered {
		t.Fatalf("a message is accepted and then answered: %v", events)
	}
	if !interrupted || !idle {
		t.Fatalf("the interrupt is acknowledged and the run says when nothing is left waiting: %v", events)
	}
	if last := events[len(events)-1]; last["type"] != "done" {
		t.Fatalf("the stream ends by saying how the run ended: %v", last)
	}
}

func has(texts []string, want string) bool {
	for _, got := range texts {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}

// A run that stays open is a thing the server knows about: it can be asked
// about itself and is one of the runs listed, while it runs and after it has
// ended.
func TestRun_InputRunIsDescribedAndListed(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: fake.Echo()})
	st, id := s.startInput("p")

	code, doc := s.call("GET", "/v1/runs/"+id, "")
	if code != http.StatusOK || doc["id"] != id || doc["account"] != 2.0 || doc["provider"] != "claude" {
		t.Fatalf("a run says which account it is spending: %d %v", code, doc)
	}
	if doc["state"] != "running" && doc["state"] != "idle" {
		t.Fatalf("a run that is going is running or idle: %v", doc)
	}
	if doc["attached"] != true {
		t.Fatalf("somebody is reading it: %v", doc)
	}
	if _, ok := doc["pending"].(float64); !ok {
		t.Fatalf("pending is how many messages are waiting, which is a number: %v", doc)
	}

	code, doc = s.call("GET", "/v1/runs", "")
	runs, _ := doc["runs"].([]any)
	if code != http.StatusOK || !listed(runs, id) {
		t.Fatalf("a live run is listed: %d %v", code, doc)
	}

	if code, doc = s.call("POST", "/v1/runs/"+id+"/close", ""); code != http.StatusOK {
		t.Fatalf("close: %d %v", code, doc)
	}
	st.waitFor("done")

	code, doc = s.call("GET", "/v1/runs/"+id, "")
	if code != http.StatusOK || doc["state"] != "ended" {
		t.Fatalf("a run that is over says so rather than vanishing: %d %v", code, doc)
	}
	code, doc = s.call("GET", "/v1/runs", "")
	runs, _ = doc["runs"].([]any)
	if code != http.StatusOK || !listed(runs, id) {
		t.Fatalf("and is still listed for whoever reads the listing next: %d %v", code, doc)
	}
}

func listed(runs []any, id string) bool {
	for _, r := range runs {
		if row, ok := r.(map[string]any); ok && row["id"] == id {
			return true
		}
	}
	return false
}

// Every door into a run says no in the same words: an id nobody knows is a
// 404, a message with nothing in it is a 400, and a run that has ended takes
// nothing more.
func TestRun_InputRunRefusesWhatItCannot(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: fake.Echo()})

	for _, call := range [][2]string{
		{"POST", "/v1/runs/nosuchrun/messages"},
		{"POST", "/v1/runs/nosuchrun/interrupt"},
		{"POST", "/v1/runs/nosuchrun/close"},
		{"GET", "/v1/runs/nosuchrun/events"},
	} {
		code, doc := s.call(call[0], call[1], "")
		if code != http.StatusNotFound || !strings.Contains(str(doc["error"]), "no run nosuchrun") {
			t.Fatalf("%s %s: %d %v", call[0], call[1], code, doc)
		}
	}

	// input is a stream, and the refusal names the field to add rather than
	// leaving the caller to find out from a CLI's own complaint.
	code, _, raw := s.run(2, `{"prompt":"p","input":true}`)
	if code != http.StatusBadRequest || !strings.Contains(string(raw), "stream") {
		t.Fatalf("input needs a stream, said by name: %d %s", code, raw)
	}

	st, id := s.startInput("p")
	code, doc := s.call("POST", "/v1/runs/"+id+"/messages", `{"text":""}`)
	if code != http.StatusBadRequest {
		t.Fatalf("a message with no text is nothing to send: %d %v", code, doc)
	}

	if code, doc = s.call("POST", "/v1/runs/"+id+"/close", ""); code != http.StatusOK {
		t.Fatalf("close: %d %v", code, doc)
	}
	st.waitFor("done")
	code, doc = s.call("POST", "/v1/runs/"+id+"/messages", `{"text":"more"}`)
	if code != http.StatusConflict || !strings.Contains(str(doc["error"]), "has ended") {
		t.Fatalf("nothing is sent into a run that has ended: %d %v", code, doc)
	}
}

// str is a JSON field a test wants to read words out of.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// The connection is not the run. A reader that drops leaves the run going,
// and what it missed is replayed to it when it comes back.
func TestRun_InputRunReplaysOnReattach(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: fake.Echo()})
	st, id := s.startInput("p")
	st.cancel() // the reader goes away; the run keeps its grace

	code, doc := s.call("POST", "/v1/runs/"+id+"/messages", `{"text":"more"}`)
	msg, _ := doc["id"].(string)
	if code != http.StatusAccepted || msg == "" {
		t.Fatalf("the run is alive with nobody reading it: %d %v", code, doc)
	}

	// The opening event is the first of the stream, so since=1 asks for
	// everything after it.
	back := s.open("GET", "/v1/runs/"+id+"/events?since=1", "", "Accept", "application/x-ndjson")
	if code, doc = s.call("POST", "/v1/runs/"+id+"/close", ""); code != http.StatusOK {
		t.Fatalf("close: %d %v", code, doc)
	}

	events := back.rest()
	var texts []string
	accepted, answered := false, false
	for _, ev := range events {
		if seq, ok := ev["seq"].(float64); ok && seq <= 1 {
			t.Fatalf("since asks for what came after it: %v", ev)
		}
		if said, ok := ev["text"].(string); ok {
			texts = append(texts, said)
		}
		if ev["type"] == "input" && ev["id"] == msg {
			accepted = accepted || ev["state"] == "accepted"
			answered = answered || ev["state"] == "answered"
		}
	}
	if !has(texts, "echo: p") {
		t.Fatalf("what the first reader had is replayed to the next: %q\n%s", texts, back.all())
	}
	if !has(texts, "echo: more") || !accepted || !answered {
		t.Fatalf("and the run goes on from there: %q accepted %v answered %v", texts, accepted, answered)
	}
	if last := events[len(events)-1]; last["type"] != "done" {
		t.Fatalf("a reattached stream ends the same way: %v", last)
	}
}
