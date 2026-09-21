package serve_test

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"rotatest/internal/fake"
)

// The opcodes a client of this server needs, and the close codes it is told
// things with.
const (
	opContinue = 0x0
	opText     = 0x1
	opClose    = 0x8

	wsNormal = 1000 // the run ended, or the client said goodbye
	wsPolicy = 1008 // a frame the server will not act on, a refused start included
)

/* --------------------------------------------------- a WebSocket client --- */

// wsClient is as much of RFC 6455 as a test of this server needs: the
// handshake, masked frames out, whole frames in. The server never masks and
// never fragments what it sends, so reading is the short half.
type wsClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	// log is every frame read so far. An ack and the run's own events are
	// written by different goroutines and arrive in either order, so a test
	// looks for what it wants among everything that has come rather than
	// only among what comes next.
	log []map[string]any
}

// dialWS opens one socket. It returns the response instead when the
// handshake was refused, so a test can read the status the server answered
// with.
func (s *server) dialWS(path string, headers ...string) (*wsClient, *http.Response) {
	s.t.Helper()
	host := strings.TrimPrefix(s.url, "http://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		s.t.Fatal(err)
	}
	req := "GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
	for i := 0; i+1 < len(headers); i += 2 {
		req += headers[i] + ": " + headers[i+1] + "\r\n"
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		s.t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, resp
	}
	// The answer is the key this request sent, mixed with the protocol's own
	// constant: an echo would not prove the server understood the handshake.
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		s.t.Fatalf("the handshake must be answered with the accept key: %q", got)
	}
	c := &wsClient{t: s.t, conn: conn, r: br}
	s.t.Cleanup(func() { conn.Close() })
	return c, resp
}

// dialRun opens a socket carrying the token as the subprotocol, which is how
// a browser does it: a WebSocket cannot carry a header.
func (s *server) dialRun(path string) *wsClient {
	s.t.Helper()
	c, resp := s.dialWS(path, "Sec-WebSocket-Protocol", "rota, bearer."+s.token)
	if c == nil {
		s.t.Fatalf("the handshake was refused: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "rota" {
		s.t.Fatalf("only rota is echoed back, never the token: %q", got)
	}
	return c
}

// send writes one frame, masked as every frame a client sends is.
func (c *wsClient) send(head byte, payload []byte) {
	c.t.Helper()
	mask := []byte{0x37, 0xfa, 0x21, 0x3d}
	out := []byte{head}
	switch n := len(payload); {
	case n < 126:
		out = append(out, 0x80|byte(n))
	case n < 1<<16:
		out = append(out, 0x80|126, byte(n>>8), byte(n))
	default:
		out = append(out, 0x80|127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	out = append(out, mask...)
	body := append([]byte(nil), payload...)
	for i := range body {
		body[i] ^= mask[i%4]
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(append(out, body...)); err != nil {
		c.t.Fatalf("writing a frame: %v", err)
	}
}

// say writes one document as a whole text frame, which is what everything a
// client sends into a run is.
func (c *wsClient) say(v any) {
	c.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(0x80|opText, raw)
}

// frame reads one whole frame.
func (c *wsClient) frame() (byte, []byte) {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(c.r, head[:]); err != nil {
		c.t.Fatalf("reading a frame: %v", err)
	}
	if head[1]&0x80 != 0 {
		c.t.Fatal("a server never masks what it sends")
	}
	n := uint64(head[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			c.t.Fatalf("reading a length: %v", err)
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			c.t.Fatalf("reading a length: %v", err)
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		c.t.Fatalf("reading a payload of %d: %v", n, err)
	}
	if head[0]&0x80 == 0 {
		c.t.Fatalf("a server sends whole frames: %q", payload)
	}
	return head[0] & 0x0f, payload
}

// read takes one frame, keeping every event and ack it sees.
func (c *wsClient) read() (byte, []byte) {
	c.t.Helper()
	op, payload := c.frame()
	if op == opText {
		var doc map[string]any
		if err := json.Unmarshal(payload, &doc); err != nil {
			c.t.Fatalf("every frame out is one JSON object: %q", payload)
		}
		c.log = append(c.log, doc)
	}
	return op, payload
}

// wait finds a frame that satisfies want, among those already read and then
// among those still to come, and says everything it saw when none did.
func (c *wsClient) wait(what string, want func(map[string]any) bool) map[string]any {
	c.t.Helper()
	for _, doc := range c.log {
		if want(doc) {
			return doc
		}
	}
	for range 400 {
		op, payload := c.read()
		switch op {
		case opText:
			if doc := c.log[len(c.log)-1]; want(doc) {
				return doc
			}
		case opClose:
			c.t.Fatalf("the socket closed with %d before %s arrived:\n%s",
				closeCode(payload), what, c.all())
		}
	}
	c.t.Fatalf("%s never arrived:\n%s", what, c.all())
	return nil
}

func (c *wsClient) waitType(kind string) map[string]any {
	c.t.Helper()
	return c.wait("a "+kind+" frame", func(doc map[string]any) bool { return doc["type"] == kind })
}

// ack is the answer to the frame the client sent under this ref.
func (c *wsClient) ack(ref string) map[string]any {
	c.t.Helper()
	return c.wait("an ack for "+ref, func(doc map[string]any) bool {
		return doc["type"] == "ack" && doc["ref"] == ref
	})
}

// expectClose reads until the close frame, checks the code, and says goodbye
// back so the server lets the connection go rather than waiting for it.
func (c *wsClient) expectClose(want uint16) {
	c.t.Helper()
	for range 400 {
		op, payload := c.read()
		if op != opClose {
			continue
		}
		if got := closeCode(payload); got != want {
			c.t.Fatalf("the socket closed with %d, wanted %d (%q)", got, want, payload)
		}
		c.send(0x80|opClose, []byte{0x03, 0xe8})
		c.conn.Close()
		return
	}
	c.t.Fatalf("no close frame arrived:\n%s", c.all())
}

// all is everything read so far, for an error that has to say what did
// arrive instead.
func (c *wsClient) all() string {
	var b strings.Builder
	for _, doc := range c.log {
		raw, _ := json.Marshal(doc)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func closeCode(payload []byte) uint16 {
	if len(payload) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(payload)
}

/* -------------------------------------------------------------- the tests --- */

// One socket is the whole conversation: the run starts on it, its events come
// out of it, and messages, interrupts and the close go in — each acknowledged
// by the ref the client gave it.
func TestWS_StartsARunAndCarriesItBothWays(t *testing.T) {
	s := start(t, opts{claude: fake.Echo()})
	c := s.dialRun("/v1/accounts/2/ws")

	c.say(map[string]any{"type": "start", "prompt": "p"})
	init := c.waitType("init")
	id, _ := init["run_id"].(string)
	if id == "" {
		t.Fatalf("a run started over a socket is addressable, so it says its id: %v", init)
	}
	t.Cleanup(func() { s.do("POST", "/v1/runs/"+id+"/close", "") })

	c.say(map[string]any{"type": "message", "text": "more", "ref": "c1"})
	ack := c.ack("c1")
	msg, _ := ack["id"].(string)
	if msg == "" || ack["state"] != "accepted" {
		t.Fatalf("a message is acknowledged by its ref, with the id its events will be under: %v", ack)
	}
	c.wait("the message being accepted", func(doc map[string]any) bool {
		return doc["type"] == "input" && doc["id"] == msg && doc["state"] == "accepted"
	})
	c.wait("the answer to it", func(doc map[string]any) bool {
		return doc["type"] == "text" && strings.Contains(str(doc["text"]), "echo: more")
	})
	c.wait("the message being answered", func(doc map[string]any) bool {
		return doc["type"] == "input" && doc["id"] == msg && doc["state"] == "answered"
	})
	c.waitType("idle")

	c.say(map[string]any{"type": "interrupt", "ref": "c2"})
	ack = c.ack("c2")
	if str(ack["id"]) == "" {
		t.Fatalf("an interrupt is acknowledged by id too: %v", ack)
	}
	c.waitType("interrupted")

	c.say(map[string]any{"type": "close", "ref": "c3"})
	ack = c.ack("c3")
	if ack["id"] != nil {
		t.Fatalf("a close has no id: there is no message to have one: %v", ack)
	}
	c.waitType("done")
	c.expectClose(wsNormal)
}

// The socket and the endpoints are two ways to the same run: one started over
// HTTP is picked up on a socket, from where its first reader got to.
func TestWS_AttachesToARunStartedOverHTTP(t *testing.T) {
	s := start(t, opts{claude: fake.Echo()})
	st, id := s.startInput("p")
	st.cancel() // the first reader drops; the run goes on through its grace

	c := s.dialRun("/v1/runs/" + id + "/ws?since=1")
	c.wait("what came after the opening event", func(doc map[string]any) bool {
		if seq, ok := doc["seq"].(float64); ok && seq <= 1 {
			t.Fatalf("since asks for what came after it: %v", doc)
		}
		return strings.Contains(str(doc["text"]), "echo: p")
	})

	c.say(map[string]any{"type": "message", "text": "more", "ref": "r1"})
	if ack := c.ack("r1"); str(ack["id"]) == "" {
		t.Fatalf("a reattached socket sends into the run like any other: %v", ack)
	}
	c.wait("the answer", func(doc map[string]any) bool {
		return strings.Contains(str(doc["text"]), "echo: more")
	})

	c.say(map[string]any{"type": "close"})
	c.waitType("done")
	c.expectClose(wsNormal)
}

// The token is checked before the connection is taken over, so a stranger
// never gets a socket at all. It may travel as a header or as the subprotocol
// a browser can set — and never in the URL, which is written to every log on
// the way.
func TestWS_AuthIsCheckedBeforeTheUpgrade(t *testing.T) {
	s := start(t, opts{claude: fake.Echo()})
	for _, call := range []struct {
		what    string
		path    string
		headers []string
	}{
		{"no token at all", "/v1/ws", nil},
		{"a wrong token in the subprotocol", "/v1/ws", []string{"Sec-WebSocket-Protocol", "rota, bearer.wrong"}},
		{"a token in the query string", "/v1/ws?token=" + s.token, nil},
	} {
		c, resp := s.dialWS(call.path, call.headers...)
		if c != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: the handshake must be refused, and it was %d", call.what, resp.StatusCode)
		}
	}
	// A client that can set a header uses one, and gets the socket.
	c, resp := s.dialWS("/v1/ws", "Authorization", "Bearer "+s.token)
	if c == nil {
		t.Fatalf("a bearer header is a token like any other: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "" {
		t.Fatalf("nothing was offered, so nothing is echoed: %q", got)
	}
	// Nothing was ever asked for on it, so it is simply let go.
	c.send(0x80|opClose, []byte{0x03, 0xe8})
	c.conn.Close()
}

// A start frame this server will not act on costs nothing: it is refused by
// name, in one frame, before any account is spent.
func TestWS_ABadStartIsRefused(t *testing.T) {
	s := start(t, opts{claude: fake.Echo()})
	c := s.dialRun("/v1/accounts/2/ws")

	c.say(map[string]any{"type": "start", "prompt": "p", "nope": 1})
	bad := c.waitType("error")
	if !strings.Contains(str(bad["error"]), "nope") {
		t.Fatalf("a misspelled option is refused by name: %v", bad)
	}
	c.expectClose(wsPolicy)

	// Only claude has a streaming input, and a socket is one by construction,
	// so the account itself can be the thing that is wrong.
	grok := s.dialRun("/v1/accounts/1/ws")
	grok.say(map[string]any{"type": "start", "prompt": "p"})
	if bad := grok.waitType("error"); !strings.Contains(str(bad["error"]), "input") {
		t.Fatalf("a CLI with no streaming input is refused by name: %v", bad)
	}
	grok.expectClose(wsPolicy)

	code, doc := s.call("GET", "/v1/runs", "")
	if runs, _ := doc["runs"].([]any); code != http.StatusOK || len(runs) != 0 {
		t.Fatalf("and nothing ran: %d %v", code, doc)
	}
}
