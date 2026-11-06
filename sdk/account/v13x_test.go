package account_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/wire"
	"rotatest/internal/release"
)

// What a listing says about an account since 1.3.0 and 1.3.1: when its
// login ends, whether it keeps its own .claude.json for Remote Control, and
// why a dead one is dead.

// endsAt is a claude account whose login, as Claude Code recorded it, ends
// at until.
func endsAt(until time.Time) *rota.Account {
	a := rota.NewAccount(1, "claude", &rota.Token{Access: "A", Refresh: "R", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	a.Email = "c@x"
	a.Extra = map[string]string{"refresh_token_expires_at": strconv.FormatInt(until.UnixMilli(), 10)}
	return a
}

// --json and GET /v1/accounts carry login_until when Claude Code has
// recorded when the login ends, and remote_control when it is on; neither is
// there otherwise.
func TestDescribe_LoginUntilAndRemoteControl(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	until := time.Now().Add(40 * 24 * time.Hour).Truncate(time.Second)
	a := endsAt(until)
	a.RemoteControl = true
	v := wire.Describe(a)
	if v.LoginUntil != until.UTC().Format(time.RFC3339) || !v.RemoteControl {
		t.Fatalf("login_until %q remote_control %v", v.LoginUntil, v.RemoteControl)
	}
	raw, _ := json.Marshal(v)
	if !strings.Contains(string(raw), `"login_until":"`+v.LoginUntil+`"`) || !strings.Contains(string(raw), `"remote_control":true`) {
		t.Fatalf("on the wire: %s", raw)
	}

	raw, _ = json.Marshal(wire.Describe(rota.NewAccount(2, "claude", &rota.Token{Access: "A"})))
	if strings.Contains(string(raw), "login_until") || strings.Contains(string(raw), "remote_control") {
		t.Fatalf("absent when unknown or off: %s", raw)
	}
}

// A listing says one line about a login in its last five days, and nothing
// about one further off or about a dead account, which has its own reason.
func TestLoginNote_SaysSoInTheLastFiveDays(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	soon := time.Now().Add(60 * time.Hour)
	if note := wire.LoginNote(endsAt(soon)); !strings.Contains(note, soon.Format(time.DateOnly)) || !strings.Contains(note, "rota login") {
		t.Fatalf("in its last days: %q", note)
	}
	if note := wire.LoginNote(endsAt(time.Now().Add(-3 * time.Hour))); !strings.Contains(note, "ended") {
		t.Fatalf("once it is over: %q", note)
	}
	if note := wire.LoginNote(endsAt(time.Now().Add(10 * 24 * time.Hour))); note != "" {
		t.Fatalf("far off: %q", note)
	}
	dead := endsAt(soon)
	dead.Dead = true
	if note := wire.LoginNote(dead); note != "" {
		t.Fatalf("a dead account: %q", note)
	}
}

// A dead account always says why, in a listing and on the wire, and one
// marked dead by a rota that kept no reason says that much instead of
// nothing.
func TestDescribe_ADeadAccountAlwaysSaysWhy(t *testing.T) {
	release.Since(t, rota.Version, "1.3.1")
	a := rota.NewAccount(1, "grok", &rota.Token{Access: "x"})
	a.Dead = true
	if v := wire.Describe(a); v.Status != rota.StatusReauth || !strings.Contains(v.DeadReason, "no reason") {
		t.Fatalf("status %q reason %q", v.Status, v.DeadReason)
	}
	a.DeadReason = "invalid_grant: refresh token revoked"
	if v := wire.Describe(a); v.DeadReason != a.DeadReason {
		t.Fatalf("the reason it kept: %q", v.DeadReason)
	}
}
