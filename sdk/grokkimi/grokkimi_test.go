package grokkimi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

var ctx = context.Background()

// delegated is an account whose credential the CLI keeps in its own home.
func delegated(provider string) *rota.Account {
	return rota.NewAccount(1, provider, &rota.Token{Delegated: true, Identity: &rota.Identity{UUID: provider + "-x"}})
}

// keyed is a grok account holding a pasted key, or whatever access was given.
func keyed(access string) *rota.Account {
	return rota.NewAccount(2, "grok", &rota.Token{Access: access})
}

// complete runs a whole login so Complete is reached the way a caller reaches it.
func complete(t *testing.T, provider, code string) (*rota.Token, error) {
	t.Helper()
	l, err := rota.Begin(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	return l.Complete(ctx, code)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// signInChecker is the grok or kimi provider's own signed-in check.
func signInChecker(t *testing.T, provider string) rota.SignInChecker {
	t.Helper()
	p, err := rota.Lookup(provider)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := p.(rota.SignInChecker)
	if !ok {
		t.Fatalf("%s is not a SignInChecker", provider)
	}
	return c
}

func TestGrokBegin_IsAPIKeyAndDelegated(t *testing.T) {
	l, err := rota.Begin(ctx, "grok")
	if err != nil {
		t.Fatal(err)
	}
	if l.Kind != "apikey" || !l.Delegated {
		t.Fatalf("kind=%q delegated=%v", l.Kind, l.Delegated)
	}
}

func TestGrokComplete_EmptyKeyIsDelegatedToken(t *testing.T) {
	tok, err := complete(t, "grok", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.Delegated || tok.Access != "" {
		t.Fatalf("delegated=%v access=%q", tok.Delegated, tok.Access)
	}
}

func TestGrokComplete_KeyMustStartWithXai(t *testing.T) {
	_, err := complete(t, "grok", "sk-1")
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "console.x.ai") {
		t.Fatalf("err=%v", err)
	}
}

func TestGrokComplete_KeyIdentityIsStable(t *testing.T) {
	first, err := complete(t, "grok", "xai-abc")
	if err != nil {
		t.Fatal(err)
	}
	again, err := complete(t, "grok", "xai-abc")
	if err != nil {
		t.Fatal(err)
	}
	other, err := complete(t, "grok", "xai-def")
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity == nil || again.Identity == nil || other.Identity == nil {
		t.Fatalf("identity missing: %+v %+v %+v", first, again, other)
	}
	if !strings.HasPrefix(first.Identity.UUID, "key-") {
		t.Fatalf("uuid=%q", first.Identity.UUID)
	}
	if first.Identity.UUID != again.Identity.UUID {
		t.Fatalf("same key, different identity: %q %q", first.Identity.UUID, again.Identity.UUID)
	}
	if first.Identity.UUID == other.Identity.UUID {
		t.Fatalf("different keys collided on %q", first.Identity.UUID)
	}
	if first.ExpiresAt != 0 || first.Access != "xai-abc" {
		t.Fatalf("expiresAt=%d access=%q", first.ExpiresAt, first.Access)
	}
}

func TestGrokLaunch_RefusesEmptyHome(t *testing.T) {
	if _, err := rota.Stage(keyed("xai-abc"), ""); err == nil {
		t.Fatal("empty home accepted")
	}
}

func TestGrokLaunch_EnvHasKeyAndHome(t *testing.T) {
	home := t.TempDir()
	cmd, err := rota.Stage(keyed("xai-abc"), home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "XAI_API_KEY=xai-abc") || !slices.Contains(cmd.Env, "GROK_HOME="+home) {
		t.Fatalf("env=%v", cmd.Env)
	}
	for _, name := range rota.NetworkRedirecting() {
		if !slices.Contains(cmd.Drop, name) {
			t.Fatalf("drop lacks %s: %v", name, cmd.Drop)
		}
	}
}

func TestGrokLaunch_NonKeyAccessIsReauth(t *testing.T) {
	_, err := rota.Stage(keyed("nope"), t.TempDir())
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("err=%v", err)
	}
}

func TestGrokLaunch_DelegatedUnsignedStillLaunches(t *testing.T) {
	// Handing over the terminal is how the account gets signed in, so Stage
	// must not refuse an account with no credential file yet.
	if _, err := rota.Stage(delegated("grok"), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestGrokSignedIn_NeedsAuthJSON(t *testing.T) {
	c := signInChecker(t, "grok")
	home := t.TempDir()
	a := delegated("grok")
	if err := c.SignedIn(a, home); err == nil {
		t.Fatal("unsigned delegated account passed")
	}
	writeFile(t, filepath.Join(home, "auth.json"), "{}")
	if err := c.SignedIn(a, home); err != nil {
		t.Fatal(err)
	}
	if err := c.SignedIn(keyed("xai-abc"), t.TempDir()); err != nil {
		t.Fatalf("keyed account needs no file: %v", err)
	}
}

func TestGrokLoginPlan_DeviceCode(t *testing.T) {
	home := t.TempDir()
	plan, ok := rota.LoginPlanFor(delegated("grok"), home)
	if !ok {
		t.Fatal("no login plan")
	}
	if plan.Bin != "grok" || !slices.Contains(plan.Args, "login") || !slices.Contains(plan.Args, "--device-code") {
		t.Fatalf("bin=%q args=%v", plan.Bin, plan.Args)
	}
	if !slices.Contains(plan.Env, "GROK_HOME="+home) || !slices.Contains(plan.Drop, "XAI_API_KEY") {
		t.Fatalf("env=%v drop=%v", plan.Env, plan.Drop)
	}
}

func TestLoginPlanFor_FalseForNonDelegatedAndForClaude(t *testing.T) {
	if _, ok := rota.LoginPlanFor(keyed("xai-abc"), "h"); ok {
		t.Fatal("non-delegated account got a plan")
	}
	if _, ok := rota.LoginPlanFor(delegated("claude"), "h"); ok {
		t.Fatal("a provider without a delegated login got a plan")
	}
}

// grokAuthJSON is the file the CLI leaves in its home: a map keyed by issuer
// and client id, with the identity beside tokens rota never takes.
const grokAuthJSON = `{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828": {
   "auth_mode":"oauth",
   "user_id":"00000000-0000-4000-8000-000000000001",
   "email":"someone@example.com",
   "refresh_token":"not-rota's-to-hold"}}`

func TestGrokAdopt_ReadsDelegatedAuthJSON(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "auth.json"), grokAuthJSON)
	a := delegated("grok")
	if err := rota.Adopt(a, home); err != nil {
		t.Fatal(err)
	}
	if a.UUID != "00000000-0000-4000-8000-000000000001" || a.Email != "someone@example.com" {
		t.Fatalf("uuid=%q email=%q", a.UUID, a.Email)
	}
	if a.Token.Refresh != "" {
		t.Fatalf("refresh token taken: %q", a.Token.Refresh)
	}
}

func TestGrokResolveModel_FloorPassesUnknownRefusesOtherProviders(t *testing.T) {
	if got, err := rota.ResolveModel("grok", "grok-9"); err != nil || got != "grok-9" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	_, err := rota.ResolveModel("grok", "claude-sonnet-5")
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("err=%v", err)
	}
}

func TestKimiBegin_IsDelegated(t *testing.T) {
	l, err := rota.Begin(ctx, "kimi")
	if err != nil {
		t.Fatal(err)
	}
	if l.Kind != "delegated" || !l.Delegated {
		t.Fatalf("kind=%q delegated=%v", l.Kind, l.Delegated)
	}
}

func TestKimiComplete_RefusesAnythingPasted(t *testing.T) {
	_, err := complete(t, "kimi", "x")
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "nothing pasted") {
		t.Fatalf("err=%v", err)
	}
	tok, err := complete(t, "kimi", "")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.Delegated {
		t.Fatalf("token=%+v", tok)
	}
}

func TestKimiLaunch_RefusesEmptyHomeAndNonDelegated(t *testing.T) {
	if _, err := rota.Stage(delegated("kimi"), ""); err == nil {
		t.Fatal("empty home accepted")
	}
	// A real home, so the account check is what refuses.
	_, err := rota.Stage(rota.NewAccount(3, "kimi", &rota.Token{Access: "old"}), t.TempDir())
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("err=%v", err)
	}
}

func TestKimiSignedIn_ThreeStates(t *testing.T) {
	c := signInChecker(t, "kimi")
	home := t.TempDir()
	a := delegated("kimi")
	err := c.SignedIn(a, home)
	if !errors.Is(err, rota.ErrReauth) || !strings.Contains(err.Error(), "signed in") {
		t.Fatalf("nothing stored: err=%v", err)
	}
	writeFile(t, filepath.Join(home, "credentials", "kimi-code.json"), "{}")
	err = c.SignedIn(a, home)
	if !errors.Is(err, rota.ErrReauth) || !strings.Contains(err.Error(), "finish") {
		t.Fatalf("token only: err=%v", err)
	}
	writeFile(t, filepath.Join(home, "config.toml"), "")
	if err := c.SignedIn(a, home); err != nil {
		t.Fatalf("both files: err=%v", err)
	}
}

func TestKimi_HasNoCatalog(t *testing.T) {
	if m := rota.Models("kimi"); m != nil {
		t.Fatalf("models=%v", m)
	}
	if e := rota.Efforts("kimi"); e != nil {
		t.Fatalf("efforts=%v", e)
	}
	if m, e := rota.Defaults("kimi"); m != "" || e != "" {
		t.Fatalf("defaults=%q/%q", m, e)
	}
	_, err := rota.ResolveEffort("kimi", "high")
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "effort") {
		t.Fatalf("err=%v", err)
	}
}

func TestKimiLaunch_EnvHasHome(t *testing.T) {
	home := t.TempDir()
	cmd, err := rota.Stage(delegated("kimi"), home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "KIMI_CODE_HOME="+home) {
		t.Fatalf("env=%v", cmd.Env)
	}
}

func TestDelegatedAccount_NeverExpiresRefreshesOrLimits(t *testing.T) {
	// The clock sits far past ExpiresAt, so only the delegated flag keeps
	// this account alive.
	fake.Clock(t, time.Unix(1_700_000_000, 0))
	a := rota.NewAccount(4, "grok", &rota.Token{Delegated: true, ExpiresAt: 1})
	if a.Expired() {
		t.Fatal("delegated account expired")
	}
	changed, err := rota.Refresh(ctx, a)
	if changed || err != nil {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if s := a.Status(); s != rota.StatusOK {
		t.Fatalf("status=%q", s)
	}
	raw, err := rota.Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"delegated":true`) {
		t.Fatalf("json=%s", raw)
	}
}
