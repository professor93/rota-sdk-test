package serve_test

import (
	"encoding/json"
	"strings"
	"testing"

	"rotatest/internal/fake"
)

// partialClaude prints what claude prints with --include-partial-messages:
// each fragment as it is written, the whole piece those made, the framing
// around them, the message's accounting, and a result with totals.
func partialClaude() string {
	return fake.Lines(
		`{"type":"system","subtype":"init","session_id":"s-p"}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"s-p"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}},"session_id":"s-p"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}},"session_id":"s-p"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]},"session_id":"s-p"}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"s-p"}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"cache_creation_input_tokens":7,"cache_read_input_tokens":13,"output_tokens":49}},"session_id":"s-p"}`,
		`{"type":"stream_event","event":{"type":"message_stop"},"session_id":"s-p"}`,
		`{"type":"result","subtype":"success","is_error":false,"session_id":"s-p","result":"hello world","num_turns":1,"total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":49}}`,
	)
}

// since skips a test of something the binary under test predates. These
// cases pin what 1.1.0 added — deltas, usage numbers, totals on done, the
// tool's input — and are gaps, not failures, against an older module; the
// gate goes when go.mod moves past it.
func since(t *testing.T, version string) {
	t.Helper()
	if binVersion == "" {
		return // no binary at all is reported by start
	}
	if older(binVersion, version) {
		t.Skipf("rota %s predates %s, which this pins", binVersion, version)
	}
}

// older reports whether a dotted version reads before another, number by
// number, with a missing number as 0.
func older(a, b string) bool {
	as, bs := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x = atoi(as[i])
		}
		if i < len(bs) {
			y = atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ndjson reads one object per line.
func ndjson(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("not one object per line: %q\n%s", line, raw)
		}
		out = append(out, ev)
	}
	return out
}

func TestRun_PartialMessagesStreamDeltasThenTheWhole(t *testing.T) {
	s := start(t, opts{claude: partialClaude()})
	resp, raw := s.do("POST", "/v1/accounts/2/run",
		`{"prompt":"p","stream":true,"include_partial_messages":true}`, "Accept", "application/x-ndjson")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	var fragments, wholes []string
	var other int
	for _, ev := range ndjson(t, raw) {
		switch {
		case ev["type"] == "text" && ev["delta"] == true:
			fragments = append(fragments, ev["text"].(string))
			if ev["blocks"] != nil {
				t.Fatalf("a fragment is not split into blocks: %v", ev)
			}
		case ev["type"] == "text":
			wholes = append(wholes, ev["text"].(string))
			if ev["delta"] != nil {
				t.Fatalf("the whole piece is unmarked: %v", ev)
			}
		case ev["type"] == "other":
			other++
		}
	}
	if strings.Join(fragments, "|") != "hello |world" || strings.Join(wholes, "|") != "hello world" {
		t.Fatalf("fragments %q, wholes %q in:\n%s", fragments, wholes, raw)
	}
	// The CLI's init and result are the two "other" a stream carries; the
	// framing around the fragments (block start and stop, message stop) is
	// no event at all.
	if other != 2 {
		t.Fatalf("%d other events, want the CLI's init and result only:\n%s", other, raw)
	}
}

func TestRun_PartialMessagesNeedAStream(t *testing.T) {
	s := start(t, opts{claude: partialClaude()})
	code, _, raw := s.run(2, `{"prompt":"p","include_partial_messages":true}`)
	if code != 400 || !strings.Contains(string(raw), "include_partial_messages") || !strings.Contains(string(raw), "stream") {
		t.Fatalf("refused by name, naming what is missing: %d %s", code, raw)
	}
}

func TestRun_UsageEventCarriesTheMessagesNumbers(t *testing.T) {
	s := start(t, opts{claude: partialClaude()})
	_, raw := s.do("POST", "/v1/accounts/2/run",
		`{"prompt":"p","stream":true,"include_partial_messages":true}`, "Accept", "application/x-ndjson")
	var usage []map[string]any
	for _, ev := range ndjson(t, raw) {
		if ev["type"] == "usage" {
			usage = append(usage, ev)
		}
	}
	if len(usage) != 1 {
		t.Fatalf("one message, one reading: %v", usage)
	}
	u, _ := usage[0]["usage"].(map[string]any)
	if u["input_tokens"] != 10.0 || u["output_tokens"] != 49.0 || u["cache_read_input_tokens"] != 13.0 || u["cache_creation_input_tokens"] != 7.0 {
		t.Fatalf("message_delta's accounting, in rota's names: %v", usage[0])
	}
}

func TestRun_DoneCarriesTheTotals(t *testing.T) {
	s := start(t, opts{claude: partialClaude()})
	resp, raw := s.do("POST", "/v1/accounts/2/run", `{"prompt":"p","stream":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	frames := sseFrames(t, string(raw))
	last := frames[len(frames)-1]
	u, _ := last.data["usage"].(map[string]any)
	if last.event != "done" || last.data["num_turns"] != 1.0 || last.data["cost_usd"] != 0.25 || u["output_tokens"] != 49.0 {
		t.Fatalf("the end of a stream says what the run cost: %+v", last)
	}
}

func TestRun_ToolEventCarriesTheToolsInput(t *testing.T) {
	s := start(t, opts{claude: fake.Lines(
		`{"type":"system","subtype":"init","session_id":"s-t"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/srv/api/go.mod"}}]},"session_id":"s-t"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"module x"}]},"session_id":"s-t"}`,
		`{"type":"result","subtype":"success","is_error":false,"session_id":"s-t","result":"x","num_turns":2}`,
	)})
	_, raw := s.do("POST", "/v1/accounts/2/run", `{"prompt":"p","stream":true}`, "Accept", "application/x-ndjson")
	var tool map[string]any
	for _, ev := range ndjson(t, raw) {
		if ev["type"] == "tool" {
			tool = ev
		}
		if ev["type"] == "tool_result" && ev["input"] != nil {
			t.Fatalf("only the call has an input: %v", ev)
		}
	}
	in, _ := tool["input"].(map[string]any)
	if tool["tool"] != "Read" || tool["tool_id"] != "toolu_1" || in["file_path"] != "/srv/api/go.mod" {
		t.Fatalf("the tool's own input, verbatim: %v\n%s", tool, raw)
	}
}
