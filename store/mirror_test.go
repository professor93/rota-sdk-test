package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// --- the claude mirror --------------------------------------------------------

// Claude Code hosts background sessions, the agent view and attached
// sessions in a daemon — one per configuration directory — that logs in for
// itself, so an account sharing the person's directory shares their daemon
// and is billed through their keychain login. From 1.1.0 a claude account
// with no config_dir of its own runs in `<homes>/claude-<id>` instead: the
// person's directory mirrored entry by entry with symlinks, minus the
// daemon's own files.
func TestStoreRun_ClaudeRunsInAMirrorOfTheSharedConfigDirectory(t *testing.T) {
	src := claudeWorld(t)
	fake.CLI(t, "claude", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s","num_turns":1}\n' "$CLAUDE_CONFIG_DIR"
`)
	fake.Registry(t)
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","token":{"accessToken":"tok","expiresAt":0}}]}`)

	res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(b.home, "claude-1")
	if res.Result != "CFG="+own {
		t.Fatalf("the CLI saw %q, want the account's own directory %q", res.Result, own)
	}
	// The settings and the file holding trust decisions are the person's
	// own, reached through links, so the account keeps their whole world.
	for _, name := range []string{"settings.json", ".claude.json"} {
		target, err := os.Readlink(filepath.Join(own, name))
		if err != nil || target != filepath.Join(src, name) {
			t.Fatalf("%s should link to %s: %q %v", name, filepath.Join(src, name), target, err)
		}
	}
	// The daemon's own state is what the mirror exists to keep apart.
	if _, err := os.Lstat(filepath.Join(own, "daemon.lock")); !os.IsNotExist(err) {
		t.Fatalf("the daemon's files must not be shared: %v", err)
	}
}

// An account given a directory of its own asked for a separate world, and
// still gets one: nothing is mirrored into it, and the CLI is pointed there.
func TestStoreRun_ClaudeWithItsOwnConfigDirIsNotMirrored(t *testing.T) {
	claudeWorld(t)
	fake.CLI(t, "claude", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s","num_turns":1}\n' "$CLAUDE_CONFIG_DIR"
`)
	fake.Registry(t)
	mine := t.TempDir()
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","config_dir":"`+mine+`","token":{"accessToken":"tok","expiresAt":0}}]}`)

	res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "CFG="+mine {
		t.Fatalf("the CLI saw %q, want the chosen directory %q", res.Result, mine)
	}
	if _, err := os.Lstat(filepath.Join(mine, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("a chosen directory is not mirrored into: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(b.home, "claude-1")); !os.IsNotExist(err) {
		t.Fatalf("and the unused home stays unused: %v", err)
	}
}

// Conversations are shared through the mirror's links by default, and that
// is a setting rather than a rule. An account whose `sessions` says `own`
// gets no link to the shared transcripts at all: Claude Code makes its own
// inside the mirror, and no other account ever reads them. The rest of the
// world — settings, memory, the file holding trust decisions — is still the
// person's, through the links.
//
// The setting is written as the store's JSON holds it, which is also how an
// older module reads a store a newer rota wrote.
func TestStoreRun_ClaudeKeepingItsConversationsToItselfLinksToNone(t *testing.T) {
	src := claudeWorld(t)
	if err := os.Mkdir(filepath.Join(src, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake.CLI(t, "claude", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s","num_turns":1}\n' "$CLAUDE_CONFIG_DIR"
`)
	fake.Registry(t)
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","sessions":"own","token":{"accessToken":"tok","expiresAt":0}}]}`)

	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(b.home, "claude-1")
	if _, err := os.Lstat(filepath.Join(own, "projects")); !os.IsNotExist(err) {
		t.Fatalf("an account keeping its conversations must have no link to the shared ones: %v", err)
	}
	target, err := os.Readlink(filepath.Join(own, "settings.json"))
	if err != nil || target != filepath.Join(src, "settings.json") {
		t.Fatalf("the rest of the world is still shared: %q %v", target, err)
	}
}

// An account pointed at a folder keeps its conversations there instead, and
// rota makes the folder first — a link to nothing would be pruned on the
// next launch, and Claude Code cannot create a directory through one. Two
// accounts given the same folder share those conversations and no others.
func TestStoreRun_ClaudeConversationsGoWhereTheAccountWasPointed(t *testing.T) {
	claudeWorld(t)
	fake.CLI(t, "claude", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s","num_turns":1}\n' "$CLAUDE_CONFIG_DIR"
`)
	fake.Registry(t)
	folder := filepath.Join(t.TempDir(), "threads") // not there yet
	blob := `{"accounts":[{"id":1,"provider":"claude","sessions":` + quoted(folder) + `,"token":{"accessToken":"tok","expiresAt":0}}]}`
	s, b := seed(t, blob)

	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(b.home, "claude-1")
	target, err := os.Readlink(filepath.Join(own, "projects"))
	if err != nil || target != filepath.Join(folder, "projects") {
		t.Fatalf("projects should link to %s: %q %v", filepath.Join(folder, "projects"), target, err)
	}
	if fi, err := os.Stat(filepath.Join(folder, "projects")); err != nil || !fi.IsDir() {
		t.Fatalf("the folder must be made before it is linked to: %v %v", fi, err)
	}
}

// A real entry where the mirror wants a link is the account's own, and rota
// leaves it exactly as it is however it got there. Claude Code writing a
// shared file through a temporary one and renaming it over the path leaves
// precisely that: a real settings.json where rota's link was, which the
// account then reads and writes instead of the person's. The file surviving
// is the promise from 1.1.0; being told about it is 1.2.0's.
func TestStoreRun_ClaudeMirrorLeavesARealFileWhereALinkBelongs(t *testing.T) {
	src := claudeWorld(t)
	fake.CLI(t, "claude", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s","num_turns":1}\n' "$CLAUDE_CONFIG_DIR"
`)
	fake.Registry(t)
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","token":{"accessToken":"tok","expiresAt":0}}]}`)
	own := filepath.Join(b.home, "claude-1")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(own, "settings.json")
	if err := os.WriteFile(mine, []byte(`{"theme":"its own"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var said []string
	s.Warn = func(msg string) { said = append(said, msg) }

	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(mine)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("a real entry must be left as it is, not replaced by a link: %v %v", fi, err)
	}
	if body, err := os.ReadFile(mine); err != nil || string(body) != `{"theme":"its own"}` {
		t.Fatalf("and its contents: %q %v", body, err)
	}
	// The rest of the person's world is mirrored around it as usual.
	if target, err := os.Readlink(filepath.Join(own, ".claude.json")); err != nil || target != filepath.Join(src, ".claude.json") {
		t.Fatalf(".claude.json should link to %s: %q %v", filepath.Join(src, ".claude.json"), target, err)
	}

	since(t, "1.2.0")
	if len(said) != 1 || !strings.Contains(said[0], "settings.json") || !strings.Contains(said[0], own) {
		t.Fatalf("one warning naming the mirror and the entry in the way: %v", said)
	}
}

// since skips unless the module under test is at least that release. The
// suite runs against the published pin and against a checkout of whatever
// is being written, so a case for behaviour that is not released yet names
// the release it waits for rather than failing on the pin.
func since(t *testing.T, version string) {
	t.Helper()
	if older(rota.Version, version) {
		t.Skipf("rota %s predates %s, which this pins", rota.Version, version)
	}
}

// older reports whether a dotted version reads before another, number by
// number, with a missing number as 0.
func older(a, b string) bool {
	as, bs := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// quoted is a path as JSON carries it, backslashes and all.
func quoted(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// claudeWorld is a stand-in for the person's own Claude Code directory,
// pointed at by the environment so nothing reads the real one.
func claudeWorld(t *testing.T) string {
	t.Helper()
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
	t.Setenv("CLAUDE_CONFIG_DIR", src)
	return src
}
