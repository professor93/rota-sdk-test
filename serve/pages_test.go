package serve_test

import (
	"encoding/json"
	"testing"
)

// rota serves two pages, each in its own route group: the playground, at
// /playground with the rest of the playground group, and the terminal page,
// at /terminal with the terminal group. Either may be served without the
// other. These are about that from the outside, over the published HTTP
// interface, and about the files the two pages are drawn with.

// TestPages_TheTerminalPageComesWithItsGroup: a server whose terminal group
// is off has no /terminal at all, and one with it on has the page whether or
// not there is a playground beside it.
func TestPages_TheTerminalPageComesWithItsGroup(t *testing.T) {
	since(t, "1.2.0")
	off := start(t, opts{})
	if resp, _ := off.do("GET", "/terminal", ""); resp.StatusCode != 404 {
		t.Fatalf("GET /terminal answered %d with the group off, want 404", resp.StatusCode)
	}

	on := start(t, opts{config: terminalConfig()})
	if resp, _ := on.do("GET", "/terminal", ""); resp.StatusCode != 200 {
		t.Fatalf("GET /terminal answered %d with the group on, want 200", resp.StatusCode)
	}

	// And with no playground: a server holding terminals and serving no
	// form over the API still has the page a terminal is watched from.
	alone := start(t, opts{
		ready: "/terminal",
		config: `[routes]
playground = false
terminal = true
`,
	})
	if resp, _ := alone.do("GET", "/terminal", ""); resp.StatusCode != 200 {
		t.Fatalf("GET /terminal answered %d with no playground, want 200", resp.StatusCode)
	}
	for _, path := range []string{"/", "/playground"} {
		if resp, _ := alone.do("GET", path, ""); resp.StatusCode != 404 {
			t.Fatalf("GET %s answered %d with no playground, want 404", path, resp.StatusCode)
		}
	}
}

// TestPages_TheEmulatorIsServedByRotaItself: the terminal page draws with
// xterm.js, and xterm.js is in the binary rather than at a CDN — rota is
// usually on a loopback address with no route out. The files answer with
// their right types where the page is, and are not there where it is not.
func TestPages_TheEmulatorIsServedByRotaItself(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{config: terminalConfig()})
	for _, c := range []struct{ path, ctype string }{
		{"/assets/xterm/xterm.js", "text/javascript; charset=utf-8"},
		{"/assets/xterm/xterm.css", "text/css; charset=utf-8"},
		{"/assets/xterm/addon-fit.js", "text/javascript; charset=utf-8"},
		// What both pages are drawn with, which is served wherever either
		// of them is.
		{"/assets/page.css", "text/css; charset=utf-8"},
		{"/assets/page.js", "text/javascript; charset=utf-8"},
	} {
		resp, raw := s.do("GET", c.path, "")
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d %s", c.path, resp.StatusCode, raw)
		}
		if got := resp.Header.Get("Content-Type"); got != c.ctype {
			t.Errorf("%s is served as %q, want %q", c.path, got, c.ctype)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s is served without nosniff: %q", c.path, got)
		}
		if len(raw) == 0 {
			t.Errorf("%s is empty", c.path)
		}
	}

	// The emulator belongs to the terminal page, so it goes where that page
	// goes; the shared two stay for the playground.
	off := start(t, opts{})
	for _, path := range []string{"/assets/xterm/xterm.js", "/assets/xterm/xterm.css", "/assets/xterm/addon-fit.js"} {
		if resp, _ := off.do("GET", path, ""); resp.StatusCode != 404 {
			t.Errorf("GET %s answered %d with no terminal page, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/assets/page.css", "/assets/page.js"} {
		if resp, _ := off.do("GET", path, ""); resp.StatusCode != 200 {
			t.Errorf("the playground still needs %s: %d", path, resp.StatusCode)
		}
	}
}

// TestPages_TheSchemaSaysWhichPagesThereAre: each page offers the way to the
// other one, and the New terminal form offers a plain shell, only where the
// server actually has them. The schema is how either page knows.
func TestPages_TheSchemaSaysWhichPagesThereAre(t *testing.T) {
	since(t, "1.2.0")
	read := func(s *server) map[string]any {
		t.Helper()
		resp, raw := s.do("GET", "/v1/schema", "")
		if resp.StatusCode != 200 {
			t.Fatalf("GET /v1/schema: %d %s", resp.StatusCode, raw)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	want := func(doc map[string]any, name string, v bool) {
		t.Helper()
		if doc[name] != v {
			t.Errorf("the schema says %s %v, want %v", name, doc[name], v)
		}
	}

	plain := read(start(t, opts{}))
	want(plain, "terminal", false)
	want(plain, "shell", false)
	want(plain, "playground", true)

	term := read(start(t, opts{config: terminalConfig()}))
	want(term, "terminal", true)
	want(term, "shell", false)
	want(term, "playground", true)

	shell := read(start(t, opts{config: `[routes]
terminal = true

[terminal]
shell = true
`}))
	want(shell, "terminal", true)
	want(shell, "shell", true)
}
