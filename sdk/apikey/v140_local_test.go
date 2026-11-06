//go:build local

// These pin the calls rota 1.4.0-beta.1 added for a pasted credential:
// rota.Paste, Account.APIKey, the store's LoginKey and PasteLong, and
// wire's HoldsSecret and Unsecret. They are behind the `local` tag because
// the published module this suite is pinned to has none of them; run them
// against a checkout with a workspace file:
//
//	GOWORK=/path/go.work go test -tags local ./sdk/apikey

package apikey_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/store"
	"github.com/professor93/rota/wire"
	"rotatest/internal/fake"
)

// setup is a token as `claude setup-token` prints one.
const setup = "sk-ant-oat01-SUITEONLY0123456789abcdefSUITEONLY0123456789-Cd34"

// models is the API's model list, answering every key check with status.
func models(t *testing.T, status int) *fake.Server {
	t.Helper()
	s := fake.NewServer(t)
	s.Claude(t)
	fake.Restore(t, &rota.ClaudeModelsEndpoint, s.URL+"/models")
	s.Handle("/models", func(*http.Request, map[string]any) (int, any) {
		return status, fake.Reply{"data": []any{}}
	})
	return s
}

// TestPaste_AKeyIsCheckedOnceWithTheModelList: one request, the key in
// x-api-key with an API version, and an account's worth of token back:
// the key itself, said to be one, named by a fingerprint of it.
func TestPaste_AKeyIsCheckedOnceWithTheModelList(t *testing.T) {
	fake.Registry(t)
	s := models(t, 200)
	tok, unverified, err := rota.Paste(ctx, "claude", "  "+key+"\n")
	if err != nil || unverified != nil {
		t.Fatalf("%v %v", unverified, err)
	}
	if tok.Access != key || tok.Credential != "apikey" || tok.Identity == nil || !strings.HasPrefix(tok.Identity.UUID, "apikey-") {
		t.Fatalf("token %+v", tok)
	}
	reqs := s.Requests()
	if len(reqs) != 1 || reqs[0].Method != "GET" || reqs[0].Header.Get("X-Api-Key") != key || reqs[0].Header.Get("Anthropic-Version") == "" {
		t.Fatalf("one GET carrying the key: %+v", reqs)
	}
	again, _, _ := rota.Paste(ctx, "claude", key)
	other, _, _ := rota.Paste(ctx, "claude", strings.Replace(key, "Ab12", "Zz99", 1))
	if again.Identity.UUID != tok.Identity.UUID || other.Identity.UUID == tok.Identity.UUID {
		t.Fatalf("the same key is the same account, another another: %s %s %s", tok.Identity.UUID, again.Identity.UUID, other.Identity.UUID)
	}
}

// TestPaste_AKeyTheAPIRefusesIsRefused: a 401 or a 403 is the API saying
// no, and nothing is taken; the refusal never repeats the key.
func TestPaste_AKeyTheAPIRefusesIsRefused(t *testing.T) {
	for _, status := range []int{401, 403} {
		fake.Registry(t)
		models(t, status)
		tok, _, err := rota.Paste(ctx, "claude", key)
		if tok != nil || !errors.Is(err, rota.ErrInvalidRequest) || strings.Contains(err.Error(), "SUITEONLY") {
			t.Fatalf("%d: %v %v", status, tok, err)
		}
	}
}

// TestPaste_AKeyTheAPICannotBeAskedAboutIsTakenUnverified: anything else
// takes the key, and unverified says why, without the key.
func TestPaste_AKeyTheAPICannotBeAskedAboutIsTakenUnverified(t *testing.T) {
	for _, status := range []int{429, 500} {
		fake.Registry(t)
		models(t, status)
		tok, unverified, err := rota.Paste(ctx, "claude", key)
		if err != nil || tok == nil || tok.Credential != "apikey" || unverified == nil || strings.Contains(unverified.Error(), "SUITEONLY") {
			t.Fatalf("%d: %v %v %v", status, tok, unverified, err)
		}
	}
}

// TestPaste_TheKeyIsNeverCarriedAlongARedirect: the check follows no
// redirect, because the key travels in a header that would go along. A
// redirect is no refusal, so the key is taken unchecked.
func TestPaste_TheKeyIsNeverCarriedAlongARedirect(t *testing.T) {
	fake.Registry(t)
	s := fake.NewServer(t)
	s.Claude(t)
	s.Handle("/moved", func(*http.Request, map[string]any) (int, any) { return 200, fake.Reply{"data": []any{}} })
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.URL+"/moved", http.StatusFound)
	}))
	t.Cleanup(elsewhere.Close)
	fake.Restore(t, &rota.ClaudeModelsEndpoint, elsewhere.URL+"/models")
	tok, unverified, err := rota.Paste(ctx, "claude", key)
	if err != nil || tok == nil || unverified == nil {
		t.Fatalf("%v %v %v", tok, unverified, err)
	}
	if s.Hits("/moved") != 0 {
		t.Fatal("the key followed a redirect")
	}
}

// TestPaste_ASetupTokenIsALongTokenAndAsksNobody: a token `claude
// setup-token` printed is good for a year from now, said to be one, and
// checked by nobody, since nothing can say whose it is.
func TestPaste_ASetupTokenIsALongTokenAndAsksNobody(t *testing.T) {
	fake.Registry(t)
	s := models(t, 200)
	at := time.Unix(1_700_000_000, 0)
	fake.Clock(t, at)
	tok, unverified, err := rota.Paste(ctx, "claude", setup)
	if err != nil || unverified != nil || tok.Credential != "setup-token" || tok.Access != setup {
		t.Fatalf("%+v %v %v", tok, unverified, err)
	}
	if want := at.AddDate(0, 0, 365).UnixMilli(); tok.ExpiresAt != want {
		t.Fatalf("expires %d, want a year from now %d", tok.ExpiresAt, want)
	}
	if len(s.Requests()) != 0 {
		t.Fatalf("nobody is asked: %+v", s.Requests())
	}
}

// TestPaste_NeitherIsRefusedUnrepeated: nothing that is neither is taken,
// no error carries it, and no other provider takes a paste at all.
func TestPaste_NeitherIsRefusedUnrepeated(t *testing.T) {
	fake.Registry(t)
	s := models(t, 200)
	for _, pasted := range []string{"", "SUITEONLY-plain", "sk-ant-api03-SUITEONLY with a space", "sk-ant-sid01-SUITEONLY0123456789abcdef"} {
		tok, _, err := rota.Paste(ctx, "claude", pasted)
		if tok != nil || !errors.Is(err, rota.ErrInvalidRequest) || strings.Contains(err.Error(), "SUITEONLY") {
			t.Fatalf("%q: %v %v", pasted, tok, err)
		}
	}
	for _, provider := range []string{"codex", "grok", "kimi"} {
		if tok, _, err := rota.Paste(ctx, provider, key); tok != nil || !errors.Is(err, rota.ErrInvalidRequest) {
			t.Fatalf("%s: %v %v", provider, tok, err)
		}
	}
	if len(s.Requests()) != 0 {
		t.Fatalf("a refusal by shape asks nobody: %+v", s.Requests())
	}
}

// TestAPIKey_SaysWhatTheAccountRunsOn.
func TestAPIKey_SaysWhatTheAccountRunsOn(t *testing.T) {
	fake.Registry(t)
	models(t, 200)
	tok, _, err := rota.Paste(ctx, "claude", key)
	if err != nil {
		t.Fatal(err)
	}
	if a := rota.NewAccount(1, "claude", tok); !a.APIKey() || a.Credential != "apikey" {
		t.Fatalf("an account made from a pasted key runs on it: %+v", a)
	}
	if a := rota.NewAccount(2, "claude", &rota.Token{Access: "tok"}); a.APIKey() {
		t.Fatal("an ordinary login does not")
	}
}

// TestLoginKey_AddsAnAccountOutOfTheRotation: order 0, because every run
// on it costs money, in a store that stays ordered so the next open keeps
// it there; the same key again updates that account, and a setup token is
// not a key.
func TestLoginKey_AddsAnAccountOutOfTheRotation(t *testing.T) {
	fake.Registry(t)
	models(t, 200)
	s := openStore(t, "")
	a, added, unverified, err := s.LoginKey(ctx, "claude", key, "work")
	if err != nil || unverified != nil || !added || !a.APIKey() || a.Order != 0 || a.Name != "work" || a.Label() != "work" {
		t.Fatalf("%+v %v %v %v", a, added, unverified, err)
	}
	again, added, _, err := s.LoginKey(ctx, "claude", key, "")
	if err != nil || added || again.ID != a.ID || again.Name != "work" {
		t.Fatalf("the same key is the same account, its name kept: %+v %v %v", again, added, err)
	}
	if _, _, _, err := s.LoginKey(ctx, "claude", setup, ""); !errors.Is(err, rota.ErrInvalidRequest) || strings.Contains(err.Error(), "SUITEONLY") {
		t.Fatalf("a setup token makes no account: %v", err)
	}
	if _, _, _, err := s.LoginKey(ctx, "claude", key, "two\nlines"); err == nil {
		t.Fatal("a label is one line")
	}
}

// TestPasteLong_PutsASetupTokenOnTheAccountNamed, as its long-lived token;
// a key account takes none, and a key is no setup token.
func TestPasteLong_PutsASetupTokenOnTheAccountNamed(t *testing.T) {
	fake.Registry(t)
	models(t, 200)
	s := openStore(t, `{"accounts":[{"id":1,"provider":"claude","email":"c@x","token":{"accessToken":"tok","expiresAt":0}}]}`)
	at := time.Unix(1_700_000_000, 0)
	fake.Clock(t, at)
	a, err := s.PasteLong(ctx, 1, setup)
	if err != nil || a.ID != 1 || a.Long == nil || a.Long.Access != setup || a.Long.ExpiresAt != at.AddDate(0, 0, 365).UnixMilli() {
		t.Fatalf("%+v %v", a, err)
	}
	if a.Token.Access != "tok" || a.APIKey() {
		t.Fatalf("the ordinary login is left as it was: %+v", a)
	}
	if _, err := s.PasteLong(ctx, 1, key); !errors.Is(err, rota.ErrInvalidRequest) || strings.Contains(err.Error(), "SUITEONLY") {
		t.Fatalf("a key is no setup token: %v", err)
	}
	keyed, _, _, err := s.LoginKey(ctx, "claude", key, "")
	if err != nil {
		t.Fatal(err)
	}
	if a, err := s.PasteLong(ctx, keyed.ID, setup); !errors.Is(err, rota.ErrInvalidRequest) || a != nil {
		t.Fatalf("a key account takes no setup token: %v %v", a, err)
	}
	if _, err := s.PasteLong(ctx, 99, setup); !errors.Is(err, rota.ErrNoAccount) {
		t.Fatalf("nobody: %v", err)
	}
	if store.PastedLongCaveat == "" {
		t.Fatal("there is one line to say about a pasted token")
	}
}

// TestHoldsSecret_FindsAnAnthropicCredentialStandingAsAWord, and
// TestUnsecret_TakesItOut: what every error rota prints passes through.
func TestHoldsSecret_FindsAnAnthropicCredentialStandingAsAWord(t *testing.T) {
	for s, want := range map[string]bool{
		key:                         true,
		setup:                       true,
		"/srv/" + key:               true,
		"use " + key + " now":       true,
		"sk-ant-api03-short":        false,
		"xsk-ant-api03-" + key[13:]: false,
		"nothing here":              false,
	} {
		if got := wire.HoldsSecret(s); got != want {
			t.Fatalf("HoldsSecret(%q) = %v", strings.ReplaceAll(s, "SUITEONLY", "…"), got)
		}
	}
}

func TestUnsecret_TakesItOut(t *testing.T) {
	got := wire.Unsecret("refused " + key + " and " + setup + " for /srv/x")
	if strings.Contains(got, "SUITEONLY") || !strings.HasPrefix(got, "refused ") || !strings.HasSuffix(got, " for /srv/x") {
		t.Fatalf("%q", got)
	}
	if got := wire.Unsecret("nothing here"); got != "nothing here" {
		t.Fatalf("a message without one is left as it is: %q", got)
	}
}

// openStore is a store in a fresh directory holding accounts, when there
// are any, closed with the test.
func openStore(t *testing.T, accounts string) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if accounts != "" {
		if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(accounts), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := store.NewFileBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
