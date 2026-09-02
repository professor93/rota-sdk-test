package claude_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// at is the instant every test runs at. The refresh accounts expire at 1 ms,
// so at this instant they are long expired and Refresh must use the network.
var (
	at   = time.Unix(1_700_000_000, 0)
	atMS = at.UnixMilli()
)

// usageFixture is the reply the published tests use: a garbage 7d date, one
// scoped limit, one unscoped limit to skip, and extra usage in credits.
const usageFixture = `{"five_hour":{"utilization":12.5,"resets_at":"2099-01-01T00:00:00Z"},
 "seven_day":{"utilization":40,"resets_at":"not a date"},
 "limits":[{"kind":"weekly","percent":91,"resets_at":"2099-01-02T00:00:00+00:00",
            "scope":{"model":{"display_name":"Fable"},"surface":{"weird":[1]}}},
           {"kind":"weekly","percent":1,"scope":null}],
 "extra_usage":{"is_enabled":true,"used_credits":1250,"monthly_limit":10000,"currency":"USD"}}`

// setup pins the clock and points every endpoint at a fresh server, so no
// test can reach the real service, even one that makes no request.
func setup(t *testing.T) *fake.Server {
	t.Helper()
	s := fake.NewServer(t)
	s.Claude(t)
	fake.Clock(t, at)
	return s
}

// account is the one every test starts from: expired, with a refresh token.
func account() *rota.Account {
	return rota.NewAccount(1, "claude", &rota.Token{Access: "A0", Refresh: "R0", ExpiresAt: 1})
}

// begin starts a login so Complete has a verifier and a state to send.
func begin(t *testing.T) *rota.Login {
	t.Helper()
	l, err := rota.Begin(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// reject answers every grant with one RFC 6749 error code.
func reject(code string) fake.Handler {
	return func(*http.Request, map[string]any) (int, any) { return 400, fake.OAuthReject(code, "") }
}

// reply answers every request with status and body, whatever was asked.
func reply(status int, body any) fake.Handler {
	return func(*http.Request, map[string]any) (int, any) { return status, body }
}

func TestClaudeBegin_URLIsAuthorizeWithPKCE(t *testing.T) {
	s := setup(t)
	l := begin(t)
	if !strings.HasPrefix(l.URL, s.URL+"/authorize") {
		t.Fatalf("url %q", l.URL)
	}
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"code_challenge", "state", "redirect_uri", "client_id"} {
		if u.Query().Get(k) == "" {
			t.Errorf("query lacks %s: %q", k, l.URL)
		}
	}
	if len(l.State) == 0 {
		t.Fatal("state is empty")
	}
}

func TestClaudeComplete_ExchangesCodeWithVerifier(t *testing.T) {
	s := setup(t)
	s.Handle("/token", func(_ *http.Request, body map[string]any) (int, any) {
		if body["grant_type"] != "authorization_code" || body["code"] != "C1" {
			t.Errorf("body %v", body)
		}
		if v, _ := body["code_verifier"].(string); v == "" {
			t.Errorf("no code_verifier: %v", body)
		}
		return 200, fake.ClaudeToken("A1", "R1", 3600, "u1", "e@x", "o1")
	})
	tok, err := begin(t).Complete(context.Background(), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Hits("/token") != 1 {
		t.Fatalf("token hits %d", s.Hits("/token"))
	}
	if tok.Access != "A1" || tok.Refresh != "R1" || tok.ExpiresAt != atMS+3600*1000 {
		t.Fatalf("token %+v", tok)
	}
	if !slices.Equal(tok.Scopes, []string{"user:inference", "user:profile"}) {
		t.Fatalf("scopes %v", tok.Scopes)
	}
	if tok.Identity == nil || *tok.Identity != (rota.Identity{UUID: "u1", Email: "e@x", Org: "o1"}) {
		t.Fatalf("identity %+v", tok.Identity)
	}
}

func TestClaudeComplete_CodeWithStateSuffixIsSplit(t *testing.T) {
	s := setup(t)
	s.Handle("/token", func(_ *http.Request, body map[string]any) (int, any) {
		if body["code"] != "C1" || body["state"] != "ST" {
			t.Errorf("body %v", body)
		}
		return 200, fake.ClaudeToken("A1", "R1", 3600, "u1", "e@x", "o1")
	})
	if _, err := begin(t).Complete(context.Background(), "C1#ST"); err != nil {
		t.Fatal(err)
	}
	if s.Hits("/token") != 1 {
		t.Fatalf("token hits %d", s.Hits("/token"))
	}
}

func TestClaudeComplete_FallsBackToProfileForIdentity(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(200, fake.ClaudeToken("A1", "R1", 3600, "", "", "")))
	s.Handle("/profile", func(r *http.Request, _ map[string]any) (int, any) {
		if got := r.Header.Get("Authorization"); got != "Bearer A1" {
			t.Errorf("authorization %q", got)
		}
		return 200, fake.ClaudeProfile("u1", "e@x", "o1")
	})
	tok, err := begin(t).Complete(context.Background(), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Hits("/profile") != 1 {
		t.Fatalf("profile hits %d", s.Hits("/profile"))
	}
	if tok.Identity == nil || *tok.Identity != (rota.Identity{UUID: "u1", Email: "e@x", Org: "o1"}) {
		t.Fatalf("identity %+v", tok.Identity)
	}
}

func TestClaudeComplete_ProfileWithoutUUIDLeavesIdentityEmpty(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(200, fake.ClaudeToken("A1", "R1", 3600, "", "", "")))
	s.Handle("/profile", reply(200, fake.Reply{}))
	tok, err := begin(t).Complete(context.Background(), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Identity != nil && *tok.Identity != (rota.Identity{}) {
		t.Fatalf("identity %+v", tok.Identity)
	}
}

func TestClaudeComplete_InvalidGrantOnCodeIsOAuthError(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(400, fake.OAuthReject("invalid_grant", "gone")))
	_, err := begin(t).Complete(context.Background(), "C1")
	var oe *rota.OAuthError
	if !errors.As(err, &oe) || oe.Code != "invalid_grant" {
		t.Fatalf("err %v", err)
	}
	if errors.Is(err, rota.ErrDeadToken) {
		t.Fatalf("a rejected code is not a dead lineage: %v", err)
	}
}

func TestClaudeComplete_ExpiredAndDeniedAreOAuthErrors(t *testing.T) {
	s := setup(t)
	for _, tc := range []struct{ code, want string }{
		{"expired_token", "expired"},
		{"access_denied", "denied"},
	} {
		s.Handle("/token", reject(tc.code))
		_, err := begin(t).Complete(context.Background(), "C1")
		var oe *rota.OAuthError
		if !errors.As(err, &oe) || !strings.Contains(oe.Error(), tc.want) {
			t.Errorf("%s: %v", tc.code, err)
		}
	}
}

func TestClaudeComplete_AuthorizationPendingIsSentinel(t *testing.T) {
	s := setup(t)
	for _, code := range []string{"authorization_pending", "slow_down"} {
		s.Handle("/token", reject(code))
		_, err := begin(t).Complete(context.Background(), "C1")
		var oe *rota.OAuthError
		if !errors.Is(err, rota.ErrAuthPending) || errors.As(err, &oe) {
			t.Errorf("%s: %v", code, err)
		}
	}
}

func TestClaudeComplete_ServerErrorIsHTTPError(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(500, "  boom  "))
	_, err := begin(t).Complete(context.Background(), "C1")
	var he *rota.HTTPError
	if !errors.As(err, &he) || he.Status != 500 || he.Body != "  boom  " {
		t.Fatalf("err %v", err)
	}
	if he.Error() != "http 500: boom" {
		t.Fatalf("message %q", he.Error())
	}

	long := strings.Repeat("b", 400)
	s.Handle("/token", reply(500, long))
	_, err = begin(t).Complete(context.Background(), "C1")
	if !errors.As(err, &he) {
		t.Fatalf("err %v", err)
	}
	msg := he.Error()
	if !strings.HasPrefix(msg, "http 500: "+long[:300]) || strings.Contains(msg, long[:301]) {
		t.Fatalf("message not cut to 300: %d chars", len(msg))
	}
}

func TestClaudeComplete_MalformedJSONFails(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(200, "not json"))
	tok, err := begin(t).Complete(context.Background(), "C1")
	if err == nil {
		t.Fatalf("token %+v", tok)
	}
}

func TestClaudeComplete_ReplyWithoutAccessTokenFails(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(200, fake.Reply{}))
	tok, err := begin(t).Complete(context.Background(), "C1")
	if err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("err=%v token=%+v", err, tok)
	}
}

func TestClaudeRefresh_RotatesAccessKeepsRefreshWhenAbsent(t *testing.T) {
	s := setup(t)
	s.Handle("/token", func(_ *http.Request, body map[string]any) (int, any) {
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "R0" {
			t.Errorf("body %v", body)
		}
		return 200, fake.Reply{"access_token": "A2", "expires_in": 60}
	})
	a := account()
	changed, err := rota.Refresh(context.Background(), a)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if s.Hits("/token") != 1 {
		t.Fatalf("token hits %d", s.Hits("/token"))
	}
	if a.Token.Access != "A2" || a.Token.Refresh != "R0" || a.Token.ExpiresAt != atMS+60*1000 || a.Dead {
		t.Fatalf("account %+v", a)
	}
}

func TestClaudeRefresh_NewRefreshTokenReplacesOld(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(200, fake.Reply{"access_token": "A2", "refresh_token": "R2", "expires_in": 60}))
	a := account()
	changed, err := rota.Refresh(context.Background(), a)
	if err != nil || !changed || a.Token.Refresh != "R2" {
		t.Fatalf("changed=%v err=%v account=%+v", changed, err, a)
	}
}

func TestClaudeRefresh_InvalidGrantKillsLineage(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reject("invalid_grant"))
	a := account()
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestClaudeRefresh_ScavengesCodeFromObjectShapedError(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(400, `{"error":{"type":"invalid_grant","message":"gone"}}`))
	a := account()
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestClaudeRefresh_TransientFailureLeavesAccountAlone(t *testing.T) {
	s := setup(t)
	s.Handle("/token", reply(500, "boom"))
	a := account()
	changed, err := rota.Refresh(context.Background(), a)
	var he *rota.HTTPError
	if changed || errors.Is(err, rota.ErrReauth) || !errors.As(err, &he) {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if a.Token.Access != "A0" || a.Dead {
		t.Fatalf("account %+v", a)
	}
}

func TestClaudeRefresh_HonoursContextDeadline(t *testing.T) {
	s := setup(t)
	stall := make(chan struct{})
	// Registered after the server, so it runs first and Close never waits
	// on a handler that is still blocked.
	t.Cleanup(func() { close(stall) })
	s.Handle("/token", func(*http.Request, map[string]any) (int, any) {
		<-stall
		return 500, "late"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	a := account()
	_, err := rota.Refresh(ctx, a)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if a.Dead {
		t.Fatal("a timed-out refresh is transient, not a dead lineage")
	}
}

func TestClaudeUsage_ParsesWindowsNoteAndExtra(t *testing.T) {
	s := setup(t)
	s.Handle("/usage", reply(200, usageFixture))
	q, err := rota.Usage(context.Background(), account())
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Windows) != 3 {
		t.Fatalf("windows %+v", q.Windows)
	}
	w := q.Windows
	if w[0].Name != "5h" || w[0].Percent != 12.5 || !w[0].Primary || w[0].ResetsAt.IsZero() {
		t.Errorf("5h %+v", w[0])
	}
	if w[1].Name != "7d" || w[1].Percent != 40 || !w[1].ResetsAt.IsZero() {
		t.Errorf("7d %+v", w[1])
	}
	if w[2].Name != "Fable" || w[2].Percent != 91 || !w[2].Scoped {
		t.Errorf("scoped %+v", w[2])
	}
	if q.Note != "extra usage 12.50 / 100.00 USD" {
		t.Errorf("note %q", q.Note)
	}
	if q.Extra == nil || *q.Extra != (rota.ExtraUsage{Used: 12.5, Limit: 100, Currency: "USD"}) {
		t.Errorf("extra %+v", q.Extra)
	}
}

func TestClaudeUsage_SendsBetaHeaderAndBearer(t *testing.T) {
	s := setup(t)
	s.Handle("/usage", reply(200, fake.ClaudeUsage(1, 2, "")))
	if _, err := rota.Usage(context.Background(), account()); err != nil {
		t.Fatal(err)
	}
	reqs := s.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/usage" {
		t.Fatalf("requests %+v", reqs)
	}
	h := reqs[0].Header
	if h.Get("anthropic-beta") != "oauth-2025-04-20" || h.Get("Authorization") != "Bearer A0" {
		t.Fatalf("headers %v", h)
	}
}

func TestClaudeUsage_ErrorStatusIsHTTPError(t *testing.T) {
	s := setup(t)
	s.Handle("/usage", reply(429, fake.Reply{"error": "slow"}))
	_, err := rota.Usage(context.Background(), account())
	var he *rota.HTTPError
	if !errors.As(err, &he) || he.Status != 429 {
		t.Fatalf("err %v", err)
	}
}

func TestClaudeUsage_OversizeReplyIsRefused(t *testing.T) {
	s := setup(t)
	// The document only closes past the 4 MiB cap, so whatever is kept
	// cannot parse; a client that buffered it all would hang or bloat.
	huge := `{"five_hour":{"utilization":1},"pad":"` + strings.Repeat("x", 5<<20) + `"}`
	s.Handle("/usage", reply(200, huge))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	q, err := rota.Usage(ctx, account())
	if err == nil {
		t.Fatalf("quota %+v", q)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung on the reply: %v", err)
	}
}

func TestClaudeLaunch_EnvAndDrops(t *testing.T) {
	setup(t)
	cmd, err := rota.Stage(account(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN=A0") {
		t.Errorf("env %v", cmd.Env)
	}
	for _, k := range append([]string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"}, rota.NetworkRedirecting()...) {
		if !slices.Contains(cmd.Drop, k) {
			t.Errorf("drop lacks %s: %v", k, cmd.Drop)
		}
	}
	if slices.ContainsFunc(cmd.Env, func(e string) bool { return strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") }) {
		t.Errorf("env names a config dir: %v", cmd.Env)
	}
}

func TestClaudeLaunch_ConfigDirComesFromAccountNotHome(t *testing.T) {
	setup(t)
	a := account()
	a.ConfigDir = "/tmp/x"
	home := t.TempDir()
	cmd, err := rota.Stage(a, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR=/tmp/x") || slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR="+home) {
		t.Fatalf("env %v", cmd.Env)
	}
}

func TestClaudeStagePlan_NoFiles(t *testing.T) {
	setup(t)
	cmd, files, err := rota.StagePlan(context.Background(), account(), "")
	if err != nil || cmd == nil || len(files) != 0 {
		t.Fatalf("cmd=%+v files=%+v err=%v", cmd, files, err)
	}
}

func TestClaudeStage_DeadIsReauth(t *testing.T) {
	setup(t)
	a := account()
	a.Dead = true
	_, err := rota.Stage(a, "")
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("err %v", err)
	}
}
