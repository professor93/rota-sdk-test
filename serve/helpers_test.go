package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The pinned server binary, built once for the whole package. A build
// failure is remembered rather than fatal, so every test can skip with the
// reason instead of the package refusing to run.
var (
	rotaBin    string
	binVersion string
	buildErr   error
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rota-serve-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rotaBin = filepath.Join(dir, "rota")
	build := exec.Command("go", "build", "-o", rotaBin, "github.com/professor93/rota/cmd/rota")
	if out, err := build.CombinedOutput(); err != nil {
		buildErr = fmt.Errorf("building the pinned rota binary: %v\n%s", err, out)
	} else if out, err := exec.Command(rotaBin, "version").Output(); err != nil {
		buildErr = fmt.Errorf("rota version: %v", err)
	} else if f := strings.Fields(string(out)); len(f) != 2 || f[0] != "rota" {
		buildErr = fmt.Errorf("rota version printed %q, want \"rota <version>\"", out)
	} else {
		binVersion = f[1]
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// opts is what a test may vary about its server. Everything unset gets the
// default the tests share, so most tests pass opts{}.
type opts struct {
	accounts       string // raw accounts.json; empty means seed()
	claude, grok   string // fake CLI bodies; empty means claudeScript / grokScript
	root           bool   // confine paths to a fresh directory, exposed as server.root
	allowDangerous bool
	maxConcurrent  int
	timeout        time.Duration
}

// server is one running rota serve with its own store and fake CLIs.
type server struct {
	t      *testing.T
	url    string
	token  string
	home   string // ROTA_HOME
	root   string // the --root directory when opts.root was set
	stderr *lockedBuffer
}

const token = "test-token"

// client keeps no connection alive. The default transport races a fresh
// dial against a connection being returned to its pool and parks the loser
// unused; the server then waits five seconds for that connection to say
// something before it will shut down, which is what the cleanup waits on.
var client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

// seed is the store every test starts from: grok first in the rotation on an
// API key, claude second on a token that never expires. Claude is metered,
// so it carries a reading fresh enough that the rotation's refresh skips it
// and nothing ever reaches the network.
func seed() string {
	return fmt.Sprintf(`{"ordered":true,"accounts":[
 {"id":1,"provider":"grok","email":"g@x","order":1,"token":{"accessToken":"xai-fake"}},
 {"id":2,"provider":"claude","email":"c@x","order":2,"quotaAt":%d,
  "quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
  "token":{"accessToken":"tok-claude","expiresAt":0}}]}`, time.Now().UnixMilli())
}

// claudeScript answers as the claude CLI does in print mode: stream-json
// gets an init and a text event before the result, plain json only the result.
// The result echoes what a test wants to see: the prompt read from stdin,
// the argv, and the working directory. sleep and exit shape the run.
func claudeScript(sleep string, exit int) string {
	s := "prompt=$(cat)\n"
	if sleep != "" {
		s += "sleep " + sleep + "\n"
	}
	return s + `case "$*" in *stream-json*)
printf '{"type":"system","subtype":"init","session_id":"s-fake"}\n'
printf '{"type":"assistant","session_id":"s-fake","message":{"role":"assistant","content":[{"type":"text","text":"streamed ARGS=%s"}]}}\n' "$*"
;; esac
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"PROMPT=%s ARGS=%s CWD=%s","num_turns":1,"total_cost_usd":0.01}\n' "$prompt" "$*" "$(pwd -P)"
echo fake-stderr >&2
exit ` + strconv.Itoa(exit) + "\n"
}

// grokScript answers as the grok CLI does with --output-format json: one
// object, no type field, camelCase keys. The prompt arrives in a file named
// by --prompt-file, so the script finds that flag and reads it back.
func grokScript(sleep string) string {
	s := `cat >/dev/null
prompt=; want=0
for a in "$@"; do
  if [ "$want" = 1 ]; then prompt=$(cat "$a"); want=0; fi
  [ "$a" = --prompt-file ] && want=1
done
`
	if sleep != "" {
		s += "sleep " + sleep + "\n"
	}
	return s + `printf '{"text":"PROMPT=%s ARGS=%s","stopReason":"end_turn","sessionId":"01a00000-0000-7000-8000-000000000001","usage":{"input_tokens":14221},"num_turns":1,"total_cost_usd":0.030798}\n' "$prompt" "$*"
`
}

// start runs the pinned binary on a free loopback port with a private
// ROTA_HOME, the fake CLIs first on the child's PATH, and background refresh
// off. It returns once GET / answers and stops the process at cleanup.
func start(t *testing.T, o opts) *server {
	t.Helper()
	if buildErr != nil {
		t.Skip(buildErr)
	}
	home := t.TempDir()
	if o.accounts == "" {
		o.accounts = seed()
	}
	if err := os.WriteFile(filepath.Join(home, "accounts.json"), []byte(o.accounts), 0o600); err != nil {
		t.Fatal(err)
	}
	if o.claude == "" {
		o.claude = claudeScript("", 0)
	}
	if o.grok == "" {
		o.grok = grokScript("")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"claude": o.claude, "grok": o.grok} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	addr := freeAddr(t)
	args := []string{"serve", addr, "--token", token, "--refresh-every", "0"}
	s := &server{t: t, url: "http://" + addr, token: token, home: home, stderr: &lockedBuffer{}}
	if o.root {
		s.root = t.TempDir()
		args = append(args, "--root", s.root)
	}
	if o.allowDangerous {
		args = append(args, "--allow-dangerous")
	}
	if o.maxConcurrent > 0 {
		args = append(args, "--max-concurrent", strconv.Itoa(o.maxConcurrent))
	}
	if o.timeout > 0 {
		args = append(args, "--timeout", o.timeout.String())
	}
	cmd := exec.Command(rotaBin, args...)
	// The child sees only what it needs: the fakes and the shell's own
	// tools, a private HOME so no real CLI configuration is read, and the
	// store. Nothing from this process's environment leaks in.
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		"ROTA_HOME=" + home,
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
	}
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// Registered after every TempDir above, so the process is gone before
	// its directories are removed.
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-exited:
			t.Fatalf("rota serve exited before answering: %v\n%s", err, s.stderr.String())
		default:
		}
		if resp, err := client.Get(s.url + "/"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("rota serve never answered on %s\n%s", addr, s.stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// freeAddr picks a loopback port nothing is listening on right now.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// do sends one request carrying the server's token and returns the response
// with its body read. hdr pairs override headers; an empty value removes one.
func (s *server) do(method, path, body string, hdr ...string) (*http.Response, []byte) {
	s.t.Helper()
	return s.doCtx(context.Background(), method, path, body, hdr...)
}

func (s *server) doCtx(ctx context.Context, method, path, body string, hdr ...string) (*http.Response, []byte) {
	s.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.url+path, r)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
			continue
		}
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// object decodes a JSON object reply, failing the test on anything else.
func object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	return doc
}

// accountView is the listing row as GET /v1/accounts renders it.
type accountView struct {
	ID        int     `json:"id"`
	Provider  string  `json:"provider"`
	Email     string  `json:"email"`
	Status    string  `json:"status"`
	Order     int     `json:"order"`
	Threshold int     `json:"threshold"`
	Percent   float64 `json:"percent"`
	Metered   bool    `json:"metered"`
	Cwd       string  `json:"cwd"`
	ConfigDir string  `json:"config_dir"`
	Windows   []struct {
		Name    string  `json:"name"`
		Percent float64 `json:"percent"`
	} `json:"windows"`
}

type listing struct {
	Accounts []accountView `json:"accounts"`
	Default  *int          `json:"default"`
}

// list reads GET /v1/accounts.
func (s *server) list() listing {
	s.t.Helper()
	resp, raw := s.do("GET", "/v1/accounts", "")
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("GET /v1/accounts: %d %s", resp.StatusCode, raw)
	}
	var l listing
	if err := json.Unmarshal(raw, &l); err != nil {
		s.t.Fatalf("GET /v1/accounts: %v\n%s", err, raw)
	}
	return l
}

// ids is the listing's account ids in the order they were sent.
func (l listing) ids() []int {
	out := make([]int, 0, len(l.Accounts))
	for _, a := range l.Accounts {
		out = append(out, a.ID)
	}
	return out
}

// runReply is a finished run as POST .../run answers it.
type runReply struct {
	Account   int    `json:"account"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
	ExitCode  int    `json:"exit_code"`
	Stderr    string `json:"stderr"`
	Error     string `json:"error"`
}

// run posts a body to an account's run endpoint, or to /v1/run for id 0.
func (s *server) run(id int, body string, hdr ...string) (int, runReply, []byte) {
	s.t.Helper()
	path := "/v1/run"
	if id != 0 {
		path = "/v1/accounts/" + strconv.Itoa(id) + "/run"
	}
	resp, raw := s.do("POST", path, body, hdr...)
	var out runReply
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

// frame is one Server-Sent Event: its name and its decoded data.
type frame struct {
	event string
	data  map[string]any
}

// sseFrames splits an event stream into frames, refusing any line that is
// not one of the two the server is meant to write.
func sseFrames(t *testing.T, body string) []frame {
	t.Helper()
	var out []frame
	for _, chunk := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var f frame
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f.data); err != nil {
					t.Fatalf("data line is not JSON: %v\n%s", err, line)
				}
			default:
				t.Fatalf("unexpected SSE line %q in:\n%s", line, body)
			}
		}
		if f.event == "" || f.data == nil {
			t.Fatalf("frame without event or data:\n%s", chunk)
		}
		out = append(out, f)
	}
	return out
}

// lockedBuffer collects the server's stderr from exec's copying goroutine
// without racing the test that reads it back.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
