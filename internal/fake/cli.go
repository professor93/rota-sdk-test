package fake

import (
	"os"
	"path/filepath"
	"testing"
)

// CLI writes a shell script as an executable named name in a fresh
// directory, puts that directory first on this process's PATH (Run finds
// the binary through exec.LookPath), and returns the directory so the test
// can also put it in Command.BaseEnv, which is the PATH the child sees.
func CLI(t testing.TB, name, script string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// BaseEnv is the environment a child started by a fake CLI in dir needs:
// that dir on PATH, and nothing else.
func BaseEnv(dir string) []string {
	return []string{"PATH=" + dir + ":/usr/bin:/bin"}
}

// ClaudeResult is a script that reads the prompt from stdin and answers as
// the claude CLI does in print mode: one terminal result event carrying the
// prompt and the argv, so a test can assert both, then exits with code.
func ClaudeResult(code int) string {
	return `stdin=$(cat)
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"STDIN=%s ARGS=%s","num_turns":1,"total_cost_usd":0.5}\n' "$stdin" "$*"
echo "fake-stderr" >&2
exit ` + itoa(code) + "\n"
}

// Echo is a script that plays a CLI with a streaming input: one turn per
// message, for as long as stdin stays open, which is the whole point of a run
// that stays open.
//
// Each turn is the two lines a turn is made of — the assistant saying
// something, and the result that ends it — carrying back the text it was sent
// so a test can tell one turn's answer from another's. Every result says that
// nothing is left waiting, which is the one number delivery is read from.
//
// A control request is answered where it arrives, carrying the id it was sent
// under: an acknowledgement naming some other request would tell a client
// nothing.
func Echo() string {
	return `n=0
while IFS= read -r line; do
  case "$line" in
  *control_request*)
    id=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
    printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{"still_queued":[]}}}\n' "$id"
    continue ;;
  esac
  n=$((n + 1))
  text=$(printf '%s' "$line" | sed -n 's/.*"text":"\([^"]*\)".*/\1/p')
  [ -n "$text" ] || text=$n
  printf '{"type":"assistant","session_id":"s-fake","message":{"role":"assistant","content":[{"type":"text","text":"echo: %s"}]}}\n' "$text"
  printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"echo: %s","num_turns":%s,"total_cost_usd":0.01,"queued_turn_count":0}\n' "$text" "$n"
done
exit 0
`
}

// Lines is a script that prints exactly these lines to stdout, consumes
// stdin, and exits 0. For event streams a test writes itself.
func Lines(lines ...string) string {
	s := "cat >/dev/null\n"
	for _, l := range lines {
		s += "printf '%s\\n' '" + l + "'\n"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
