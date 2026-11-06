package serve_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// What the program under test says and refuses since 1.3.0 and 1.3.1: when
// a login ends, why a dead account is dead, and Remote Control, which needs
// the home rota keeps for an account.

// rotaIn runs the binary under test to completion with a private HOME and
// the store in home, and returns everything it wrote and its exit status.
func rotaIn(t *testing.T, home string, env []string, args ...string) (string, int) {
	t.Helper()
	if buildErr != nil {
		t.Skip(buildErr)
	}
	cmd := exec.Command(rotaBin, args...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "ROTA_HOME=" + home}, env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	}
	t.Fatalf("rota %s: %v", strings.Join(args, " "), err)
	return "", 0
}

// storeOf is a store directory holding accounts.json.
func storeOf(t *testing.T, accounts string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "accounts.json"), []byte(accounts), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// claudeIn is a claude account in a directory the person chose, so nothing
// about it reads a keychain: a token good for an hour, a reading too fresh
// to read again, and a login Claude Code recorded as ending at until.
func claudeIn(id int, dir string, until time.Time) string {
	now := time.Now()
	return fmt.Sprintf(`{"id":%d,"provider":"claude","email":"c%d@x","order":%d,"config_dir":%q,
  "quotaAt":%d,"quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
  "extra":{"refresh_token_expires_at":"%d"},
  "token":{"accessToken":"A%d","refreshToken":"R%d","expiresAt":%d}}`,
		id, id, id, dir, now.UnixMilli(), until.UnixMilli(), id, id, now.Add(time.Hour).UnixMilli())
}

// The STATUS column says when a login ends in its last three days, and that
// it ended once it is over, because sessions that outlive a login stop
// making progress; a dead account always says why, one an earlier rota
// marked dead without a reason included.
func TestRotaList_StatusSaysWhenALoginEndsAndWhyAnAccountIsDead(t *testing.T) {
	since(t, "1.3.1")
	home := storeOf(t, `{"ordered":true,"accounts":[`+
		claudeIn(1, t.TempDir(), time.Now().Add(60*time.Hour))+`,`+
		claudeIn(2, t.TempDir(), time.Now().Add(-3*time.Hour-time.Minute))+`,`+
		claudeIn(3, t.TempDir(), time.Now().Add(20*24*time.Hour))+`,
 {"id":4,"provider":"grok","email":"g@x","order":4,"dead":true,"token":{"accessToken":"xai-fake"}}]}`)
	out, code := rotaIn(t, home, nil, "list")
	if code != 0 {
		t.Fatalf("rota list: %d\n%s", code, out)
	}
	row := func(email string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, " "+email+" ") {
				return line
			}
		}
		t.Fatalf("no row for %s in\n%s", email, out)
		return ""
	}
	for email, want := range map[string]string{
		"c1@x": "ok (login ends in 2d)",
		"c2@x": "ok (login ended 3h ago)",
	} {
		if r := row(email); !strings.Contains(r, want) {
			t.Errorf("%s: want %q in\n%s", email, want, r)
		}
	}
	if r := row("c3@x"); strings.Contains(r, "login end") {
		t.Errorf("a login far from its end is not mentioned:\n%s", r)
	}
	if r := row("g@x"); !strings.Contains(r, "re-auth needed (") || !strings.Contains(r, "no reason") {
		t.Errorf("a dead account says why:\n%s", r)
	}

	out, code = rotaIn(t, home, nil, "list", "--json")
	var doc struct {
		Accounts []struct {
			ID         int    `json:"id"`
			DeadReason string `json:"deadReason"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("rota list --json: %d %v\n%s", code, err, out)
	}
	for _, a := range doc.Accounts {
		if a.ID == 4 && !strings.Contains(a.DeadReason, "no reason") {
			t.Errorf("and so does --json: %q", a.DeadReason)
		}
	}
}

// --json carries login_until when Claude Code has recorded when the login
// ends, and the line under the table says so in the last five days.
func TestRotaList_JSONCarriesLoginUntil(t *testing.T) {
	since(t, "1.3.0")
	until := time.Now().Add(60 * time.Hour).Truncate(time.Second)
	home := storeOf(t, `{"ordered":true,"accounts":[`+claudeIn(1, t.TempDir(), until)+`]}`)
	out, code := rotaIn(t, home, nil, "list", "--json")
	var doc struct {
		Accounts []struct {
			LoginUntil string `json:"login_until"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 || len(doc.Accounts) != 1 {
		t.Fatalf("rota list --json: %d %v\n%s", code, err, out)
	}
	if got := doc.Accounts[0].LoginUntil; got != until.UTC().Format(time.RFC3339) {
		t.Fatalf("login_until %q, want %q", got, until.UTC().Format(time.RFC3339))
	}
	out, _ = rotaIn(t, home, nil, "list")
	if !strings.Contains(out, "its login ends on "+until.Format(time.DateOnly)) {
		t.Fatalf("one line under the table:\n%s", out)
	}
}

// Remote Control needs the home rota keeps for an account: PATCH refuses it
// for an account in a directory the person chose and says why, and refuses
// to name for one account a directory that is already another's home.
func TestServePatch_RemoteControlAndOneHomeOneAccount(t *testing.T) {
	since(t, "1.3.0")
	theirs, mine := t.TempDir(), t.TempDir()
	s := start(t, opts{accounts: `{"ordered":true,"accounts":[` +
		claudeIn(1, theirs, time.Now().Add(30*24*time.Hour)) + `,` +
		claudeIn(2, mine, time.Now().Add(30*24*time.Hour)) + `]}`})

	resp, raw := s.do("PATCH", "/v1/accounts/1", `{"remote_control":true}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "configuration directory") {
		t.Fatalf("refused, saying why: %d %s", resp.StatusCode, raw)
	}
	for _, dir := range []string{theirs, theirs + "/", theirs + "/."} {
		resp, raw = s.do("PATCH", "/v1/accounts/2", `{"config_dir":`+quote(dir)+`}`)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "already") {
			t.Errorf("%q is account 1's home: %d %s", dir, resp.StatusCode, raw)
		}
	}
	resp, raw = s.do("GET", "/v1/accounts", "")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(raw), `"remote_control"`) {
		t.Fatalf("nothing changed: %d %s", resp.StatusCode, raw)
	}
	for _, a := range s.list().Accounts {
		if a.ID == 2 && a.ConfigDir != mine {
			t.Fatalf("account 2 keeps its own directory: %q", a.ConfigDir)
		}
	}
}

// For an account in the home rota keeps for it, PATCH takes remote_control,
// and the listing carries it while it is on.
func TestServePatch_RemoteControlOnAHomeRotaKeeps(t *testing.T) {
	since(t, "1.3.0")
	if runtime.GOOS != "linux" {
		t.Skip("elsewhere the home's login is read from a keychain, which no test may touch")
	}
	now := time.Now()
	s := start(t, opts{accounts: fmt.Sprintf(`{"ordered":true,"accounts":[
 {"id":1,"provider":"claude","email":"c@x","order":1,"quotaAt":%d,
  "quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
  "token":{"accessToken":"A","refreshToken":"R","expiresAt":%d}}]}`, now.UnixMilli(), now.Add(time.Hour).UnixMilli()),
		env: []string{"ROTA_CLAUDE_HOME=" + t.TempDir()}})
	resp, raw := s.do("PATCH", "/v1/accounts/1", `{"remote_control":true}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"remote_control":true`) {
		t.Fatalf("PATCH: %d %s", resp.StatusCode, raw)
	}
	_, raw = s.do("GET", "/v1/accounts", "")
	if !strings.Contains(string(raw), `"remote_control":true`) {
		t.Fatalf("the listing carries it: %s", raw)
	}
	resp, raw = s.do("PATCH", "/v1/accounts/1", `{"remote_control":false}`)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(raw), `"remote_control"`) {
		t.Fatalf("and off again: %d %s", resp.StatusCode, raw)
	}
}

// The command line refuses Remote Control for an account in a directory the
// person chose, saying why, before anything runs; and naming such a
// directory for an account that has it on is refused the same way.
func TestRotaSetAndRun_RemoteControlNeedsTheHomeRotaKeeps(t *testing.T) {
	since(t, "1.3.0")
	now := time.Now()
	store := storeOf(t, `{"ordered":true,"accounts":[`+claudeIn(1, t.TempDir(), now.Add(30*24*time.Hour))+`,
 {"id":2,"provider":"claude","email":"rc@x","order":2,"remoteControl":true,
  "token":{"accessToken":"B","refreshToken":"R","expiresAt":`+fmt.Sprint(now.Add(time.Hour).UnixMilli())+`}}]}`)
	before, _ := os.ReadFile(filepath.Join(store, "accounts.json"))
	bin := t.TempDir()
	ran := filepath.Join(bin, "ran")
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n: > "+ran+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin + ":/usr/bin:/bin", "ROTA_CLAUDE_HOME=" + t.TempDir()}
	for _, args := range [][]string{
		{"set", "1", "--remote-control", "on"},
		{"run", "1", "--remote-control"},
	} {
		out, code := rotaIn(t, store, env, args...)
		if code == 0 || !strings.Contains(out, "cannot have Remote Control") || !strings.Contains(out, "configuration directory") {
			t.Errorf("rota %s: refused, saying why: %d\n%s", strings.Join(args, " "), code, out)
		}
	}
	if _, err := os.Stat(ran); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing runs")
	}

	s := start(t, opts{accounts: string(before)})
	resp, raw := s.do("PATCH", "/v1/accounts/2", `{"config_dir":`+quote(t.TempDir())+`}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "Remote Control") {
		t.Fatalf("a chosen directory for an account with Remote Control on: %d %s", resp.StatusCode, raw)
	}
	if after, _ := os.ReadFile(filepath.Join(s.home, "accounts.json")); string(after) != string(before) {
		t.Fatalf("nothing changed:\n%s", after)
	}
	if after, _ := os.ReadFile(filepath.Join(store, "accounts.json")); string(after) != string(before) {
		t.Fatalf("nor by the command line:\n%s", after)
	}
}
