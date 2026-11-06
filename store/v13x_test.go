package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/store"
	"rotatest/internal/fake"
	"rotatest/internal/release"
)

// What 1.3.0, 1.3.1 and 1.3.2 promise about a claude account's home, seen
// through the store: a login of its own written there, one owner per login,
// the person's directory told to everything launched, one home per account,
// and since 1.3.2 the rescue of a home whose daemon gave up or whose store
// Claude Code blanked. Each case names the release it pins.

// --- the world these cases run in -------------------------------------------

// keptHome skips where a claude account keeps no login in its home: before
// version, and on Windows, which keeps the environment route.
func keptHome(t *testing.T, version string) {
	t.Helper()
	since(t, version)
	if runtime.GOOS == "windows" {
		t.Skip("windows keeps claude on the environment route")
	}
}

// personsWorld stands in for the person's own Claude Code directory, named
// both ways a store reads it, so nothing reads the real one whatever started
// this test, and keeps the case offline.
func personsWorld(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	for name, body := range map[string]string{"settings.json": `{"theme":"dark"}`, ".claude.json": `{"projects":{}}`} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", src)
	t.Setenv("ROTA_CLAUDE_HOME", src)
	offline(t)
	return src
}

// offline points every claude endpoint at a fake server that knows no path,
// and gives rota its client, which dials nothing off this machine: a case
// that expects no request reaches no real service even when rota makes one.
// A server a case installs after it wins, and is restored first.
func offline(t *testing.T) {
	t.Helper()
	fake.NewServer(t).Claude(t)
}

// echoClaude is a claude CLI that answers with the directory it was pointed
// at and the token it was handed, "none" when there is none.
func echoClaude(t *testing.T) {
	t.Helper()
	fake.CLI(t, "claude", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"CFG=%s TOKEN=%s"}\n' "$CLAUDE_CONFIG_DIR" "${CLAUDE_CODE_OAUTH_TOKEN:-none}"
`)
	fake.Registry(t)
}

// claudeStore is a store holding one claude account, c@x (uuid u1), whose
// login can be kept in its home: A0/R0, expiring at expires, with a usage
// reading too fresh to read again. extra is spliced into the account.
func claudeStore(t *testing.T, expires time.Time, extra string) (*store.Store, *memBackend, *rota.Account) {
	t.Helper()
	if extra != "" {
		extra = "," + extra
	}
	s, b := seed(t, fmt.Sprintf(`{"accounts":[{"id":1,"provider":"claude","email":"c@x","uuid":"u1",
 "quotaAt":%d,"quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
 "token":{"accessToken":"A0","refreshToken":"R0","expiresAt":%d}%s}]}`, time.Now().UnixMilli(), expires.UnixMilli(), extra))
	return s, b, s.Find(1)
}

// tokenServer is a fake provider whose token endpoint answers with reply; the
// server counts what it was asked.
func tokenServer(t *testing.T, reply fake.Handler) *fake.Server {
	t.Helper()
	srv := fake.NewServer(t)
	srv.Claude(t)
	srv.Handle("/token", reply)
	return srv
}

func rotatesTo(access, refresh string) fake.Handler {
	return func(*http.Request, map[string]any) (int, any) {
		return 200, fake.ClaudeToken(access, refresh, 8*3600, "u1", "c@x", "")
	}
}

// liveWindow records a Claude Code window alive in home, the way Claude Code
// records one: sessions/<n>.json naming a running pid, this test's own. It
// returns what closes the window.
func liveWindow(t *testing.T, home string) (close func()) {
	t.Helper()
	path := filepath.Join(home, "sessions", "1.json")
	writeIn(t, path, fmt.Sprintf(`{"pid":%d,"kind":"interactive"}`, os.Getpid()))
	return func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func writeIn(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// homeLogin is a home's credential store: the login entry, and the store
// as raw text.
func homeLogin(t *testing.T, home string) (login map[string]any, raw string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".credentials.json"))
	if err != nil {
		t.Fatalf("the home's credential store: %v", err)
	}
	var doc struct {
		Login map[string]any `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the home's credential store: %v\n%s", err, b)
	}
	return doc.Login, string(b)
}

// credentialStore is a credential store holding a login, and an MCP
// server's token beside it, which every write must keep.
func credentialStore(access, refresh string, expires time.Time, until int64) string {
	l := map[string]any{"accessToken": access, "refreshToken": refresh, "expiresAt": expires.UnixMilli(), "scopes": []string{"user:inference"}}
	if until != 0 {
		l["refreshTokenExpiresAt"] = until
	}
	raw, _ := json.Marshal(map[string]any{"mcpOAuth": map[string]any{"srv": map[string]any{"accessToken": "mcp-secret"}}, "claudeAiOauth": l})
	return string(raw)
}

// blankedStore is the store as Claude Code leaves it when a refresh of its
// own was refused: the login emptied, everything else kept.
const blankedStore = `{"mcpOAuth":{"srv":{"accessToken":"mcp-secret"}},"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0,"scopes":["user:inference"]}}`

// collectWarnings collects what the store says.
func collectWarnings(s *store.Store) *[]string {
	var said []string
	s.Warn = func(msg string) { said = append(said, msg) }
	return &said
}

func hoursFromNow(n float64) time.Time { return time.Now().Add(time.Duration(n * float64(time.Hour))) }

// --- 1.3.0: a login of its own -----------------------------------------------

// A launch in the account's home writes the account's login into Claude
// Code's own credential store there and puts nothing that authenticates in
// the environment.
func TestStoreRun_ClaudeLaunchesOnALoginOfItsOwnInItsHome(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	res, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	if res.Result != "CFG="+home+" TOKEN=none" {
		t.Fatalf("the CLI saw %q, want its home and no token", res.Result)
	}
	login, _ := homeLogin(t, home)
	if login["accessToken"] != "A0" || login["refreshToken"] != "R0" {
		t.Fatalf("the account's own login: %v", login)
	}
	if fi, err := os.Stat(filepath.Join(home, ".credentials.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("private to the person: %v %v", fi, err)
	}
}

// A write replaces only the login: Claude Code keeps the OAuth tokens of MCP
// servers in the same store, and they stay exactly as they were.
func TestStoreRun_ClaudeLoginWriteKeepsTheRestOfTheStore(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	home := filepath.Join(b.home, "claude-1")
	writeIn(t, filepath.Join(home, ".credentials.json"), `{"mcpOAuth":{"srv":{"accessToken":"mcp-secret","expiresAt":1.50}}}`)
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	login, raw := homeLogin(t, home)
	if login["refreshToken"] != "R0" || !strings.Contains(raw, `"mcpOAuth":{"srv":{"accessToken":"mcp-secret","expiresAt":1.50}}`) {
		t.Fatalf("the login written beside the MCP token, which is untouched:\n%s", raw)
	}
}

// While Claude Code runs in the home, the home owns the login: rota reads
// it, launches on what is there and writes nothing, and the account's login
// goes in the first time nothing runs there.
func TestStoreRun_ClaudeHomeWithSomethingAliveIsReadNotWritten(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	home := filepath.Join(b.home, "claude-1")
	before := credentialStore("A-old", "R-old", hoursFromNow(-1), 0)
	writeIn(t, filepath.Join(home, ".credentials.json"), before)
	closeWindow := liveWindow(t, home)
	said := collectWarnings(s)

	res, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "CFG="+home+" TOKEN=none" {
		t.Fatalf("launched on what the home holds: %q", res.Result)
	}
	if _, raw := homeLogin(t, home); raw != before {
		t.Fatalf("nothing written while Claude Code runs there:\n%s", raw)
	}
	if line := strings.Join(*said, "\n"); !strings.Contains(line, a.String()) || !strings.Contains(line, "running in its home") {
		t.Fatalf("and it says so: %q", *said)
	}

	closeWindow()
	s2 := nextStore(t, b)
	if _, err := s2.Run(ctx, s2.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if login, raw := homeLogin(t, home); login["refreshToken"] != "R0" || !strings.Contains(raw, "mcp-secret") {
		t.Fatalf("the first quiet launch writes the account's login:\n%s", raw)
	}
}

// A refresh by Claude Code keeps the login's refreshTokenExpiresAt and is
// taken with no network call, and saved.
func TestStoreRun_ClaudeTakesWhatClaudeCodeRotatedWithNoNetwork(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	srv := tokenServer(t, rotatesTo("A-no", "R-no"))
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	writeIn(t, filepath.Join(home, ".credentials.json"), credentialStore("A1", "R1", hoursFromNow(8), 0))

	s2 := nextStore(t, b)
	if _, err := s2.Run(ctx, s2.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := s2.Find(1).Token; got.Access != "A1" || got.Refresh != "R1" {
		t.Fatalf("the rotation is the account's now: %+v", got)
	}
	if !strings.Contains(string(b.blob), `"R1"`) {
		t.Fatalf("and saved:\n%s", b.blob)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Fatalf("with no network call: %d requests", n)
	}
}

// A login with another refreshTokenExpiresAt is somebody's /login inside
// the home. It is checked with the provider before anything of it is taken:
// the same account's is taken; another account's is refused, the account's
// own tokens stay as they were, and rota names both.
func TestStoreRefresh_ClaudeLoginMadeInsideTheHomeIsCheckedFirst(t *testing.T) {
	keptHome(t, "1.3.0")
	until := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	for _, c := range []struct {
		name, uuid, email string
		taken             bool
	}{
		{"the same account", "u1", "c@x", true},
		{"another account", "u2", "other@x", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			personsWorld(t)
			echoClaude(t)
			srv := tokenServer(t, rotatesTo("A-no", "R-no"))
			srv.Handle("/profile", func(r *http.Request, _ map[string]any) (int, any) {
				if r.Header.Get("Authorization") != "Bearer A9" {
					return 401, fake.Reply{"error": "unauthorized"}
				}
				return 200, fake.ClaudeProfile(c.uuid, c.email, "org")
			})
			s, b, a := claudeStore(t, hoursFromNow(1), `"extra":{"refresh_token_expires_at":"`+strconv.FormatInt(until, 10)+`"}`)
			if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(b.home, "claude-1")
			writeIn(t, filepath.Join(home, ".credentials.json"), credentialStore("A9", "R9", hoursFromNow(8), until+86_400_000))

			s2 := nextStore(t, b)
			said := collectWarnings(s2)
			a = s2.Find(1)
			errs := s2.Refresh(ctx, false, a)
			words := strings.Join(*said, "\n")
			for _, err := range errs {
				words += "\n" + err.Error()
			}
			switch {
			case srv.Hits("/profile") != 1:
				t.Fatalf("asked the provider once: %d", srv.Hits("/profile"))
			case c.taken && (a.Token.Refresh != "R9" || a.Token.Access != "A9"):
				t.Fatalf("the account's own new login is taken: %+v (%s)", a.Token, words)
			case !c.taken && (a.Token.Refresh != "R0" || a.Token.Access != "A0"):
				t.Fatalf("somebody else's is not: %+v", a.Token)
			case !c.taken && (!strings.Contains(words, "other@x") || !strings.Contains(words, "c@x")):
				t.Fatalf("and rota names both: %q", words)
			case srv.Hits("/token") != 0:
				t.Fatalf("no refresh on the way: %d", srv.Hits("/token"))
			}
		})
	}
}

// An account told a configuration directory of the person's choosing stays
// on a token in its environment: rota reads no credential store there and
// writes none, changes no .claude.json, and refuses it Remote Control and
// says why.
func TestStoreRun_ClaudeInAChosenDirectoryStaysOnAToken(t *testing.T) {
	since(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	mine := t.TempDir()
	theirs := credentialStore("A-theirs", "R-theirs", hoursFromNow(8), 0)
	writeIn(t, filepath.Join(mine, ".credentials.json"), theirs)
	writeIn(t, filepath.Join(mine, ".claude.json"), `{"oauthAccount":{"emailAddress":"me@person"}}`)
	s, _, a := claudeStore(t, hoursFromNow(1), `"config_dir":`+quoted(mine))
	res, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "CFG="+mine+" TOKEN=A0" {
		t.Fatalf("the chosen directory and a token: %q", res.Result)
	}
	if a.Token.Access != "A0" || a.Token.Refresh != "R0" {
		t.Fatalf("nothing is read from the credential store there: %+v", a.Token)
	}
	if _, raw := homeLogin(t, mine); raw != theirs {
		t.Fatalf("nor written:\n%s", raw)
	}
	if body, _ := os.ReadFile(filepath.Join(mine, ".claude.json")); string(body) != `{"oauthAccount":{"emailAddress":"me@person"}}` {
		t.Fatalf("nor its .claude.json changed: %s", body)
	}
	want := *a
	want.RemoteControl = true
	err = s.CheckRemoteControl(&want)
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "configuration directory") {
		t.Fatalf("Remote Control is refused, saying why: %v", err)
	}
	if err := s.CheckRemoteControl(a); err != nil {
		t.Fatalf("with it off there is nothing to refuse: %v", err)
	}
}

// One home, one account: a directory that is already another account's
// home is refused under any spelling, and on a volume that ignores case under
// any case.
func TestStoreCheckHome_RefusesAnotherAccountsHomeUnderAnySpelling(t *testing.T) {
	since(t, "1.3.0")
	theirs := filepath.Join(t.TempDir(), "Home")
	if err := os.Mkdir(theirs, 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := seed(t, `{"accounts":[
 {"id":1,"provider":"claude","config_dir":`+quoted(theirs)+`,"token":{"accessToken":"a"}},
 {"id":2,"provider":"grok","token":{"accessToken":"g"}}]}`)
	spellings := []string{theirs, theirs + string(filepath.Separator), filepath.Join(theirs, "x", "..") + string(filepath.Separator) + "."}
	if upper := filepath.Join(filepath.Dir(theirs), "HOME"); oneFile(upper, theirs) {
		spellings = append(spellings, upper) // this volume ignores case
	}
	for _, dir := range spellings {
		want := *s.Find(2)
		want.ConfigDir = dir
		if err := s.CheckHome(&want); !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "already") {
			t.Errorf("%q is account 1's home: %v", dir, err)
		}
	}
	want := *s.Find(2)
	want.ConfigDir = t.TempDir()
	if err := s.CheckHome(&want); err != nil {
		t.Fatalf("a directory of its own: %v", err)
	}
}

// Two accounts that already share one home are not launched there, and the
// CLI never starts.
func TestStoreRun_TwoAccountsToldOneHomeAreNotLaunched(t *testing.T) {
	since(t, "1.3.0")
	personsWorld(t)
	ran := filepath.Join(t.TempDir(), "ran")
	fake.CLI(t, "claude", "cat >/dev/null\n: > "+ran+"\n")
	fake.Registry(t)
	shared := t.TempDir()
	s, _ := seed(t, `{"accounts":[
 {"id":1,"provider":"claude","config_dir":`+quoted(shared)+`,"token":{"accessToken":"a","expiresAt":0}},
 {"id":2,"provider":"claude","config_dir":`+quoted(shared)+`,"token":{"accessToken":"b","expiresAt":0}}]}`)
	_, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("refused: %v", err)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatal("the CLI must not start")
	}
}

// A window open in the home of a claude account refuses its removal, and
// nothing is touched; an account in a directory the person chose is never
// refused, and its directory loses nothing.
func TestStoreRemovable_AWindowOpenInItsHomeRefuses(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	mine := t.TempDir()
	writeIn(t, filepath.Join(mine, "settings.json"), `{"mine":true}`)
	s, b := seed(t, `{"accounts":[
 {"id":1,"provider":"claude","token":{"accessToken":"a","refreshToken":"r","expiresAt":1}},
 {"id":2,"provider":"claude","config_dir":`+quoted(mine)+`,"token":{"accessToken":"b","expiresAt":0}}]}`)
	home := filepath.Join(b.home, "claude-1")
	liveWindow(t, home)
	liveWindow(t, mine)

	err := s.Removable(s.Find(1))
	if !errors.Is(err, rota.ErrBusy) || !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("refused, naming what runs there: %v", err)
	}
	if err := s.Remove(1); !errors.Is(err, rota.ErrBusy) {
		t.Fatalf("Remove refuses the same: %v", err)
	}
	if s.Find(1) == nil {
		t.Fatal("the account is still there")
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("and so is its home: %v", err)
	}

	if err := s.Removable(s.Find(2)); err != nil {
		t.Fatalf("a directory the person chose is never refused: %v", err)
	}
	if err := s.Remove(2); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(mine, "settings.json")); err != nil || string(body) != `{"mine":true}` {
		t.Fatalf("and loses nothing: %q %v", body, err)
	}
}

// Giving a claude account a directory of the person's choosing reads the
// home rota kept its login in first, keeps any rotation found there, then
// takes the login out of that home, and saves.
func TestStoreMoveHome_ReadsTheOldHomeFirstAndTakesTheLoginOut(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	srv := tokenServer(t, rotatesTo("A-no", "R-no"))
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	writeIn(t, filepath.Join(home, ".credentials.json"), credentialStore("A1", "R1", hoursFromNow(8), 0))

	s2 := nextStore(t, b)
	a = s2.Find(1)
	mine := t.TempDir()
	if err := s2.MoveHome(ctx, a, mine); err != nil {
		t.Fatal(err)
	}
	if a.Token.Refresh != "R1" || a.ConfigDir != mine {
		t.Fatalf("the rotation found there is kept, and the directory taken: %+v %q", a.Token, a.ConfigDir)
	}
	if login, err := os.ReadFile(filepath.Join(home, ".credentials.json")); err == nil && strings.Contains(string(login), "R1") {
		t.Fatalf("the login is taken out of the old home:\n%s", login)
	}
	if !strings.Contains(string(b.blob), `"R1"`) || !strings.Contains(string(b.blob), quoted(mine)) {
		t.Fatalf("and saved:\n%s", b.blob)
	}
	if srv.Hits("/token") != 0 {
		t.Fatal("no refresh on the way")
	}
}

// It is refused while Claude Code runs there, and a refused move leaves the
// account and the home as they were.
func TestStoreMoveHome_RefusedWhileClaudeCodeRunsThere(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	liveWindow(t, home)
	s2 := nextStore(t, b)
	a = s2.Find(1)
	if err := s2.MoveHome(ctx, a, t.TempDir()); err == nil {
		t.Fatal("refused while something runs there")
	}
	if a.ConfigDir != "" {
		t.Fatalf("the account is as it was: %q", a.ConfigDir)
	}
	if login, _ := homeLogin(t, home); login["refreshToken"] != "R0" {
		t.Fatalf("and so is its home: %v", login)
	}
}

// ROTA_CLAUDE_HOME, when set, says which Claude Code directory is the
// person's, whoever started this process: a rota started inside a session
// of an account's inherits that account's home as CLAUDE_CONFIG_DIR.
func TestPersonalClaudeDir_ROTACLAUDEHOMEIsTheAnswer(t *testing.T) {
	since(t, "1.3.0")
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	def := filepath.Join(fakeHome, ".claude")
	mine, accounts := t.TempDir(), t.TempDir()
	cases := []struct {
		name                      string
		rotaClaudeHome, accountID *string
		configDir                 string
		want                      string
	}{
		{"named", str(mine), nil, accounts, mine},
		{"set but empty is the default", str(""), nil, accounts, def},
		{"inside another rota's launch", nil, str("3"), accounts, def},
		{"a person's own CLAUDE_CONFIG_DIR", nil, nil, mine, mine},
	}
	for _, c := range cases {
		setOrUnset(t, "ROTA_CLAUDE_HOME", c.rotaClaudeHome)
		setOrUnset(t, "ROTA_ACCOUNT_ID", c.accountID)
		t.Setenv("CLAUDE_CONFIG_DIR", c.configDir)
		if got := store.PersonalClaudeDir(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Everything rota launches, whatever its CLI, is told whose Claude Code
// directory is the person's: the directory when a variable named it, set but
// empty when it is the default.
func TestStoreRun_EveryCLIIsToldWhoseClaudeDirectoryIsYours(t *testing.T) {
	since(t, "1.3.0")
	dir := fake.CLI(t, "t-cli", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"HOME=%s SET=%s"}\n' "$ROTA_CLAUDE_HOME" "${ROTA_CLAUDE_HOME+yes}"
`)
	fake.Registry(t)
	p := fake.New("t-other")
	p.BaseEnv = fake.BaseEnv(dir)
	rota.Register(fake.Claude(p))
	mine := t.TempDir()
	for _, c := range []struct {
		name string
		env  *string
		want string
	}{
		{"named", str(mine), "HOME=" + mine + " SET=yes"},
		{"the default", str(""), "HOME= SET=yes"},
	} {
		setOrUnset(t, "ROTA_CLAUDE_HOME", c.env)
		s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-other","token":{"accessToken":"tok"}}]}`)
		res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Result != c.want {
			t.Errorf("%s: the CLI saw %q, want %q", c.name, res.Result, c.want)
		}
	}
}

// Besides the login and the daemon's files, what Claude Code keeps per login
// is each account's own in every mode, never linked from the person's
// directory: telemetry, policy-limits.json, statsig,
// mcp-needs-auth-cache.json, bridge-spawn and every *.lock. A link an older
// rota made for one of them goes on the first launch that finds the home
// quiet, and not while Claude Code runs there.
func TestStoreRun_ClaudeMirrorKeepsWhatBelongsToOneLoginApart(t *testing.T) {
	keptHome(t, "1.3.0")
	src := personsWorld(t)
	private := []string{"telemetry", "statsig", "bridge-spawn", "policy-limits.json", "mcp-needs-auth-cache.json", "history.lock"}
	for _, name := range private {
		if strings.Contains(name, ".") {
			writeIn(t, filepath.Join(src, name), "{}")
		} else if err := os.Mkdir(filepath.Join(src, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	home := filepath.Join(b.home, "claude-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "telemetry"), filepath.Join(home, "telemetry")); err != nil {
		t.Fatal(err) // what an older rota left
	}
	closeWindow := liveWindow(t, home)

	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(home, "settings.json")); err != nil || target != filepath.Join(src, "settings.json") {
		t.Fatalf("the person's settings are linked as ever: %q %v", target, err)
	}
	for _, name := range private[1:] {
		if target, err := os.Readlink(filepath.Join(home, name)); err == nil {
			t.Errorf("%s is the login's own, never linked: -> %s", name, target)
		}
	}
	if _, err := os.Readlink(filepath.Join(home, "telemetry")); err != nil {
		t.Fatalf("an old link stays while Claude Code runs there: %v", err)
	}

	closeWindow()
	s2 := nextStore(t, b)
	if _, err := s2.Run(ctx, s2.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(home, "telemetry")); err == nil {
		t.Fatalf("and goes on the first quiet launch: -> %s", target)
	}
}

// With Remote Control on the account keeps its own .claude.json: a copy of
// the person's without their identity, given the account's. Every launch
// folds in what the person's gains, one way and only by adding. Off, the copy
// is put aside, never deleted, and the person's file is linked again.
func TestStoreRun_ClaudeRemoteControlKeepsAClaudeJSONOfItsOwn(t *testing.T) {
	keptHome(t, "1.3.0")
	src := personsWorld(t)
	writeIn(t, filepath.Join(src, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"me","emailAddress":"me@person"},"mcpServers":{"m1":{"command":"one"}},"projects":{}}`)
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), `"remoteControl":true`)
	if err := s.RemoteControlNow(a); err != nil {
		t.Fatalf("a quiet home can have it at its next launch: %v", err)
	}
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	own := filepath.Join(home, ".claude.json")
	doc := jsonAt(t, own)
	id, _ := doc["oauthAccount"].(map[string]any)
	if id["accountUuid"] != "u1" || id["emailAddress"] != "c@x" || strings.Contains(fmt.Sprint(doc), "me@person") {
		t.Fatalf("its own identity, not the person's: %v", doc["oauthAccount"])
	}
	if !strings.Contains(fmt.Sprint(doc["mcpServers"]), "m1") {
		t.Fatalf("the person's MCP servers come with it: %v", doc["mcpServers"])
	}
	if fi, err := os.Lstat(own); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("a file of its own, not a link: %v", err)
	}

	writeIn(t, filepath.Join(src, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"me","emailAddress":"me@person"},"mcpServers":{"m1":{"command":"one"},"m2":{"command":"two"}},"projects":{}}`)
	s2 := nextStore(t, b)
	if _, err := s2.Run(ctx, s2.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if doc := jsonAt(t, own); !strings.Contains(fmt.Sprint(doc["mcpServers"]), "m2") {
		t.Fatalf("what the person's file gains is folded in: %v", doc["mcpServers"])
	}

	s3 := nextStore(t, b)
	s3.Find(1).RemoteControl = false
	if _, err := s3.Run(ctx, s3.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(own); err != nil || target != filepath.Join(src, ".claude.json") {
		t.Fatalf("off, the person's file is linked again: %q %v", target, err)
	}
	if aside := jsonAt(t, filepath.Join(home, ".claude.json.own")); !strings.Contains(fmt.Sprint(aside), "u1") {
		t.Fatalf("and the copy is put aside: %v", aside)
	}
}

// RemoteControlNow says whether the setting can work by the next launch:
// nil when it can, ErrUnsupported when the account does not run on a login
// of its own in its home, ErrBusy when it waits for running sessions to end.
func TestStoreRemoteControlNow_SaysWhyItCannotWorkYet(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	s, b := seed(t, fmt.Sprintf(`{"accounts":[
 {"id":1,"provider":"claude","remoteControl":true,"token":{"accessToken":"a","refreshToken":"r","expiresAt":%d}},
 {"id":2,"provider":"claude","remoteControl":true,"dead":true,"token":{"accessToken":"a","refreshToken":"r","expiresAt":1}},
 {"id":3,"provider":"claude","remoteControl":true,"token":{"accessToken":"a","expiresAt":0}}]}`, hoursFromNow(1).UnixMilli()))
	if err := s.RemoteControlNow(s.Find(1)); err != nil {
		t.Fatalf("a quiet home: %v", err)
	}
	liveWindow(t, filepath.Join(b.home, "claude-1"))
	if err := s.RemoteControlNow(s.Find(1)); !errors.Is(err, rota.ErrBusy) {
		t.Fatalf("waiting for the running sessions: %v", err)
	}
	for _, id := range []int{2, 3} {
		if err := s.RemoteControlNow(s.Find(id)); !errors.Is(err, rota.ErrUnsupported) {
			t.Errorf("account %d runs on no login of its own: %v", id, err)
		}
	}
}

// A dead account is refreshed nowhere, but when its home holds a login made
// inside Claude Code that cannot be confirmed yet, it still launches there,
// on that login, and is alive again once the provider confirms it is its own.
func TestStoreLaunchable_ADeadAccountsHomeSignedInAgainLaunchesUntilConfirmed(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	var answers atomic.Bool
	srv := tokenServer(t, rotatesTo("A-no", "R-no"))
	srv.Handle("/profile", func(*http.Request, map[string]any) (int, any) {
		if !answers.Load() {
			return 503, fake.Reply{"error": "overloaded"}
		}
		return 200, fake.ClaudeProfile("u1", "c@x", "org")
	})
	s, b, a := claudeStore(t, hoursFromNow(-2), `"dead":true,"deadReason":"invalid_grant"`)
	home := filepath.Join(b.home, "claude-1")
	writeIn(t, filepath.Join(home, ".credentials.json"), credentialStore("A9", "R9", hoursFromNow(8), hoursFromNow(24*90).UnixMilli()))

	if err := s.Launchable(ctx, a); err != nil {
		t.Fatalf("let through while the login waits to be confirmed: %v", err)
	}
	res, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "CFG="+home+" TOKEN=none" {
		t.Fatalf("launched in its home, on that login: %q", res.Result)
	}
	if login, _ := homeLogin(t, home); login["refreshToken"] != "R9" {
		t.Fatalf("which is left as it is: %v", login)
	}

	answers.Store(true)
	s2 := nextStore(t, b)
	a = s2.Find(1)
	s2.Refresh(ctx, false, a)
	if a.Dead || a.Token.Refresh != "R9" {
		t.Fatalf("confirmed, the account is alive on it: dead=%v %+v", a.Dead, a.Token)
	}
	if srv.Hits("/token") != 0 {
		t.Fatalf("and its refused token was never presented: %d", srv.Hits("/token"))
	}
}

// --- 1.3.2: a home that is owed a refresh -------------------------------------

// blankedHome is the home of the store's account as Claude Code leaves it
// after a refresh of its own was refused: the account's current login,
// written there by the application, blanked.
func blankedHome(t *testing.T, b *memBackend, a *rota.Account) string {
	t.Helper()
	home := filepath.Join(b.home, "claude-1")
	writeIn(t, filepath.Join(home, ".credentials.json"), blankedStore)
	a.StagedWritten()
	return home
}

// A store Claude Code blanked no longer makes an account dead by itself: the
// account presents its own refresh token once, whatever runs there, the login
// goes back in beside what Claude Code kept, and rota says so in one line.
func TestStoreRefresh_ClaudeBlankedLoginIsRescuedOnce(t *testing.T) {
	keptHome(t, "1.3.2")
	personsWorld(t)
	srv := tokenServer(t, rotatesTo("A1", "R1"))
	s, b, a := claudeStore(t, hoursFromNow(-1), "")
	home := blankedHome(t, b, a)
	liveWindow(t, home)
	said := collectWarnings(s)

	if errs := s.Refresh(ctx, false, a); len(errs) != 0 {
		t.Fatal(errs)
	}
	if srv.Hits("/token") != 1 || a.Dead || a.Token.Refresh != "R1" {
		t.Fatalf("one refresh, alive: %d dead=%v %+v", srv.Hits("/token"), a.Dead, a.Token)
	}
	if got := srv.Requests()[0].Body["refresh_token"]; got != "R0" {
		t.Fatalf("the account's own refresh token is presented: %v", got)
	}
	login, raw := homeLogin(t, home)
	if login["refreshToken"] != "R1" || !strings.Contains(raw, "mcp-secret") {
		t.Fatalf("the login is back beside what Claude Code kept:\n%s", raw)
	}
	if line := strings.Join(*said, "\n"); !strings.Contains(line, a.String()) || !strings.Contains(line, "cleared") {
		t.Fatalf("and it says so: %q", *said)
	}
	if !strings.Contains(string(b.blob), `"R1"`) {
		t.Fatalf("saved:\n%s", b.blob)
	}
	s.Refresh(ctx, false, a)
	if srv.Hits("/token") != 1 {
		t.Fatalf("once: %d", srv.Hits("/token"))
	}
}

// A refusal of that token is final: the account is dead, says why, and the
// token is never presented again, by a listing, maintenance or a launch.
func TestStoreRefresh_ClaudeBlankedLoginRefusedIsDeadAndSaysWhy(t *testing.T) {
	keptHome(t, "1.3.2")
	personsWorld(t)
	echoClaude(t)
	srv := tokenServer(t, func(*http.Request, map[string]any) (int, any) {
		return 400, fake.OAuthReject("invalid_grant", "refresh token revoked")
	})
	s, b, a := claudeStore(t, hoursFromNow(-1), "")
	home := blankedHome(t, b, a)

	s.Refresh(ctx, false, a)
	const why = "Claude Code cleared the login in its home, and the provider refused rota's own refresh token too"
	if !a.Dead || !strings.Contains(a.DeadReason, why) || !strings.Contains(a.DeadReason, "invalid_grant") {
		t.Fatalf("dead, and says why: %v %q", a.Dead, a.DeadReason)
	}
	if _, raw := homeLogin(t, home); raw != blankedStore {
		t.Fatalf("nothing written:\n%s", raw)
	}
	if !strings.Contains(string(b.blob), why) {
		t.Fatalf("saved, reason and all:\n%s", b.blob)
	}
	s.Refresh(ctx, true, a)
	s.Maintain(ctx)
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("a launch is refused: %v", err)
	}
	if srv.Hits("/token") != 1 {
		t.Fatalf("the refused token is never presented again: %d", srv.Hits("/token"))
	}
}

// Any other failure — an error the provider answered with, or no answer —
// leaves the account alive; the look says so, and the next one tries again.
func TestStoreRefresh_ClaudeBlankedLoginUnansweredStaysAlive(t *testing.T) {
	keptHome(t, "1.3.2")
	personsWorld(t)
	var answered atomic.Bool
	srv := tokenServer(t, func(r *http.Request, body map[string]any) (int, any) {
		if !answered.Load() {
			return 503, fake.Reply{"error": "overloaded"}
		}
		return rotatesTo("A1", "R1")(r, body)
	})
	s, b, a := claudeStore(t, hoursFromNow(-1), "")
	home := blankedHome(t, b, a)

	errs := s.Refresh(ctx, false, a)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), a.String()) || !strings.Contains(errs[0].Error(), "cleared") {
		t.Fatalf("says so: %v", errs)
	}
	if a.Dead || a.Token.Refresh != "R0" {
		t.Fatalf("alive and as it was: dead=%v %+v", a.Dead, a.Token)
	}
	answered.Store(true)
	if errs := s.Refresh(ctx, false, a); len(errs) != 0 {
		t.Fatal(errs)
	}
	if login, _ := homeLogin(t, home); login["refreshToken"] != "R1" || srv.Hits("/token") != 2 {
		t.Fatalf("the next look gets through: %v after %d", login, srv.Hits("/token"))
	}
}

// strandedHome is the home of the store's account holding its current login,
// whose daemon reported auth_required since gaveUp.
func strandedHome(t *testing.T, b *memBackend, a *rota.Account, gaveUp time.Time) string {
	t.Helper()
	home := filepath.Join(b.home, "claude-1")
	writeIn(t, filepath.Join(home, ".credentials.json"), credentialStore("A0", "R0", time.UnixMilli(a.Token.ExpiresAt), 0))
	a.StagedWritten()
	writeIn(t, filepath.Join(home, "daemon-auth-status.json"), fmt.Sprintf(`{"status":"auth_required","since":%d}`, gaveUp.UnixMilli()))
	return home
}

// A home whose daemon has given up on its login is refreshed whatever runs
// there, once its token truly expired ten minutes ago, and the login is
// written into the store. A token not that far gone, or no report from a
// daemon, leaves the home to Claude Code.
func TestStoreRefresh_ClaudeStrandedHomeIsRefreshedWhateverRunsThere(t *testing.T) {
	keptHome(t, "1.3.2")
	for _, c := range []struct {
		name    string
		expired time.Duration
		report  bool
		rescued bool
	}{
		{"stranded", 20 * time.Minute, true, true},
		{"expired a moment ago", 3 * time.Minute, true, false},
		{"no daemon said anything", 20 * time.Minute, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			personsWorld(t)
			srv := tokenServer(t, rotatesTo("A1", "R1"))
			s, b, a := claudeStore(t, time.Now().Add(-c.expired), "")
			home := strandedHome(t, b, a, time.Now().Add(-c.expired+time.Minute))
			if !c.report {
				if err := os.Remove(filepath.Join(home, "daemon-auth-status.json")); err != nil {
					t.Fatal(err)
				}
			}
			liveWindow(t, home)
			said := collectWarnings(s)
			s.Refresh(ctx, false, a)
			login, _ := homeLogin(t, home)
			switch {
			case c.rescued && (srv.Hits("/token") != 1 || login["refreshToken"] != "R1"):
				t.Fatalf("refreshed and written: %d %v", srv.Hits("/token"), login)
			case c.rescued && !strings.Contains(strings.Join(*said, "\n"), "given up"):
				t.Fatalf("and said in one line: %q", *said)
			case !c.rescued && (srv.Hits("/token") != 0 || login["refreshToken"] != "R0"):
				t.Fatalf("left to Claude Code: %d %v", srv.Hits("/token"), login)
			}
		})
	}
}

// While rota refreshes and writes it holds Claude Code's own refresh lock,
// .oauth_refresh.lock in the home. A lock somebody holds leaves the look to
// them, and is neither touched nor removed; one its holder left long ago is
// cleared, and rota's own goes when it is done.
func TestStoreRefresh_ClaudeRescueKeepsToClaudeCodesRefreshLock(t *testing.T) {
	keptHome(t, "1.3.2")
	personsWorld(t)
	srv := tokenServer(t, rotatesTo("A1", "R1"))
	s, b, a := claudeStore(t, hoursFromNow(-1), "")
	home := blankedHome(t, b, a)
	lock := filepath.Join(home, ".oauth_refresh.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	s.Refresh(ctx, false, a)
	if srv.Hits("/token") != 0 {
		t.Fatalf("a held lock leaves the look to its holder: %d", srv.Hits("/token"))
	}
	if fi, err := os.Stat(lock); err != nil || !fi.IsDir() {
		t.Fatalf("and is left alone: %v", err)
	}
	if a.Dead {
		t.Fatal("nothing is decided meanwhile")
	}

	stale := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	s.Refresh(ctx, false, a)
	if login, _ := homeLogin(t, home); srv.Hits("/token") != 1 || login["refreshToken"] != "R1" {
		t.Fatalf("a lock left long ago is cleared and the look goes on: %d %v", srv.Hits("/token"), login)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("rota's own lock goes when it is done: %v", err)
	}
}

// --- 1.3.1 and 1.3.2: for the SDK ---------------------------------------------
//
// The methods these releases added are reached through interfaces, so the
// suite still builds against a module that predates them; from the release
// that promises one, a module without it fails.

type rescuer interface {
	Rescue(ctx context.Context, accounts ...*rota.Account) []error
}

type watchable interface {
	Watchable(a *rota.Account) bool
}

type whyDead interface{ WhyDead() string }

type stagedCurrent interface{ StagedCurrent() bool }

// promised is v as T when the module under test has it, failing from the
// release that promises it and skipping before.
func promised[T any](t *testing.T, version string, v any) T {
	t.Helper()
	got, ok := v.(T)
	switch {
	case ok:
		return got
	case release.Older(rota.Version, version):
		t.Skipf("rota %s predates %s, which this pins", rota.Version, version)
	default:
		t.Fatalf("rota %s has no %T, which %s promises", rota.Version, (*T)(nil), version)
	}
	return got
}

// Store.Rescue does what a home is owed and reads no usage, for a listing
// that reads none; the account then knows its home holds its current login.
func TestStoreRescue_DoesWhatAHomeIsOwedAndReadsNoUsage(t *testing.T) {
	keptHome(t, "1.3.2")
	personsWorld(t)
	srv := tokenServer(t, rotatesTo("A1", "R1"))
	srv.Handle("/usage", func(*http.Request, map[string]any) (int, any) { return 200, fake.ClaudeUsage(1, 1, "") })
	s, b, a := claudeStore(t, hoursFromNow(-1), "")
	rescue := promised[rescuer](t, "1.3.2", s)
	home := blankedHome(t, b, a)
	staged := promised[stagedCurrent](t, "1.3.1", a)
	if !staged.StagedCurrent() {
		t.Fatal("the account's current login was written there")
	}
	if errs := rescue.Rescue(ctx, a); len(errs) != 0 {
		t.Fatal(errs)
	}
	if login, _ := homeLogin(t, home); login["refreshToken"] != "R1" || srv.Hits("/token") != 1 {
		t.Fatalf("rescued: %v after %d", login, srv.Hits("/token"))
	}
	if srv.Hits("/usage") != 0 {
		t.Fatalf("and no usage read: %d", srv.Hits("/usage"))
	}
	if !staged.StagedCurrent() {
		t.Fatal("the home holds the current login again")
	}

	if errs := rescue.Rescue(ctx, a); len(errs) != 0 || srv.Hits("/token") != 1 {
		t.Fatalf("a home owed nothing asks no provider anything: %v %d", errs, srv.Hits("/token"))
	}
}

// Store.Watchable says a watcher has something to keep: a claude account
// whose login lives in its home, alive, whose last launch there took the
// stored route.
func TestStoreWatchable_OnlyALoginKeptInItsHome(t *testing.T) {
	keptHome(t, "1.3.2")
	personsWorld(t)
	echoClaude(t)
	exp := hoursFromNow(1).UnixMilli()
	s, _ := seed(t, fmt.Sprintf(`{"accounts":[
 {"id":1,"provider":"claude","token":{"accessToken":"a","refreshToken":"r","expiresAt":%d}},
 {"id":2,"provider":"claude","config_dir":%s,"token":{"accessToken":"a","refreshToken":"r","expiresAt":%d}},
 {"id":3,"provider":"claude","token":{"accessToken":"a","expiresAt":0}},
 {"id":4,"provider":"grok","token":{"accessToken":"g"}}]}`, exp, quoted(t.TempDir()), exp))
	w := promised[watchable](t, "1.3.2", s)
	if w.Watchable(s.Find(1)) {
		t.Fatal("not before a launch took the stored route")
	}
	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !w.Watchable(s.Find(1)) {
		t.Fatal("a claude login kept in its home, launched on it")
	}
	for _, id := range []int{2, 3, 4} {
		if w.Watchable(s.Find(id)) {
			t.Errorf("account %d has nothing a watcher could keep", id)
		}
	}
	s.Find(1).Dead = true
	if w.Watchable(s.Find(1)) {
		t.Fatal("nor a dead one")
	}
}

// Account.WhyDead is why an account needs a login, in words for a person:
// the reason it kept, or for one an older rota marked dead, that much.
func TestAccountWhyDead_SaysWhyOrThatNobodyKeptIt(t *testing.T) {
	a := rota.NewAccount(1, "claude", &rota.Token{Access: "A"})
	why := promised[whyDead](t, "1.3.1", a)
	if why.WhyDead() != "" {
		t.Fatalf("alive: %q", why.WhyDead())
	}
	a.Dead = true
	if !strings.Contains(why.WhyDead(), "no reason") {
		t.Fatalf("an older rota kept none: %q", why.WhyDead())
	}
	a.DeadReason = "invalid_grant"
	if !strings.Contains(why.WhyDead(), "invalid_grant") {
		t.Fatalf("the reason kept: %q", why.WhyDead())
	}
}

// --- helpers -------------------------------------------------------------------

// nextStore is a second store over the same backend, as the next command would
// open it, once the first has let go of the lock.
func nextStore(t *testing.T, b *memBackend) *store.Store {
	t.Helper()
	return open(t, b)
}

func jsonAt(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, raw)
	}
	return doc
}

func oneFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func str(s string) *string { return &s }

// setOrUnset sets a variable for the test, or unsets it for the test when v
// is nil.
func setOrUnset(t *testing.T, name string, v *string) {
	t.Helper()
	if v != nil {
		t.Setenv(name, *v)
		return
	}
	t.Setenv(name, "")
	os.Unsetenv(name)
}

// The long-lived token is for runs without a home: a launch in the
// account's home runs on its stored login whether or not one is there, and
// a hermetic run, which takes a throwaway directory, goes on a token in its
// environment, the long one when there is one.
func TestStoreRun_ClaudeLongTokenIsForRunsWithoutAHome(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	s, b, a := claudeStore(t, hoursFromNow(1), fmt.Sprintf(`"long":{"accessToken":"LONG","expiresAt":%d}`, hoursFromNow(24*300).UnixMilli()))
	res, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if home := filepath.Join(b.home, "claude-1"); res.Result != "CFG="+home+" TOKEN=none" {
		t.Fatalf("in its home, on its stored login: %q", res.Result)
	}
	s2 := nextStore(t, b)
	res, err = s2.Run(ctx, s2.Find(1), rota.Spec{Prompt: "hi", Hermetic: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Result, " TOKEN=LONG") || strings.Contains(res.Result, "claude-1") {
		t.Fatalf("a hermetic run goes on the long token, in a directory of its own: %q", res.Result)
	}
}

// A store whose lock a run has released — the reading taken after a run —
// rotates nothing: no refresh request, nothing written into the home.
func TestStoreRefresh_AfterARunReleasedTheLockRotatesNothing(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	srv := tokenServer(t, rotatesTo("A1", "R1"))
	s, b, a := claudeStore(t, hoursFromNow(1), "")
	if _, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	_, written := homeLogin(t, home)
	a.Token.ExpiresAt = hoursFromNow(-1).UnixMilli() // due, as a long run leaves it
	s.Refresh(ctx, true, a)
	if srv.Hits("/token") != 0 || a.Token.Refresh != "R0" {
		t.Fatalf("no refresh: %d %+v", srv.Hits("/token"), a.Token)
	}
	if _, raw := homeLogin(t, home); raw != written {
		t.Fatalf("nothing written:\n%s", raw)
	}
}

// Clearing a chosen directory gives the account the home rota keeps for it,
// written from nothing on its next launch.
func TestStoreMoveHome_ClearingAChosenDirectoryGivesBackRotasHome(t *testing.T) {
	keptHome(t, "1.3.0")
	personsWorld(t)
	echoClaude(t)
	mine := t.TempDir()
	s, b, a := claudeStore(t, hoursFromNow(1), `"config_dir":`+quoted(mine))
	if err := s.MoveHome(ctx, a, ""); err != nil {
		t.Fatal(err)
	}
	if a.ConfigDir != "" {
		t.Fatalf("cleared: %q", a.ConfigDir)
	}
	res, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(b.home, "claude-1")
	if res.Result != "CFG="+home+" TOKEN=none" {
		t.Fatalf("its next launch is in rota's home, on its own login: %q", res.Result)
	}
	if login, _ := homeLogin(t, home); login["refreshToken"] != "R0" {
		t.Fatalf("written there: %v", login)
	}
}
