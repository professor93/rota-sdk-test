// These pin who a request is and what that lets them do, from outside the
// binary: a token with a role that reads and cannot run, a person who signs
// in on the page and is answered with a cookie, a link that works once, and
// the one route that answers with no credential at all.
//
// Nothing here names anything the pinned module does not have — the file is
// written as text and the binary is asked about it over HTTP — so the
// package still compiles against the published version and each test simply
// skips there.

package serve_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// serveStdin is serveOnce with something on the command's standard input:
// `rota serve passwd` reads the password from there when it is not a
// terminal, which in a test it never is.
func serveStdin(t *testing.T, home, in string, args ...string) (string, int) {
	t.Helper()
	if buildErr != nil {
		t.Skip(buildErr)
	}
	cmd := exec.Command(rotaBin, append([]string{"serve"}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "ROTA_HOME=" + home}
	cmd.Stdin = strings.NewReader(in)
	// The two streams are kept apart: the block to paste is on standard
	// output and the advice about it on standard error, which is what makes
	// `rota serve passwd … > block.toml` work at all.
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &exit):
		return out.String() + errs.String(), exit.ExitCode()
	}
	t.Fatalf("rota serve %s: %v", strings.Join(args, " "), err)
	return "", 0
}

// writeFile puts one server.toml somewhere rota will read it: nobody but
// its owner may, which is what the server insists on.
func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// tomlBlock is one `[[table]]` block out of what a command printed: the
// header and the `key = value` lines under it, and nothing after them. Taking
// the rest of the output would take whatever the command said next.
func tomlBlock(t *testing.T, out, table string) string {
	t.Helper()
	i := strings.Index(out, "[["+table+"]]")
	if i < 0 {
		t.Fatalf("no [[%s]] block in:\n%s", table, out)
	}
	var b strings.Builder
	for n, line := range strings.Split(out[i:], "\n") {
		if n > 0 && !strings.Contains(line, "=") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// fieldOf is one `key = "value"` out of a printed block.
func fieldOf(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	t.Fatalf("no %s in:\n%s", key, out)
	return ""
}

// hashed is the hex SHA-256 a [[tokens]] entry holds.
func hashed(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// watchToken is a token a test hands out; the server only ever sees its
// digest, which is the whole point of writing one down.
const watchToken = "a-watch-token-for-the-suite"

// do sends one request with headers of the caller's choosing and no bearer
// token unless they add one: the suite's own helper always carries the
// server's token, which is the one thing these tests must be able to leave
// off.
func (s *server) raw(t *testing.T, method, path, body string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	return s.do(method, path, body, append([]string{"Authorization", ""}, hdr...)...)
}

// TestAuth_AWatchTokenReadsAndDoesNotRun is the roles in one case: the same
// server, two tokens, and the difference is what each is allowed to ask for.
func TestAuth_AWatchTokenReadsAndDoesNotRun(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{config: `[[tokens]]
name = "ci-watch"
role = "watch"
sha256 = "` + hashed(watchToken) + `"
`})
	// Reading is allowed.
	resp, raw := s.raw(t, "GET", "/v1/accounts", "", "Authorization", "Bearer "+watchToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a watcher must be able to list: %d %s", resp.StatusCode, raw)
	}
	var l listing
	if err := json.Unmarshal(raw, &l); err != nil || len(l.Accounts) != 2 {
		t.Fatalf("the listing a watcher got: %v %s", err, raw)
	}
	// Running is not, and the refusal says why.
	resp, raw = s.raw(t, "POST", "/v1/accounts/1/run", `{"prompt":"hello"}`,
		"Authorization", "Bearer "+watchToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a watcher ran something: %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "only watch") {
		t.Fatalf("the refusal must say why: %s", raw)
	}
	// And the server's own token still does everything it always did.
	if code, _, out := s.run(1, `{"prompt":"hello"}`); code != http.StatusOK {
		t.Fatalf("control must still run: %d %s", code, out)
	}
}

// TestAuth_AUserSignsInAndTheCookieWorks walks the page's way in: the block
// `rota serve passwd` prints, a sign-in, and then the API on the cookie
// alone.
func TestAuth_AUserSignsInAndTheCookieWorks(t *testing.T) {
	since(t, "1.2.0")
	out, code := serveStdin(t, t.TempDir(), "from-the-suite\n", "passwd", "inoyat", "--role", "control")
	if code != 0 {
		t.Fatalf("serve passwd: %d\n%s", code, out)
	}
	block := tomlBlock(t, out, "users")
	if strings.Contains(block, "from-the-suite") {
		t.Fatalf("the password itself is in the block:\n%s", out)
	}
	if !strings.Contains(block, "pbkdf2-sha256$") {
		t.Fatalf("that is not a derived password:\n%s", out)
	}
	s := start(t, opts{config: block})

	// Signed out, the page is told so.
	if resp, _ := s.raw(t, "GET", "/v1/session", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /v1/session with nothing: %d", resp.StatusCode)
	}
	// Signing in.
	resp, raw := s.raw(t, "POST", "/v1/session", `{"name":"inoyat","password":"from-the-suite"}`,
		"Origin", s.url)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signing in: %d %s", resp.StatusCode, raw)
	}
	var who struct {
		Name string `json:"name"`
		Role string `json:"role"`
		Via  string `json:"via"`
	}
	if err := json.Unmarshal(raw, &who); err != nil {
		t.Fatal(err)
	}
	if who.Name != "inoyat" || who.Role != "control" || who.Via != "user" {
		t.Fatalf("signed in as %+v", who)
	}
	var cookie string
	for _, ck := range resp.Cookies() {
		if ck.Name == "rota_session" {
			cookie = ck.Name + "=" + ck.Value
			if !ck.HttpOnly {
				t.Fatal("the session cookie must not be readable by a script")
			}
		}
	}
	if cookie == "" {
		t.Fatalf("no session cookie was set: %v", resp.Header.Values("Set-Cookie"))
	}
	// The cookie alone is the credential now.
	if resp, raw := s.raw(t, "GET", "/v1/accounts", "", "Cookie", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("the API on the cookie: %d %s", resp.StatusCode, raw)
	}
	// A write on it from somebody else's page is not.
	resp, raw = s.raw(t, "POST", "/v1/accounts/1/run", `{"prompt":"hello"}`,
		"Cookie", cookie, "Origin", "https://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-site write on the cookie: %d %s", resp.StatusCode, raw)
	}
	// A wrong password says nothing about whether the name exists, and the
	// two answers are the same.
	var answers []string
	for _, body := range []string{
		`{"name":"inoyat","password":"not-it"}`,
		`{"name":"nobody-at-all","password":"not-it"}`,
	} {
		resp, raw := s.raw(t, "POST", "/v1/session", body, "Origin", s.url)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: %d %s", body, resp.StatusCode, raw)
		}
		answers = append(answers, string(raw))
	}
	if answers[0] != answers[1] {
		t.Fatalf("a wrong password and an unknown name must read the same:\n%s\n%s", answers[0], answers[1])
	}
	// And signing out ends it on the server, not only in the browser.
	if resp, raw := s.raw(t, "DELETE", "/v1/session", "", "Cookie", cookie, "Origin", s.url); resp.StatusCode != http.StatusOK {
		t.Fatalf("signing out: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := s.raw(t, "GET", "/v1/session", "", "Cookie", cookie); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a session that was ended still works: %d", resp.StatusCode)
	}
}

// TestAuth_AnInviteWorksOnce: made with control, spent by whoever opens it,
// and worth nothing afterwards.
func TestAuth_AnInviteWorksOnce(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/invites", `{"ttl":"5m"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("making an invite: %d %s", resp.StatusCode, raw)
	}
	var made struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &made); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(made.URL, s.url+"/invite/") {
		t.Fatalf("the link is %q, not on this server", made.URL)
	}

	client := &http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	first, err := client.Get(made.URL)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("following the link: %d", first.StatusCode)
	}
	var cookie string
	for _, ck := range first.Cookies() {
		if ck.Name == "rota_session" {
			cookie = ck.Name + "=" + ck.Value
		}
	}
	if cookie == "" {
		t.Fatal("the invite set no session")
	}
	// A watcher: it may read...
	if resp, raw := s.raw(t, "GET", "/v1/accounts", "", "Cookie", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("an invited watcher cannot list: %d %s", resp.StatusCode, raw)
	}
	// ...and it may not run.
	resp, raw = s.raw(t, "POST", "/v1/accounts/1/run", `{"prompt":"hello"}`,
		"Cookie", cookie, "Origin", s.url)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an invited watcher ran something: %d %s", resp.StatusCode, raw)
	}
	// The same link again is nothing.
	again, err := client.Get(made.URL)
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusNotFound {
		t.Fatalf("the link worked twice: %d", again.StatusCode)
	}
}

// TestAuth_HealthAnswersWithThePageOff is the reason health is its own
// group: a server with nothing else open still says it is alive.
func TestAuth_HealthAnswersWithThePageOff(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{
		ready:  "/v1/health",
		config: "[routes]\nplayground = false\n",
	})
	resp, raw := s.raw(t, "GET", "/v1/health", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health with no credential: %d %s", resp.StatusCode, raw)
	}
	doc := object(t, raw)
	if doc["ok"] != true || len(doc) != 1 {
		t.Fatalf("health says %s; it must say one thing and no version", raw)
	}
	// The page is off, so the root that used to be the liveness answer is
	// not there.
	if resp, _ := s.do("GET", "/", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / with the page off: %d", resp.StatusCode)
	}
}

// TestAuth_ServeTokenPrintsATokenAndItsDigest: the command prints a token
// once, the file holds only its hash, and the two go together.
func TestAuth_ServeTokenPrintsATokenAndItsDigest(t *testing.T) {
	since(t, "1.2.0")
	out, code := serveStdin(t, t.TempDir(), "", "token", "ci-watch", "--role", "watch")
	if code != 0 {
		t.Fatalf("serve token: %d\n%s", code, out)
	}
	token := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	block := tomlBlock(t, out, "tokens")
	if strings.Contains(block, token) {
		t.Fatalf("the token itself is in the block:\n%s", out)
	}
	if !strings.Contains(block, hashed(token)) {
		t.Fatalf("the block does not hold the digest of the token printed:\n%s", out)
	}
	// And a server configured with that block admits that token.
	s := start(t, opts{config: block})
	if resp, raw := s.raw(t, "GET", "/v1/accounts", "", "Authorization", "Bearer "+token); resp.StatusCode != http.StatusOK {
		t.Fatalf("the token the command printed was refused: %d %s", resp.StatusCode, raw)
	}
}

// TestAuth_PrintConfigMasksEverySecret: what --print-config shows of a file
// full of secrets is that they are set, and that it is a report.
func TestAuth_PrintConfigMasksEverySecret(t *testing.T) {
	since(t, "1.2.0")
	home := t.TempDir()
	pw, code := serveStdin(t, home, "from-the-suite\n", "passwd", "inoyat")
	if code != 0 {
		t.Fatalf("serve passwd: %d\n%s", code, pw)
	}
	hash := fieldOf(t, pw, "password")
	digest := hashed("some-token")
	file := `[auth]
token = "a-real-secret"

[[users]]
name = "inoyat"
role = "watch"
password = "` + hash + `"

[[tokens]]
name = "ci"
role = "watch"
sha256 = "` + digest + `"
`
	path := writeFile(t, file)
	out, code := serveOnce(t, home, "--print-config", "--config", path)
	if code != 0 {
		t.Fatalf("--print-config: %d\n%s", code, out)
	}
	for _, gone := range []string{"a-real-secret", hash, digest} {
		if strings.Contains(out, gone) {
			t.Fatalf("a secret was printed:\n%s", out)
		}
	}
	for _, want := range []string{`"(set)"`, "report", "[[users]]", "[[tokens]]", "session_ttl", "health"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--print-config must show %q:\n%s", want, out)
		}
	}
}
