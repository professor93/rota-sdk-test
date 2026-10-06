package serve_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A terminal is an account's CLI on a pseudo-terminal that lives in the
// server: several people may attach to one, and exactly one of them at a
// time may type into it. These are about that from the outside — over the
// published HTTP and WebSocket interface, against a fake claude that
// behaves like something being typed at.
//
// The group is off in the defaults, so every case here turns it on in a
// server.toml, which is also the only way to turn it on.

// opBinary is the frame that carries a terminal's bytes. Everything else on
// the socket is a document, and the framing is what tells them apart.
const opBinary = 0x2

// terminalConfig is a server with the terminal on and one watch token, for
// the half of these cases that is about who may type.
func terminalConfig() string {
	return `[routes]
terminal = true

[[tokens]]
name = "ci-watch"
role = "watch"
sha256 = "` + hashed(watchToken) + `"
`
}

// ttyClaude is a claude that sits at a terminal rather than on a pipe: it
// says how big its window is — read off the device, which is something only
// a program at a terminal can do — answers every line with a transformed
// copy, and exits on "bye".
//
// The transformed copy matters: a terminal echoes what is typed into it all
// by itself, so a test that saw "hello" could not otherwise tell whether the
// CLI read it or the kernel bounced it back.
func ttyClaude() string {
	return `printf 'ready %s\r\n' "$(stty size)"
while IFS= read -r line; do
  case "$line" in
  size) printf 'size %s\r\n' "$(stty size)" ;;
  bye)  printf 'bye\r\n'; exit 0 ;;
  *)    printf 'got:%s\r\n' "$line" ;;
  esac
done
`
}

// onlyUnix skips where there are no pseudo-terminals, which is where rota
// refuses to serve one at all.
func onlyUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a terminal is a Unix device; rota serves one on linux and macOS")
	}
}

/* ----------------------------------------------- a client of a terminal --- */

// terminal is one socket onto a terminal: everything it has been shown, and
// how far its waits have got through the documents.
type terminal struct {
	t    *testing.T
	c    *wsClient
	out  strings.Builder
	seen int
}

func (s *server) attachTerm(t *testing.T, id, tok, query string) *terminal {
	t.Helper()
	path := "/v1/terminals/" + id + "/ws"
	if query != "" {
		path += "?" + query
	}
	c, resp := s.dialWS(path, "Sec-WebSocket-Protocol", "rota, bearer."+tok)
	if c == nil {
		t.Fatalf("attaching to %s was refused: %d", id, resp.StatusCode)
	}
	return &terminal{t: t, c: c}
}

// step reads one frame, keeping the bytes of output apart from the
// documents.
func (tm *terminal) step() (byte, []byte) {
	op, payload := tm.c.read()
	if op == opBinary {
		tm.out.Write(payload)
	}
	return op, payload
}

// waitDoc finds a document, going forward only: a terminal answers several
// different things with an error frame, and a wait that looked at everything
// again would keep finding the last refusal and call it the next answer.
func (tm *terminal) waitDoc(what string, want func(map[string]any) bool) map[string]any {
	tm.t.Helper()
	for tm.seen < len(tm.c.log) {
		doc := tm.c.log[tm.seen]
		tm.seen++
		if want(doc) {
			return doc
		}
	}
	for range 2000 {
		op, payload := tm.step()
		switch op {
		case opText:
			tm.seen = len(tm.c.log)
			if doc := tm.c.log[len(tm.c.log)-1]; want(doc) {
				return doc
			}
		case opClose:
			tm.t.Fatalf("the socket closed with %d before %s arrived:\n%s",
				closeCode(payload), what, tm.c.all())
		}
	}
	tm.t.Fatalf("%s never arrived:\n%s", what, tm.c.all())
	return nil
}

func (tm *terminal) waitType(kind string) map[string]any {
	tm.t.Helper()
	return tm.waitDoc("a "+kind+" frame", func(doc map[string]any) bool { return doc["type"] == kind })
}

// waitOut reads until the terminal has printed something.
func (tm *terminal) waitOut(want string) {
	tm.t.Helper()
	for range 2000 {
		if strings.Contains(tm.out.String(), want) {
			return
		}
		if op, payload := tm.step(); op == opClose {
			tm.t.Fatalf("the socket closed with %d before %q was printed; it had said:\n%s",
				closeCode(payload), want, tm.out.String())
		}
	}
	tm.t.Fatalf("%q was never printed; the terminal said:\n%s", want, tm.out.String())
}

// typeIn sends the bytes of somebody typing, which is a binary frame.
func (tm *terminal) typeIn(s string) {
	tm.t.Helper()
	tm.c.send(0x80|opBinary, []byte(s))
}

// startTerm asks for one terminal and returns its description.
func (s *server) startTerm(t *testing.T, body string) map[string]any {
	t.Helper()
	resp, raw := s.do("POST", "/v1/terminals", body)
	if resp.StatusCode != 201 {
		t.Fatalf("starting a terminal: %d %s", resp.StatusCode, raw)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the answer is not a description: %v %s", err, raw)
	}
	if doc["id"] == nil {
		t.Fatalf("a terminal with no id: %s", raw)
	}
	return doc
}

// endTerm ends a terminal, whatever the test did with it: a CLI on a
// pseudo-terminal outlives every connection to it on purpose, so nothing
// else would.
func (s *server) endTerm(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() { s.do("DELETE", "/v1/terminals/"+id, "") })
}

/* ------------------------------------------------------------ the cases --- */

// TestTerminal_ACLIRunsOnAPseudoTerminalAndTakesTyping is the whole feature
// in one case: a terminal is started on an account, what its CLI printed
// arrives as bytes at the size it was given, and what the holder types
// reaches the CLI and comes back.
func TestTerminal_ACLIRunsOnAPseudoTerminalAndTakesTyping(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig(), claude: ttyClaude()})

	doc := s.startTerm(t, `{"account":2,"cols":120,"rows":40,"label":"suite"}`)
	id := doc["id"].(string)
	s.endTerm(t, id)
	if doc["kind"] != "account" || doc["label"] != "suite" {
		t.Fatalf("the description is %v", doc)
	}
	acc, _ := doc["account"].(map[string]any)
	if acc["id"] != float64(2) || acc["provider"] != "claude" {
		t.Fatalf("the account it runs is %v", acc)
	}

	tm := s.attachTerm(t, id, s.token, "")
	hello := tm.waitType("hello")
	if hello["id"] != id || hello["cols"] != float64(120) || hello["rows"] != float64(40) {
		t.Fatalf("hello says %v", hello)
	}
	if hello["holder"] != "token" {
		t.Fatalf("the first control connection must hold the keyboard: %v", hello["holder"])
	}
	// stty reads the size off the device, so this is the kernel's answer.
	tm.waitOut("ready 40 120")

	tm.typeIn("hello there\n")
	tm.waitOut("got:hello there")
}

// TestTerminal_AWatchTokenLooksAndCannotType is the rule the whole feature
// rests on: a watcher is sent every byte and its own bytes never reach the
// CLI.
func TestTerminal_AWatchTokenLooksAndCannotType(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig(), claude: ttyClaude()})

	id := s.startTerm(t, `{"account":2,"cols":100,"rows":30}`)["id"].(string)
	s.endTerm(t, id)
	holder := s.attachTerm(t, id, s.token, "")
	holder.waitOut("ready ")

	looker := s.attachTerm(t, id, watchToken, "")
	if hello := looker.waitType("hello"); hello["holder"] != "token" {
		t.Fatalf("a watcher must be told who is typing: %v", hello["holder"])
	}
	// The watcher sees what the terminal printed before it arrived.
	looker.waitOut("ready ")

	looker.typeIn("watcher-was-here\n")
	if msg := looker.waitType("error")["message"]; msg == nil || !strings.Contains(msg.(string), "watch") {
		t.Fatalf("a watcher typing is refused with %v", msg)
	}

	// The proof is what the CLI did with it: nothing. Something sent after
	// it has already come back, so anything the watcher sent would be here.
	holder.typeIn("after the watcher\n")
	holder.waitOut("got:after the watcher")
	if strings.Contains(holder.out.String(), "watcher-was-here") {
		t.Fatalf("a watcher's bytes reached the CLI:\n%s", holder.out.String())
	}
}

// TestTerminal_TheListingSaysWhoIsHoldingTheKeyboard: what the page's panel
// is built from is the same document the listing carries.
func TestTerminal_TheListingSaysWhoIsHoldingTheKeyboard(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig(), claude: ttyClaude()})

	id := s.startTerm(t, `{"account":2}`)["id"].(string)
	s.endTerm(t, id)

	// Nobody is attached yet, so nobody is holding it.
	if got := s.terminals(t)[0]; got["holder"] != nil || got["id"] != id {
		t.Fatalf("with nobody attached the listing says %v", got)
	}

	tm := s.attachTerm(t, id, s.token, "")
	tm.waitType("hello")
	tm.waitOut("ready ")

	got := s.terminals(t)[0]
	if got["holder"] != "token" {
		t.Fatalf("the listing says the keyboard is %v", got["holder"])
	}
	viewers, _ := got["viewers"].([]any)
	if len(viewers) != 1 {
		t.Fatalf("the listing says the viewers are %v", viewers)
	}
	who, _ := viewers[0].(map[string]any)
	if who["name"] != "token" || who["role"] != "control" {
		t.Fatalf("the one viewer is %v", who)
	}
	if got["offset"] == nil || got["ended"] != false {
		t.Fatalf("the listing says %v", got)
	}
}

// TestTerminal_AShellNeedsAskingFor: what this serves is an account's CLI. A
// plain shell is a different offer, and is refused in a sentence until the
// file makes it.
func TestTerminal_AShellNeedsAskingFor(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig(), claude: ttyClaude()})

	resp, raw := s.do("POST", "/v1/terminals", `{"kind":"shell"}`)
	if resp.StatusCode != 403 || !strings.Contains(string(raw), "terminal.shell") {
		t.Fatalf("a shell nobody allowed: %d %s", resp.StatusCode, raw)
	}
}

// TestTerminal_TheGroupIsOffUntilItIsAskedFor: without the file saying so,
// the routes are not registered and answer as any path this server never had.
func TestTerminal_TheGroupIsOffUntilItIsAskedFor(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{})
	for _, r := range []struct{ method, path string }{
		{"GET", "/v1/terminals"},
		{"POST", "/v1/terminals"},
		{"DELETE", "/v1/terminals/whatever"},
	} {
		if resp, raw := s.do(r.method, r.path, ""); resp.StatusCode != 404 {
			t.Fatalf("%s %s answered %d, want 404: %s", r.method, r.path, resp.StatusCode, raw)
		}
	}
	// And the rest of the server is untouched.
	if resp, _ := s.do("GET", "/v1/accounts", ""); resp.StatusCode != 200 {
		t.Fatalf("the API must be untouched: %d", resp.StatusCode)
	}
}

// TestTerminal_ANetworkAddressNeedsACertificate is the security rule stated
// as a refusal to start: whoever reaches this port with a control sign-in
// runs commands on the machine, so a terminal served anywhere but the
// loopback needs TLS. It is read through --print-config, which loads the
// file and exits, so a server that failed to refuse cannot hang this test.
func TestTerminal_ANetworkAddressNeedsACertificate(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "server.toml")
	body := "[routes]\nterminal = true\n\n[server]\nlisten = \"0.0.0.0:8787\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := serveOnce(t, home, "--config", path, "--print-config")
	if code == 0 || !strings.Contains(out, "tls.cert") {
		t.Fatalf("a terminal open to the network was accepted: %d\n%s", code, out)
	}

	// With a certificate named, the same file is read.
	withTLS := body + "\n[tls]\ncert = \"/c.pem\"\nkey = \"/k.pem\"\n"
	if err := os.WriteFile(path, []byte(withTLS), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := serveOnce(t, home, "--config", path, "--print-config"); code != 0 ||
		!strings.Contains(out, "terminal") {
		t.Fatalf("with a certificate it must load: %d\n%s", code, out)
	}
}

// terminals is the listing, as a test reads it.
func (s *server) terminals(t *testing.T) []map[string]any {
	t.Helper()
	resp, raw := s.do("GET", "/v1/terminals", "")
	if resp.StatusCode != 200 {
		t.Fatalf("listing the terminals: %d %s", resp.StatusCode, raw)
	}
	var doc struct {
		Terminals []map[string]any `json:"terminals"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Terminals) == 0 {
		t.Fatalf("the listing is %v %s", err, raw)
	}
	return doc.Terminals
}
