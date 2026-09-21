package serve_test

import (
	"strconv"
	"strings"
	"testing"

	"rotatest/internal/fake"
)

// askingClaude answers with a fenced command and a question with two
// choices: something for both readings to find.
func askingClaude() string {
	answer := "Run:\n```sh\nls\n```\nWhich one?\n- keep\n- drop"
	return fake.Lines(
		`{"type":"system","subtype":"init","session_id":"s-w"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":`+strconv.Quote(answer)+`}]},"session_id":"s-w"}`,
		`{"type":"result","subtype":"success","is_error":false,"session_id":"s-w","result":`+strconv.Quote(answer)+`,"num_turns":1}`,
	)
}

// The reply is the answer as the CLI gave it, and nothing read out of it
// unless "with" names a reading.
func TestRun_ReplyIsTheAnswerAloneUnlessAsked(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: askingClaude()})
	code, _, raw := s.run(2, `{"prompt":"p"}`)
	if code != 200 || strings.Contains(string(raw), `"blocks"`) || strings.Contains(string(raw), `"ask"`) {
		t.Fatalf("unasked: %d %s", code, raw)
	}
}

func TestRun_WithNamesTheReadings(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: askingClaude()})
	for _, with := range []string{`["blocks","ask"]`, `["blocks,ask"]`, `["ask","blocks"]`} {
		code, _, raw := s.run(2, `{"prompt":"p","with":`+with+`}`)
		if code != 200 || !strings.Contains(string(raw), `"lang":"sh"`) || !strings.Contains(string(raw), `"kind":"choice"`) {
			t.Fatalf("with %s: both readings: %d %s", with, code, raw)
		}
	}
	code, _, raw := s.run(2, `{"prompt":"p","with":["ask"]}`)
	if code != 200 || strings.Contains(string(raw), `"blocks"`) || !strings.Contains(string(raw), `"question":"Which one?"`) {
		t.Fatalf("one reading, not the other: %d %s", code, raw)
	}
}

func TestRun_UnknownReadingIs400(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: askingClaude()})
	code, _, raw := s.run(2, `{"prompt":"p","with":["blocks","foo"]}`)
	if code != 400 || !strings.Contains(string(raw), "foo") || !strings.Contains(string(raw), "blocks, ask") {
		t.Fatalf("refused by name, naming the known: %d %s", code, raw)
	}
}

func TestRun_StreamedTextCarriesBlocksOnlyWithWith(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: askingClaude()})
	_, raw := s.do("POST", "/v1/accounts/2/run", `{"prompt":"p","stream":true}`, "Accept", "application/x-ndjson")
	if strings.Contains(string(raw), `"blocks"`) {
		t.Fatalf("unasked stream: %s", raw)
	}
	_, raw = s.do("POST", "/v1/accounts/2/run", `{"prompt":"p","stream":true,"with":["blocks"]}`, "Accept", "application/x-ndjson")
	if strings.Count(string(raw), `"blocks"`) != 1 || !strings.Contains(string(raw), `"lang":"sh"`) {
		t.Fatalf("asked, once per whole text event: %s", raw)
	}
}

// A failed run keeps its answer empty: the reason is in stderr, where the
// CLI put it, and result is the answer or nothing.
func TestRun_FailedRunKeepsResultEmpty(t *testing.T) {
	since(t, "1.1.0")
	s := start(t, opts{claude: "cat >/dev/null\necho fake-stderr >&2\nexit 2\n"})
	code, out, raw := s.run(2, `{"prompt":"p"}`)
	if code != 502 || !out.IsError || out.ExitCode != 2 || out.Result != "" || out.Stderr != "fake-stderr" {
		t.Fatalf("%d %s", code, raw)
	}
}
