//go:build local

// These pin the long-lived token: the second credential an account may hold
// for a provider whose ordinary one dies inside a running process.
//
// They are behind the `local` tag because they name rota.Account.Long,
// rota.BeginLong and the store's long login, none of which exist in the
// published module the rest of this suite is pinned to. Run them against a
// worktree with a workspace file:
//
//	GOWORK=/path/go.work go test -tags local ./longtoken
package longtoken_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/store"
	"rotatest/internal/fake"
)

var ctx = context.Background()

// claudeLong wires the token endpoint to answer an authorization-code
// exchange as the provider does, recording each body, and returns the
// server so a test can read what was asked.
func claudeLong(t *testing.T, uuid, email string) *fake.Server {
	t.Helper()
	s := fake.NewServer(t)
	s.Claude(t)
	s.Handle("/token", func(_ *http.Request, body map[string]any) (int, any) {
		if body["grant_type"] == "refresh_token" {
			return 400, fake.OAuthReject("invalid_grant", "no refreshing here")
		}
		life := 0
		if n, ok := body["expires_in"].(float64); ok {
			life = int(n)
		}
		return 200, fake.ClaudeToken("LONG-SECRET", "", life, uuid, email, "org-1")
	})
	return s
}

func TestBeginLongAsksForInferenceOnly(t *testing.T) {
	fake.NewServer(t).Claude(t)
	l, err := rota.BeginLong(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Long {
		t.Fatal("a long login must remember that it is one")
	}
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("scope"); got != "user:inference" {
		t.Fatalf("scope = %q, want user:inference", got)
	}
	// The ordinary login still asks for everything else.
	ord, err := rota.Begin(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := url.Parse(ord.URL); !strings.Contains(o.Query().Get("scope"), "user:profile") {
		t.Fatalf("ordinary scope = %q", o.Query().Get("scope"))
	}
	// No other provider has one to give.
	for _, name := range []string{"codex", "grok", "kimi"} {
		if _, err := rota.BeginLong(ctx, name); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: err = %v, want a refusal naming it", name, err)
		}
	}
}

func TestCompleteLongAsksForAYearAsANumber(t *testing.T) {
	s := claudeLong(t, "u1", "one@x")
	l, err := rota.BeginLong(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := l.Complete(ctx, "CODE")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "LONG-SECRET" || tok.Identity == nil || tok.Identity.UUID != "u1" {
		t.Fatalf("token = %+v", tok)
	}
	reqs := s.Requests()
	life, ok := reqs[len(reqs)-1].Body["expires_in"].(float64)
	if !ok || int64(life) != 31536000 {
		t.Fatalf("expires_in = %#v, want the number 31536000", reqs[len(reqs)-1].Body["expires_in"])
	}
	if until := time.UnixMilli(tok.ExpiresAt); until.Before(time.Now().Add(300 * 24 * time.Hour)) {
		t.Fatalf("expiry = %v, want about a year out", until)
	}

	// The ordinary exchange asks for no particular life at all.
	ord, err := rota.Begin(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ord.Complete(ctx, "CODE"); err != nil {
		t.Fatal(err)
	}
	reqs = s.Requests()
	if got := reqs[len(reqs)-1].Body["expires_in"]; got != nil {
		t.Fatalf("the ordinary exchange sent expires_in = %#v", got)
	}
}

// Launch is where the whole thing shows: the CLI is handed the long token
// whenever there is one worth using, and the short one otherwise.
func TestLaunchPrefersTheLongToken(t *testing.T) {
	ms := func(d time.Duration) int64 { return time.Now().Add(d).UnixMilli() }
	for _, c := range []struct {
		what string
		long *rota.LongToken
		want string
	}{
		{"none at all", nil, "SHORT"},
		{"most of a year left", &rota.LongToken{Access: "LONG", ExpiresAt: ms(300 * 24 * time.Hour)}, "LONG"},
		{"no expiry", &rota.LongToken{Access: "LONG"}, "LONG"},
		{"an hour left", &rota.LongToken{Access: "LONG", ExpiresAt: ms(time.Hour)}, "SHORT"},
		{"expired", &rota.LongToken{Access: "LONG", ExpiresAt: ms(-time.Hour)}, "SHORT"},
	} {
		a := &rota.Account{ID: 1, Provider: "claude", Long: c.long,
			Token: rota.Token{Access: "SHORT", ExpiresAt: ms(time.Hour)}}
		cmd, err := rota.Stage(a, "")
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if got := env(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN"); got != c.want {
			t.Fatalf("%s: launched with %q, want %q", c.what, got, c.want)
		}
	}
}

func env(list []string, name string) string {
	for _, kv := range list {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// A store is where the two halves meet: the login attaches the token to the
// account that approved it, and nothing else.
func TestStoreLongLoginAttachesToTheApprovingAccount(t *testing.T) {
	claudeLong(t, "u1", "one@x")
	s, _ := seed(t, `{"nextId":3,"accounts":[
	 {"id":1,"provider":"claude","uuid":"u0","email":"zero@x","token":{"accessToken":"a0","refreshToken":"r0"}},
	 {"id":2,"provider":"claude","uuid":"u1","email":"one@x","token":{"accessToken":"a1","refreshToken":"r1"}}]}`)

	l, err := s.BeginLongLogin(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.PendingLogin(l.ID); err != nil || got == nil || !got.Long {
		t.Fatalf("parked login = %+v, %v", got, err)
	}
	a, added, err := s.FinishLogin(ctx, l.ID, "CODE")
	if err != nil || added || a.ID != 2 {
		t.Fatalf("a = %+v added = %v err = %v", a, added, err)
	}
	if len(s.Accounts) != 2 {
		t.Fatalf("a long login must create nothing; accounts = %d", len(s.Accounts))
	}
	if a.Long == nil || a.Long.Access != "LONG-SECRET" || !a.LongValid() {
		t.Fatalf("long = %+v", a.Long)
	}
	if a.Token.Access != "a1" || a.Token.Refresh != "r1" {
		t.Fatal("the ordinary credential must be untouched")
	}
	if s.Accounts[0].Long != nil {
		t.Fatal("only the account that approved gets it")
	}
}

func TestStoreLongLoginRefusesAnUnknownIdentity(t *testing.T) {
	claudeLong(t, "u9", "nine@x")
	s, _ := seed(t, `{"nextId":2,"accounts":[
	 {"id":1,"provider":"claude","uuid":"u1","email":"one@x","token":{"accessToken":"a1"}}]}`)
	l, err := s.BeginLongLogin(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.FinishLogin(ctx, l.ID, "CODE")
	if err == nil || !strings.Contains(err.Error(), "nine@x") {
		t.Fatalf("err = %v, want a refusal naming who approved", err)
	}
	if len(s.Accounts) != 1 || s.Accounts[0].Long != nil {
		t.Fatal("nothing may be created or stored")
	}
}

// seed opens a file-backed store over a raw blob: the account state this
// suite builds without running a login.
func seed(t *testing.T, blob string) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
