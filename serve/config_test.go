// These pin the one file a server is configured by, from outside the
// binary: what it takes from the file, what a flag still beats, what it
// refuses to read at all, and what it says it would serve with.
//
// Nothing here names anything the pinned module does not have — the file is
// written as text and the binary is asked about it over HTTP — so the
// package still compiles against the published version and each test simply
// skips there.

package serve_test

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestConfig_FileGivesTheTokenAndSwitchesThePageOff is the whole point of
// the file in one case: the token is written down instead of typed, and a
// group of routes the file turns off is not there at all.
func TestConfig_FileGivesTheTokenAndSwitchesThePageOff(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{
		noToken: true,
		ready:   "/v1/accounts",
		config: `[auth]
token = "` + token + `"

[routes]
playground = false
`,
	})
	// A group that is off answers like a path this server never had.
	for _, path := range []string{"/", "/playground"} {
		if resp, raw := s.do("GET", path, ""); resp.StatusCode != 404 {
			t.Fatalf("GET %s answered %d, want 404: %s", path, resp.StatusCode, raw)
		}
	}
	// And the API the file left alone still answers, to the token the file
	// gave — which was never on the command line.
	if got := s.list().ids(); len(got) != 2 {
		t.Fatalf("the API must be untouched: %v", got)
	}
	if strings.Contains(s.stderr.String(), "visible to every process") {
		t.Fatalf("a token that was never typed must draw no warning:\n%s", s.stderr.String())
	}
}

// TestConfig_WebSocketOffIsThreeRoutesThatWereNeverThere.
func TestConfig_WebSocketOffIsThreeRoutesThatWereNeverThere(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{config: "[routes]\nwebsocket = false\n"})
	for _, path := range []string{"/v1/ws", "/v1/accounts/1/ws", "/v1/runs/r-1/ws"} {
		if resp, raw := s.do("GET", path, ""); resp.StatusCode != 404 {
			t.Fatalf("GET %s answered %d, want 404: %s", path, resp.StatusCode, raw)
		}
	}
	// The page is told, so it does not offer a run it could not hold.
	resp, raw := s.do("GET", "/v1/schema", "")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	if object(t, raw)["websocket"] != false {
		t.Fatalf("the schema must say the sockets are off: %s", raw)
	}
}

// TestConfig_AFlagBeatsTheFile: a command line is what somebody typed just
// now, so it wins over anything written down earlier.
func TestConfig_AFlagBeatsTheFile(t *testing.T) {
	since(t, "1.2.0")
	// The file alone reaches the server.
	s := start(t, opts{config: "[runs]\nallow_dangerous = true\n"})
	_, raw := s.do("GET", "/v1/schema", "")
	if object(t, raw)["allow_dangerous"] != true {
		t.Fatalf("the file must reach the server: %s", raw)
	}
	// And a flag that says otherwise beats it.
	s = start(t, opts{config: "[runs]\nallow_dangerous = true\n", args: []string{"--allow-dangerous=false"}})
	_, raw = s.do("GET", "/v1/schema", "")
	if object(t, raw)["allow_dangerous"] != false {
		t.Fatalf("a flag must beat the file: %s", raw)
	}
}

// TestConfig_AFileOthersCanReadIsRefused: it may hold the token that
// authorizes running every account on the machine.
func TestConfig_AFileOthersCanReadIsRefused(t *testing.T) {
	since(t, "1.2.0")
	if runtime.GOOS == "windows" {
		t.Skip("windows has neither the bits nor the convention")
	}
	home := t.TempDir()
	path := home + "/server.toml"
	if err := os.WriteFile(path, []byte("[server]\nquiet = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := serveOnce(t, home, "--config", path, "--token", token, "--print-config")
	if code == 0 {
		t.Fatalf("a world-readable file was accepted:\n%s", out)
	}
	if !strings.Contains(out, "chmod 600 "+path) {
		t.Fatalf("the refusal must say the command that fixes it:\n%s", out)
	}
	// The same file, readable by its owner alone, is fine.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := serveOnce(t, home, "--config", path, "--token", token, "--print-config"); code != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
}

// TestConfig_PrintConfigSaysWhereEachValueCameFrom, and never says the
// token: the output has to be something an operator can paste into a bug
// report.
func TestConfig_PrintConfigSaysWhereEachValueCameFrom(t *testing.T) {
	since(t, "1.2.0")
	home := t.TempDir()
	path := home + "/server.toml"
	if err := os.WriteFile(path, []byte("[runs]\ntimeout = \"30s\"\nmax_concurrent = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := serveOnce(t, home, "--config", path, "--token", "a-real-secret",
		"--print-config", "--timeout", "45s")
	if code != 0 {
		t.Fatalf("--print-config must exit 0: %d\n%s", code, out)
	}
	if strings.Contains(out, "a-real-secret") {
		t.Fatalf("the token was printed:\n%s", out)
	}
	for _, want := range []string{
		`timeout         = "45s"`, "# flag --timeout",
		"max_concurrent  = 3", "# file",
		"replay          = 1000", "# default",
		`token           = "(set)"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--print-config must show %q:\n%s", want, out)
		}
	}
	// And a file that is not there is a mistake, not a default.
	if out, code := serveOnce(t, home, "--config", home+"/nowhere.toml", "--print-config"); code == 0 {
		t.Fatalf("a named file that is not there was accepted:\n%s", out)
	}
}
