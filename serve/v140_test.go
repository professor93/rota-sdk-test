// These pin what rota 1.4.0's betas added that the binary shows from
// outside: a server that logs to a file of its owner's and rotates it
// (beta.3), the one sentence for a file only its owner may use (beta.3),
// and the two credentials a person pastes — a Console API key and a token
// from `claude setup-token` — over HTTP and on the command line, neither
// ever taken from an argument or repeated (beta.1).
//
// Nothing here names anything the pinned module lacks, so the package still
// compiles against it, and each test skips on a binary older than the beta
// that brought what it pins.
//
// Nothing reaches the network. A key's one check against the API goes to a
// proxy on the loopback address that refuses every CONNECT, which is the
// "could not be asked" the key is then taken with. The claude accounts here
// name a configuration directory of their own, so no look into an account's
// home ever asks the macOS keychain for a login.

package serve_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- the log file (1.4.0-beta.3) ----------------------------------------------

// TestLogFile_TheTerminalStaysQuiet: with --log-file, everything the
// server says goes to the file, its own plain lines dated, and nothing to
// the terminal. The file is made for its owner alone.
func TestLogFile_TheTerminalStaysQuiet(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	path := filepath.Join(t.TempDir(), "rota.log")
	s := start(t, opts{accounts: keyedStore(t), args: []string{"--log-file", path}})

	// A request with no token is the one thing every server logs.
	req, _ := http.NewRequest("GET", s.url+"/v1/accounts", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
	log := waitForLog(t, path, "rejected request")
	if got := s.stderr.String(); got != "" {
		t.Fatalf("the terminal must hear nothing while the file is written:\n%s", got)
	}
	first := strings.SplitN(log, "\n", 2)[0]
	if !dated.MatchString(first) || !strings.Contains(first, "serving on "+s.url) {
		t.Fatalf("the start-up line is the file's first, dated: %q", first)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the log is its owner's alone: %v %v", fi, err)
	}
}

// TestLogFile_AppendsToWhatIsThere: a restart adds to the log rather
// than starting it again.
func TestLogFile_AppendsToWhatIsThere(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	path := filepath.Join(t.TempDir(), "rota.log")
	if err := os.WriteFile(path, []byte("the server before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	start(t, opts{accounts: keyedStore(t), args: []string{"--log-file", path}})
	log := waitForLog(t, path, "serving on")
	if !strings.HasPrefix(log, "the server before\n") {
		t.Fatalf("what was there must stay first:\n%s", log)
	}
}

// TestLogFile_QuietStillDecidesHowMuch: the file takes what stderr
// would have, and --quiet trims it the same way.
func TestLogFile_QuietStillDecidesHowMuch(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	for _, quiet := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "rota.log")
		args := []string{"--log-file", path}
		if quiet {
			args = append(args, "--quiet")
		}
		s := start(t, opts{accounts: keyedStore(t), args: args})
		if resp, raw := s.do("POST", "/v1/login", `{"account":2,"token":"`+pastedSetup+`"}`); resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, raw)
		}
		log := waitForLog(t, path, "serving on")
		if logged := strings.Contains(log, "long-lived token stored"); logged == quiet {
			t.Fatalf("quiet=%v, and the file says the token was stored: %v\n%s", quiet, logged, log)
		}
		if stillSecret(log) {
			t.Fatalf("the token itself is never logged:\n%s", log)
		}
	}
}

// TestLogFile_ComesFromTheFileAndAFlagBeatsIt: log_file is a key in
// [server] as well as a flag, and the flag wins, as every flag does.
func TestLogFile_ComesFromTheFileAndAFlagBeatsIt(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	dir := t.TempDir()
	fromFile, fromFlag := filepath.Join(dir, "file.log"), filepath.Join(dir, "flag.log")
	config := "[server]\nlog_file = " + strconv.Quote(fromFile) + "\n"

	s := start(t, opts{accounts: keyedStore(t), config: config})
	waitForLog(t, fromFile, "serving on")
	if got := s.stderr.String(); got != "" {
		t.Fatalf("the file's log_file keeps the terminal quiet too:\n%s", got)
	}

	other := t.TempDir()
	config = "[server]\nlog_file = " + strconv.Quote(filepath.Join(other, "file.log")) + "\n"
	start(t, opts{accounts: keyedStore(t), config: config, args: []string{"--log-file", fromFlag}})
	waitForLog(t, fromFlag, "serving on")
	if _, err := os.Stat(filepath.Join(other, "file.log")); !os.IsNotExist(err) {
		t.Fatalf("the flag beats the file, which is never opened: %v", err)
	}

	// --print-config says which one it would use, and why.
	home := t.TempDir()
	cfg := filepath.Join(home, "server.toml")
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := serveOnce(t, home, "--config", cfg, "--token", token, "--print-config", "--log-file", fromFlag)
	if code != 0 || !regexp.MustCompile(`log_file\s*= `+regexp.QuoteMeta(strconv.Quote(fromFlag))+`\s*# flag --log-file`).MatchString(out) {
		t.Fatalf("--print-config must show the flag's log_file and where it came from: %d\n%s", code, out)
	}
}

// TestLogFile_HasNoEnvironmentVariable: like quiet, it is a key and a
// flag and nothing else, so a variable named for it changes nothing.
func TestLogFile_HasNoEnvironmentVariable(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	path := filepath.Join(t.TempDir(), "rota.log")
	s := start(t, opts{accounts: keyedStore(t), env: []string{"ROTA_LOG_FILE=" + path}})
	if !strings.Contains(s.stderr.String(), "serving on") {
		t.Fatalf("the log stays on stderr:\n%s", s.stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no file is made: %v", err)
	}
}

// TestLogFile_RefusesAPathThatIsNotAPlainFileOfItsOwn: the path is
// absolute, its directory is there, and what is at it is a plain file only
// its owner can use. A link is never followed — the file it names, which
// could be a credential, is left as it was — and a FIFO is never waited on.
// Each refusal is said on stderr, the server having nowhere else yet.
func TestLogFile_RefusesAPathThatIsNotAPlainFileOfItsOwn(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	dir := t.TempDir()
	secret := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(secret, []byte(`{"secret":"kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.log")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v %s", err, out)
	}
	open := filepath.Join(dir, "open.log")
	if err := os.WriteFile(open, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ what, path, says string }{
		{"a relative path", "rota.log", "absolute"},
		{"a directory that is not there", filepath.Join(dir, "nowhere", "rota.log"), "not an existing directory"},
		{"a link", link, "is a link"},
		{"a FIFO", fifo, "not a plain file"},
		{"a file others can read", open, "readable or writable by other users"},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, stderr, code := runRota(t, t.TempDir(), "", nil,
				"serve", freeAddr(t), "--token", token, "--log-file", c.path)
			if code == 0 || !strings.Contains(stderr, c.says) {
				t.Fatalf("refused on stderr with %q, got %d:\n%s", c.says, code, stderr)
			}
		})
	}
	if raw, err := os.ReadFile(secret); err != nil || string(raw) != `{"secret":"kept"}` {
		t.Fatalf("the file a link named is untouched: %q %v", raw, err)
	}
	if _, stderr, _ := runRota(t, t.TempDir(), "", nil, "serve", freeAddr(t), "--token", token, "--log-file", open); !strings.Contains(stderr, "chmod 600 "+open) {
		t.Fatalf("the refusal says the command that fixes it:\n%s", stderr)
	}
}

// TestLogFile_AServerThatCannotStartSaysWhyOnStderr: the reason goes
// into the log, and to the terminal as well, because nobody is reading a
// log of a server that never started.
func TestLogFile_AServerThatCannotStartSaysWhyOnStderr(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	addr := taken.Addr().String()
	path := filepath.Join(t.TempDir(), "rota.log")
	_, stderr, code := runRota(t, t.TempDir(), "", nil, "serve", addr, "--token", token, "--log-file", path)
	if code == 0 || !strings.Contains(stderr, addr) {
		t.Fatalf("a server that cannot listen says so on stderr: %d\n%s", code, stderr)
	}
	log, _ := os.ReadFile(path)
	if !regexp.MustCompile(`(?m)^` + dated.String() + `error: .*` + regexp.QuoteMeta(addr)).Match(log) {
		t.Fatalf("and in its log, dated:\n%s", log)
	}
}

// TestLogFile_RotatesAtTwentyMiBKeepingFour: a log that would pass
// 20 MiB moves to <name>.1, every older one moves up, and the oldest of
// four goes. The new file is its owner's alone like the first.
func TestLogFile_RotatesAtTwentyMiBKeepingFour(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	path := filepath.Join(t.TempDir(), "rota.log")
	full := fullLog(t, path)
	for i := 1; i <= 4; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", path, i), []byte(fmt.Sprintf("old %d\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start(t, opts{accounts: keyedStore(t), args: []string{"--log-file", path}})
	log := waitForLog(t, path, "serving on")
	if int64(len(log)) >= full {
		t.Fatalf("the log starts again after it moves: %d bytes", len(log))
	}
	if fi, err := os.Stat(path + ".1"); err != nil || fi.Size() != full {
		t.Fatalf("the full log is <name>.1 now: %v %v", fi, err)
	}
	for i, want := range map[int]string{2: "old 1\n", 3: "old 2\n", 4: "old 3\n"} {
		if raw, err := os.ReadFile(fmt.Sprintf("%s.%d", path, i)); err != nil || string(raw) != want {
			t.Fatalf("<name>.%d: %q %v, want %q", i, raw, err, want)
		}
	}
	if _, err := os.Stat(path + ".5"); !os.IsNotExist(err) {
		t.Fatalf("four old files are kept, no more: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the new log is its owner's alone: %v %v", fi, err)
	}
}

// TestLogFile_ALeftoverFoldJoinsTheChain: a rotation a crash cut short
// leaves <name>.0, or <name>.folding as well; on the next start both join
// the chain as the newest old files, the .folding one the older of the
// two, and nothing is written over.
func TestLogFile_ALeftoverFoldJoinsTheChain(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	path := filepath.Join(t.TempDir(), "rota.log")
	for suffix, body := range map[string]string{".0": "left at .0\n", ".folding": "left folding\n", ".1": "old 1\n"} {
		if err := os.WriteFile(path+suffix, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start(t, opts{accounts: keyedStore(t), args: []string{"--log-file", path}})
	waitForLog(t, path, "serving on")
	for i, want := range map[int]string{1: "left at .0\n", 2: "left folding\n", 3: "old 1\n"} {
		if raw, err := os.ReadFile(fmt.Sprintf("%s.%d", path, i)); err != nil || string(raw) != want {
			t.Fatalf("<name>.%d: %q %v, want %q", i, raw, err, want)
		}
	}
	for _, gone := range []string{".0", ".folding"} {
		if _, err := os.Stat(path + gone); !os.IsNotExist(err) {
			t.Fatalf("<name>%s has joined the chain: %v", gone, err)
		}
	}
}

// TestLogFile_ARotationThatCannotMoveTouchesNothingOlder: in a
// directory that refuses renames the log cannot move, so no older file is
// shifted or dropped and the log goes on growing.
func TestLogFile_ARotationThatCannotMoveTouchesNothingOlder(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	if os.Geteuid() == 0 {
		t.Skip("root renames in a directory nobody may write")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rota.log")
	full := fullLog(t, path)
	if err := os.WriteFile(path+".1", []byte("old 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	start(t, opts{accounts: keyedStore(t), args: []string{"--log-file", path}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(path); err == nil && fi.Size() > full {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the log that cannot move goes on growing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if raw, err := os.ReadFile(path + ".1"); err != nil || string(raw) != "old 1\n" {
		t.Fatalf("no older file is touched: %q %v", raw, err)
	}
	for _, none := range []string{".0", ".2"} {
		if _, err := os.Stat(path + none); !os.IsNotExist(err) {
			t.Fatalf("nothing moves: <name>%s %v", none, err)
		}
	}
}

// --- one sentence for a file only its owner may use (1.4.0-beta.3) -------------

// TestConfig_AFileOnlyItsOwnerMayUseIsRefusedInOneSentence: server.toml
// and the file auth.token_file names are refused in the log file's words,
// which say what the check has always looked at: writable counts as well as
// readable.
func TestConfig_AFileOnlyItsOwnerMayUseIsRefusedInOneSentence(t *testing.T) {
	gateRelease(t, "1.4.0-beta.3")
	home := t.TempDir()
	config := filepath.Join(home, "server.toml")
	if err := os.WriteFile(config, []byte("[server]\nquiet = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(config, 0o620); err != nil { // writable by the group, readable by nobody else
		t.Fatal(err)
	}
	out, code := serveOnce(t, home, "--config", config, "--token", token, "--print-config")
	if code == 0 || !strings.Contains(out, config+" is readable or writable by other users") || !strings.Contains(out, "chmod 600 "+config) {
		t.Fatalf("server.toml: %d\n%s", code, out)
	}

	tokenFile := filepath.Join(home, "token")
	if err := os.WriteFile(tokenFile, []byte("a-token-from-a-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tokenFile, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[auth]\ntoken_file = "+strconv.Quote(tokenFile)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(config, 0o600); err != nil {
		t.Fatal(err)
	}
	out, code = serveOnce(t, home, "--config", config, "--print-config")
	if code == 0 || !strings.Contains(out, "auth.token_file: "+tokenFile+" is readable or writable by other users") ||
		!strings.Contains(out, "chmod 600 "+tokenFile) {
		t.Fatalf("auth.token_file: %d\n%s", code, out)
	}
}

// --- pasted credentials over HTTP (1.4.0-beta.1) -------------------------------

// TestLogin_ASetupTokenGoesOnTheAccountNamed: a token from `claude
// setup-token` becomes that account's long-lived one, good for a year from
// now, and the answer says what rota cannot know about it. It never makes
// an account, goes only on a claude.ai login, and is never repeated.
func TestLogin_ASetupTokenGoesOnTheAccountNamed(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	s := start(t, opts{accounts: keyedStore(t)})
	resp, raw := s.do("POST", "/v1/login", `{"account":2,"token":"`+pastedSetup+`"}`)
	doc := object(t, raw)
	if resp.StatusCode != 200 || doc["status"] != "long" || doc["id"] != float64(2) ||
		!strings.Contains(fmt.Sprint(doc["note"]), "cannot tell which claude.ai account") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	until, err := time.Parse(time.RFC3339, fmt.Sprint(doc["long_until"]))
	if year := time.Now().AddDate(1, 0, 0); err != nil || until.Before(year.Add(-48*time.Hour)) || until.After(year.Add(48*time.Hour)) {
		t.Fatalf("good for a year from now: %v %v", doc["long_until"], err)
	}
	if stored := storedAccount(t, s.home, 2); !strings.Contains(string(stored["long"]), pastedSetup) {
		t.Fatalf("the token is the account's long one: %s", stored["long"])
	}

	for _, body := range []string{
		`{"token":"` + pastedSetup + `"}`,                         // no account named
		`{"account":1,"token":"` + pastedSetup + `"}`,             // grok takes none
		`{"account":3,"token":"` + pastedSetup + `"}`,             // an account on a key takes none
		`{"account":2,"token":"` + pastedSetup + `","long":true}`, // nothing else beside it
		`{"key":"` + pastedSetup + `"}`,                           // and it is no key
	} {
		resp, raw := s.do("POST", "/v1/login", body)
		if resp.StatusCode != 400 || stillSecret(string(raw)) {
			t.Fatalf("%s: %d %s", body, resp.StatusCode, raw)
		}
	}
	if stillSecret(s.stderr.String()) {
		t.Fatalf("the token is never logged:\n%s", s.stderr.String())
	}
}

// TestLogin_AKeyMakesAnAccountOutOfTheRotation: a Console API key is
// an account of its own, at order 0 because every run on it costs money,
// named by its label or by the key's last four characters and nothing
// more. Each paste asks the API about the key once; when it cannot be
// asked the key is taken with a warning. The same key again is the same
// account.
func TestLogin_AKeyMakesAnAccountOutOfTheRotation(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	proxy, connects := refusingProxy(t)
	s := start(t, opts{accounts: keyedStore(t), env: []string{"HTTPS_PROXY=" + proxy}})

	resp, raw := s.do("POST", "/v1/login", `{"key":"`+pastedKey+`","label":"work"}`)
	doc := object(t, raw)
	if resp.StatusCode != 200 || doc["status"] != "added" || doc["credential"] != "apikey" ||
		doc["order"] != float64(0) || doc["label"] != "work" || doc["warning"] == nil {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	if got := connects(); len(got) != 1 || !strings.HasPrefix(got[0], "CONNECT api.anthropic.com:443 ") {
		t.Fatalf("the paste asks the API about the key once: %q", got)
	}
	id := doc["id"]

	resp, raw = s.do("POST", "/v1/login", `{"key":"`+pastedKey+`"}`)
	doc = object(t, raw)
	if resp.StatusCode != 200 || doc["status"] != "updated" || doc["id"] != id || doc["label"] != "work" {
		t.Fatalf("the same key is the same account, its label kept: %d %s", resp.StatusCode, raw)
	}
	asked := len(connects())

	_, listing := s.do("GET", "/v1/accounts", "")
	var l struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(listing, &l); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	for _, a := range l.Accounts {
		if a["id"] == id {
			row = a
		}
	}
	if row == nil || row["credential"] != "apikey" || row["name"] != "work" || row["order"] != float64(0) {
		t.Fatalf("listed as a key account, out of the rotation: %s", listing)
	}

	for _, body := range []string{
		`{"key":"nothing-like-a-key"}`,
		`{"key":"` + pastedKey + `","long":true}`,
		`{"key":"` + pastedKey + `","account":2}`,
		`{"label":"no key"}`,
	} {
		resp, raw := s.do("POST", "/v1/login", body)
		if resp.StatusCode != 400 || stillSecret(string(raw)) {
			t.Fatalf("%s: %d %s", body, resp.StatusCode, raw)
		}
	}
	if len(connects()) != asked {
		t.Fatalf("a refusal by shape asks nobody: %q", connects())
	}
	if stillSecret(string(listing)) || stillSecret(s.stderr.String()) {
		t.Fatalf("the key is never shown or logged:\n%s\n%s", listing, s.stderr.String())
	}
	if fi, err := os.Stat(filepath.Join(s.home, "accounts.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("it is kept with every other credential, 0600: %v %v", fi, err)
	}
}

// TestAccounts_AKeyAccountSaysSoAndNeverShowsItsKey: the listing gains
// "credential":"apikey" for an account on a key, and "name" when it has
// one; an ordinary account is rendered as it was.
func TestAccounts_AKeyAccountSaysSoAndNeverShowsItsKey(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	s := start(t, opts{accounts: keyedStore(t)})
	resp, raw := s.do("GET", "/v1/accounts", "")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	var l struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	rows := map[float64]map[string]any{}
	for _, a := range l.Accounts {
		rows[a["id"].(float64)] = a
	}
	for _, id := range []float64{1, 2} {
		if _, ok := rows[id]["credential"]; ok {
			t.Fatalf("an ordinary account carries no credential field: %v", rows[id])
		}
	}
	if rows[3]["credential"] != "apikey" || rows[3]["name"] != "ci key" || rows[3]["metered"] != false {
		t.Fatalf("a named key account: %v", rows[3])
	}
	if rows[4]["credential"] != "apikey" || rows[4]["metered"] != false {
		t.Fatalf("an unnamed key account: %v", rows[4])
	}
	if strings.Contains(string(raw), "SEEDEDONLY") {
		t.Fatalf("no key is ever listed:\n%s", raw)
	}
}

// TestPatchAccount_AKeyIsNeverTakenAsADirectory: a directory is kept and
// listed again, so a key sent as one is refused before it is stored, and
// not repeated. An account on a key takes no Remote Control, which Claude
// Code refuses for keys.
func TestPatchAccount_AKeyIsNeverTakenAsADirectory(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	s := start(t, opts{accounts: keyedStore(t)})
	for _, field := range []string{"cwd", "config_dir", "sessions"} {
		resp, raw := s.do("PATCH", "/v1/accounts/2", `{"`+field+`":"/srv/`+pastedKey+`"}`)
		if resp.StatusCode != 400 || !strings.Contains(string(raw), "looks like a key") || stillSecret(string(raw)) {
			t.Fatalf("%s: %d %s", field, resp.StatusCode, raw)
		}
	}
	if _, raw := s.do("GET", "/v1/accounts", ""); stillSecret(string(raw)) {
		t.Fatalf("nothing was kept:\n%s", raw)
	}
	resp, raw := s.do("PATCH", "/v1/accounts/3", `{"remote_control":true}`)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "API key") {
		t.Fatalf("remote control on a key account: %d %s", resp.StatusCode, raw)
	}
}

// --- pasted credentials on the command line (1.4.0-beta.1) ---------------------

// TestCommandLine_AKeyAsAnArgumentIsNeverRepeated: wherever an argument goes —
// a command, an account id, a run id, a flag's value, a prompt, a
// directory, or the credential's own flag — a key typed there is refused
// without being repeated, and nothing is stored.
func TestCommandLine_AKeyAsAnArgumentIsNeverRepeated(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	home := storeDir(t, keyedStore(t))
	before, _ := os.ReadFile(filepath.Join(home, "accounts.json"))
	for _, argv := range [][]string{
		{"login", pastedKey},
		{pastedKey},
		{"login", "--key=" + pastedKey},
		{"login", "--key", pastedKey},
		{"login", "2", "--token", pastedSetup},
		{"login", "--token=" + pastedSetup},
		{"set", pastedKey, "--order", "1"},
		{"set", "2", "--cwd", "/srv/" + pastedKey},
		{"run", pastedKey},
		{"run", "1", pastedKey},
		{"send", pastedKey, "hello there"},
		{"serve", "--timeout", pastedKey},
	} {
		stdout, stderr, code := runRota(t, home, "", nil, argv...)
		if code == 0 || stillSecret(stdout+stderr) {
			t.Fatalf("rota %s: %d\n%s%s", strings.ReplaceAll(strings.Join(argv, " "), "SUITEONLY", "…"), code, stdout, stderr)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(home, "accounts.json")); string(after) != string(before) {
		t.Fatalf("nothing is stored:\n%s", after)
	}
}

// TestLoginKey_ReadsTheKeyFromStandardInput: `rota login --key` reads one
// line of stdin when it is not a terminal, and makes an account of its own
// out of the rotation, shown by its label or the key's last four. The API
// that cannot be asked is a warning. The key appears nowhere it prints.
func TestLoginKey_ReadsTheKeyFromStandardInput(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	proxy, connects := refusingProxy(t)
	home := storeDir(t, keyedStore(t))
	env := []string{"HTTPS_PROXY=" + proxy}

	stdout, stderr, code := runRota(t, home, pastedKey+"\n", env, "login", "--key")
	if code != 0 || !strings.Contains(stdout, "Added claude account 5 (api key ..."+pastedKey[len(pastedKey)-4:]+")") ||
		!strings.Contains(stdout, "out of the rotation") || !strings.Contains(stderr, "warning: ") {
		t.Fatalf("%d\n%s%s", code, stdout, stderr)
	}
	if stillSecret(stdout + stderr) {
		t.Fatalf("the key is never printed:\n%s%s", stdout, stderr)
	}
	if got := connects(); len(got) != 1 || !strings.HasPrefix(got[0], "CONNECT api.anthropic.com:443 ") {
		t.Fatalf("asked about once, of the API: %q", got)
	}
	stored := storedAccount(t, home, 5)
	if order := string(stored["order"]); string(stored["credential"]) != `"apikey"` || (order != "" && order != "0") {
		t.Fatalf("stored as a key account out of the rotation: %v", stored)
	}

	stdout, stderr, code = runRota(t, home, pastedKey+"\n", env, "login", "--key", "--label", "ci", "--json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s%s", code, err, stdout, stderr)
	}
	if doc["id"] != float64(5) || doc["status"] != "updated" || doc["label"] != "ci" || doc["credential"] != "apikey" {
		t.Fatalf("the same key is the same account, named now: %s", stdout)
	}

	// Something that is not a key is refused by its shape, unrepeated, and
	// nobody is asked about it.
	_, stderr, code = runRota(t, home, "SUITEONLY-not-a-key\n", env, "login", "--key")
	if code == 0 || stillSecret(stderr) || len(connects()) != 2 {
		t.Fatalf("%d %q\n%s", code, connects(), stderr)
	}
}

// TestLoginToken_ReadsASetupTokenFromStandardInput: `rota login <id>
// --token` puts a pasted setup token on that account as its long-lived
// token, says what rota cannot know about it, and takes it only for a
// claude.ai login.
func TestLoginToken_ReadsASetupTokenFromStandardInput(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	home := storeDir(t, keyedStore(t))
	stdout, stderr, code := runRota(t, home, pastedSetup+"\n", nil, "login", "2", "--token")
	if code != 0 || !strings.Contains(stdout, "long-lived token stored for #2") ||
		!strings.Contains(stdout, "cannot tell which claude.ai account") || stillSecret(stdout+stderr) {
		t.Fatalf("%d\n%s%s", code, stdout, stderr)
	}
	if stored := storedAccount(t, home, 2); !strings.Contains(string(stored["long"]), pastedSetup) {
		t.Fatalf("the account's long-lived token: %s", stored["long"])
	}
	for _, id := range []string{"1", "3", "9"} { // grok, a key account, nobody
		_, stderr, code := runRota(t, home, pastedSetup+"\n", nil, "login", id, "--token")
		if code == 0 || stillSecret(stderr) {
			t.Fatalf("account %s: %d\n%s", id, code, stderr)
		}
	}
}

// --- helpers ----------------------------------------------------------------

// The credentials pasted here. Each carries SUITEONLY in its middle, which
// nothing rota prints may ever contain: the last four characters of a key
// are all of it anything shows.
const (
	pastedKey   = "sk-ant-api03-SUITEONLY0123456789abcdefSUITEONLY0123456789-Ab12"
	pastedSetup = "sk-ant-oat01-SUITEONLY0123456789abcdefSUITEONLY0123456789-Cd34"
)

func stillSecret(s string) bool { return strings.Contains(s, "SUITEONLY") }

// dated is how the server's own plain lines begin in a log file.
var dated = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(Z|[+-]\d\d:\d\d) `)

// keyedStore is accounts.json with two accounts on Console API keys, one
// named, beside grok and an ordinary claude account. That claude account
// names its own configuration directory, so nothing ever looks for a login
// kept in a home of rota's — which on macOS would ask the keychain.
func keyedStore(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`{"ordered":true,"accounts":[
 {"id":1,"provider":"grok","email":"g@x","order":1,"token":{"accessToken":"xai-fake"}},
 {"id":2,"provider":"claude","email":"c@x","order":2,"config_dir":%s,"quotaAt":%d,
  "quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
  "token":{"accessToken":"tok-claude","expiresAt":0}},
 {"id":3,"provider":"claude","credential":"apikey","name":"ci key","uuid":"apikey-0000000000000003","order":0,
  "token":{"accessToken":"sk-ant-api03-SEEDEDONLY0123456789abcdefSEEDEDONLY-3333"}},
 {"id":4,"provider":"claude","credential":"apikey","uuid":"apikey-0000000000000004","order":0,
  "token":{"accessToken":"sk-ant-api03-SEEDEDONLY0123456789abcdefSEEDEDONLY-4444"}}]}`,
		strconv.Quote(t.TempDir()), time.Now().UnixMilli())
}

// storeDir is a ROTA_HOME holding accounts, for the commands that are not
// a server.
func storeDir(t *testing.T, accounts string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "accounts.json"), []byte(accounts), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// storedAccount is one account as accounts.json holds it, field by field.
func storedAccount(t *testing.T, home string, id int) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	for _, a := range doc.Accounts {
		if string(a["id"]) == fmt.Sprint(id) {
			return a
		}
	}
	t.Fatalf("no account %d in:\n%s", id, raw)
	return nil
}

// runRota runs the binary under test to completion with a private HOME and
// the given ROTA_HOME, stdin and extra environment, and fails the test if
// it has not finished within twenty seconds — a FIFO waited on, say.
func runRota(t *testing.T, home, stdin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if buildErr != nil {
		t.Skip(buildErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rotaBin, args...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "ROTA_HOME=" + home}, env...)
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
	}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("rota %s did not finish", strings.ReplaceAll(strings.Join(args, " "), "SUITEONLY", "…"))
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// refusingProxy is an HTTPS proxy on the loopback address that answers every
// CONNECT with 502, so a request through it fails at once without anything
// leaving this machine. connects is every request line it was sent.
func refusingProxy(t *testing.T) (url string, connects func() []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var mu sync.Mutex
	var seen []string
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				line, _ := bufio.NewReader(c).ReadString('\n')
				mu.Lock()
				seen = append(seen, strings.TrimSpace(line))
				mu.Unlock()
				io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			}()
		}
	}()
	return "http://" + l.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// fullLog makes path a log of exactly 20 MiB, its owner's alone, so the
// next line the server writes there moves it aside. It returns the size.
func fullLog(t *testing.T, path string) int64 {
	t.Helper()
	const full = 20 << 20
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(full); err != nil {
		t.Fatal(err)
	}
	return full
}

// waitForLog is the log file once it holds want, failing after five seconds.
func waitForLog(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), want) {
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never said %q:\n%s", path, want, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// gateRelease skips a test of something the binary under test predates,
// reading pre-releases as releases do: 1.4.0-beta.2 comes before
// 1.4.0-beta.3, which comes before 1.4.0. The package's own since reads
// "1.4.0-beta.3" as later than "1.4.0" and would skip these once 1.4.0 is
// out, so they gate here instead.
func gateRelease(t *testing.T, version string) {
	t.Helper()
	if buildErr != nil {
		t.Skip(buildErr)
	}
	if releaseBefore(binVersion, version) {
		t.Skipf("rota %s predates %s, which this pins", binVersion, version)
	}
}

// releaseBefore reports whether version a comes before b by semantic
// versioning: the numbers first, then a release after any pre-release of
// it, then the pre-release's dotted parts, numbers by value and below
// words.
func releaseBefore(a, b string) bool {
	an, ap := splitRelease(a)
	bn, bp := splitRelease(b)
	for i := range max(len(an), len(bn)) {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			return x < y
		}
	}
	switch {
	case ap == "" || ap == bp:
		return false
	case bp == "":
		return true
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := range min(len(as), len(bs)) {
		if as[i] == bs[i] {
			continue
		}
		x, xn := releaseNumber(as[i])
		y, yn := releaseNumber(bs[i])
		switch {
		case xn && yn:
			return x < y
		case xn != yn:
			return xn
		}
		return as[i] < bs[i]
	}
	return len(as) < len(bs)
}

// splitRelease is a version's numbers and its pre-release part, without a
// leading v or any build metadata.
func splitRelease(v string) ([]int, string) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, pre, _ := strings.Cut(v, "-")
	var nums []int
	for _, part := range strings.Split(core, ".") {
		n, _ := releaseNumber(part)
		nums = append(nums, n)
	}
	return nums, pre
}

// releaseNumber reads a part made of digits alone.
func releaseNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// TestReleaseBefore_ReadsPreReleasesAsReleasesDo pins the gate itself.
func TestReleaseBefore_ReadsPreReleasesAsReleasesDo(t *testing.T) {
	ordered := []string{"1.3.0", "1.3.2", "1.4.0-beta.1", "1.4.0-beta.2", "1.4.0-beta.10", "1.4.0-rc.1", "1.4.0", "v1.4.1", "1.10.0"}
	for i, a := range ordered {
		for j, b := range ordered {
			if got := releaseBefore(a, b); got != (i < j) {
				t.Fatalf("releaseBefore(%q, %q) = %v", a, b, got)
			}
		}
	}
}
