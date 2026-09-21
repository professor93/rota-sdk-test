package serve_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code hosts background sessions, the agent view and attached
// sessions in a daemon — one per configuration directory — which logs in for
// itself. A server that ran every claude account in the person's own
// directory shared that daemon with them, and the daemon's keychain login is
// what paid for everything it hosted.
//
// From 1.1.0 each claude account without a config_dir of its own runs in
// `<ROTA_HOME>/homes/claude-<id>`, which rota keeps as a mirror of the
// person's directory: a symlink to every entry, and to their .claude.json,
// minus the daemon's own files. The account keeps the whole world and gets a
// daemon of its own.
func TestRun_ClaudeRunsInAMirrorOfTheServersConfigDirectory(t *testing.T) {
	src := t.TempDir()
	for name, body := range map[string]string{
		"settings.json": `{"theme":"dark"}`,
		".claude.json":  `{"projects":{}}`,
		"daemon.lock":   "",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := start(t, opts{
		claude: `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s","num_turns":1,"total_cost_usd":0.01}\n' "$CLAUDE_CONFIG_DIR"
`,
		env: []string{"CLAUDE_CONFIG_DIR=" + src},
	})

	code, reply, raw := s.run(2, `{"prompt":"hi"}`)
	if code != 200 || reply.IsError {
		t.Fatalf("status %d: %s", code, raw)
	}
	own := filepath.Join(s.home, "homes", "claude-2")
	if reply.Result != "CFG="+own {
		t.Fatalf("the CLI saw %q, want the account's own directory %q", reply.Result, own)
	}
	for _, name := range []string{"settings.json", ".claude.json"} {
		target, err := os.Readlink(filepath.Join(own, name))
		if err != nil || target != filepath.Join(src, name) {
			t.Fatalf("%s should link to %s: %q %v", name, filepath.Join(src, name), target, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(own, "daemon.lock")); !os.IsNotExist(err) {
		t.Fatalf("the daemon's own files must stay behind: %v", err)
	}
}

// Where a claude account's conversations live is a setting like the rest,
// so a caller sets it over HTTP: `own` keeps them to the account, a
// directory puts them there, and `shared` — the default — stores nothing.
// A run afterwards links the account's mirror into whatever was said.
func TestPatchAccount_SessionsSaysWhereConversationsLive(t *testing.T) {
	s := start(t, opts{
		claude: `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"ok","num_turns":1,"total_cost_usd":0.01}\n'
`,
	})
	if code, doc, raw := patch(s, "2", `{"sessions":"own"}`); code != 200 || doc["sessions"] != "own" {
		t.Fatalf("own: %d %s", code, raw)
	}
	if got := s.list().Accounts[1].Sessions; got != "own" {
		t.Fatalf("the listing must carry it too: %q", got)
	}

	folder := filepath.Join(t.TempDir(), "threads")
	if code, doc, raw := patch(s, "2", `{"sessions":`+quote(folder)+`}`); code != 200 || doc["sessions"] != folder {
		t.Fatalf("a folder: %d %s", code, raw)
	}
	if code, reply, raw := s.run(2, `{"prompt":"hi"}`); code != 200 || reply.IsError {
		t.Fatalf("status %d: %s", code, raw)
	}
	own := filepath.Join(s.home, "homes", "claude-2")
	target, err := os.Readlink(filepath.Join(own, "projects"))
	if err != nil || target != filepath.Join(folder, "projects") {
		t.Fatalf("projects should link to %s: %q %v", filepath.Join(folder, "projects"), target, err)
	}
	if fi, err := os.Stat(filepath.Join(folder, "projects")); err != nil || !fi.IsDir() {
		t.Fatalf("the folder must be made before it is linked to: %v %v", fi, err)
	}

	// shared is the default and is stored as nothing at all.
	if code, doc, raw := patch(s, "2", `{"sessions":"shared"}`); code != 200 || doc["sessions"] != nil {
		t.Fatalf("shared: %d %s", code, raw)
	}
	// And a relative path is refused, as every other directory over HTTP is.
	if code, _, raw := patch(s, "2", `{"sessions":"threads"}`); code != 400 || !strings.Contains(string(raw), "absolute") {
		t.Fatalf("relative: %d %s", code, raw)
	}
}
