package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// exp is the exp claim of every access token here: far enough out that no
// token expires by accident, and a round number in the assertions.
const exp = int64(4102444800)

// tokenPath is where fake.Server.Codex points the token endpoint.
const tokenPath = "/codex/token"

// pinned is the clock every test runs under, so last_refresh and expiries
// are exact values rather than "about now".
var pinned = time.Unix(1_700_000_000, 0)

var ctx = context.Background()

// server points codex at a fresh fake server and pins the clock. Even tests
// that expect no request use it, so a stray one is counted, not sent.
func server(t *testing.T) *fake.Server {
	t.Helper()
	s := fake.NewServer(t)
	s.Codex(t)
	fake.Clock(t, pinned)
	return s
}

// want checks one form field. Handlers run on the server goroutine, so
// this must never call Fatal.
func want(t *testing.T, body map[string]any, key, val string) {
	t.Helper()
	if got, _ := body[key].(string); got != val {
		t.Errorf("%s = %q, want %q", key, got, val)
	}
}

// nonEmpty checks a form field is present with some value.
func nonEmpty(t *testing.T, body map[string]any, key string) {
	t.Helper()
	if got, _ := body[key].(string); got == "" {
		t.Errorf("%s is empty", key)
	}
}

// account is a codex account with a live access token; extra is folded in.
func account(refresh string, extra map[string]string) *rota.Account {
	return rota.NewAccount(3, "codex", &rota.Token{
		Access: fake.JWT(fake.Reply{"exp": exp}), Refresh: refresh, ExpiresAt: exp * 1000, Extra: extra,
	})
}

// fresh is an account straight out of a login: id_token and account id known.
func fresh() *rota.Account {
	return account("R1", map[string]string{"id_token": fake.JWT(fake.Reply{"sub": "s1"}), "account_id": "acct"})
}

// staged is an account whose auth.json provenance is unknown, so adoption
// treats whatever the file holds as the CLI's own rotation.
func staged() *rota.Account {
	a := rota.NewAccount(3, "codex", &rota.Token{
		Access: "old", Refresh: "R1", Extra: map[string]string{"id_token": "old-idt", "account_id": "acct"},
	})
	a.Staged = ""
	return a
}

// cliFile is the auth.json the CLI leaves after rotating to refresh.
func cliFile(refresh, accountID string) []byte {
	raw, _ := json.Marshal(map[string]any{"tokens": map[string]any{
		"refresh_token": refresh, "access_token": fake.JWT(fake.Reply{"exp": exp}),
		"id_token": "idt", "account_id": accountID,
	}})
	return raw
}

// authDoc is the document rota stages for the CLI.
type authDoc struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

func decodeAuth(t *testing.T, raw []byte) authDoc {
	t.Helper()
	var d authDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("auth.json does not decode: %v\n%s", err, raw)
	}
	return d
}

// homeWith is a private home holding one file.
func homeWith(t *testing.T, name string, content []byte) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, name), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func ids(models []rota.Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func TestCodexBegin_URLIsAuthorizeWithPKCE(t *testing.T) {
	s := server(t)
	l, err := rota.Begin(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.URL, s.URL+"/authorize") {
		t.Fatalf("URL = %s", l.URL)
	}
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge") == "" {
		t.Fatalf("no code_challenge in %s", l.URL)
	}
	if !strings.Contains(q.Get("redirect_uri"), "localhost") {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("Begin made %d requests; the authorize URL is handed over, never fetched", n)
	}
}

func TestCodexComplete_FormEncodedExchange(t *testing.T) {
	s := server(t)
	s.Handle(tokenPath, func(r *http.Request, body map[string]any) (int, any) {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q", ct)
		}
		want(t, body, "grant_type", "authorization_code")
		want(t, body, "code", "C1")
		nonEmpty(t, body, "code_verifier")
		nonEmpty(t, body, "redirect_uri")
		return 200, fake.CodexToken("R1", "s1", "c@x", "acct", exp)
	})
	l, err := rota.Begin(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := l.Complete(ctx, "C1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != fake.JWT(fake.Reply{"exp": exp}) || tok.Refresh != "R1" {
		t.Fatalf("access=%q refresh=%q", tok.Access, tok.Refresh)
	}
	// expires_in wins over the JWT's own exp, and the fake reply always
	// carries 864000 s of it.
	if want := pinned.UnixMilli() + 864000*1000; tok.ExpiresAt != want {
		t.Fatalf("ExpiresAt = %d, want %d", tok.ExpiresAt, want)
	}
	if tok.Identity == nil || tok.Identity.UUID != "s1" || tok.Identity.Email != "c@x" {
		t.Fatalf("identity = %+v", tok.Identity)
	}
	if tok.Extra["id_token"] == "" {
		t.Fatalf("id_token not carried: %v", tok.Extra)
	}
	if tok.Extra["account_id"] != "acct" {
		t.Fatalf("account_id = %q", tok.Extra["account_id"])
	}
}

func TestCodexComplete_AcceptsWholeRedirectURL(t *testing.T) {
	s := server(t)
	s.Handle(tokenPath, func(_ *http.Request, body map[string]any) (int, any) {
		want(t, body, "code", "C9")
		return 200, fake.CodexToken("R1", "s1", "c@x", "acct", exp)
	})
	l, err := rota.Begin(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Complete(ctx, "http://localhost:1455/auth/callback?code=C9&state=S"); err != nil {
		t.Fatal(err)
	}
	if s.Hits(tokenPath) != 1 {
		t.Fatalf("token endpoint hit %d times", s.Hits(tokenPath))
	}
}

func TestCodexComplete_InvalidGrantIsOAuthError(t *testing.T) {
	s := server(t)
	s.Handle(tokenPath, func(_ *http.Request, _ map[string]any) (int, any) {
		return 400, fake.OAuthReject("invalid_grant", "bad code")
	})
	l, err := rota.Begin(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Complete(ctx, "C1")
	var oe *rota.OAuthError
	if !errors.As(err, &oe) {
		t.Fatalf("err = %v (%T), want *rota.OAuthError", err, err)
	}
	if oe.Code != "invalid_grant" || oe.Description != "bad code" {
		t.Fatalf("code=%q description=%q", oe.Code, oe.Description)
	}
}

func TestCodexRefresh_SendsScopeAndRotates(t *testing.T) {
	s := server(t)
	s.Handle(tokenPath, func(_ *http.Request, body map[string]any) (int, any) {
		want(t, body, "grant_type", "refresh_token")
		want(t, body, "refresh_token", "R1")
		want(t, body, "scope", "openid profile email")
		return 200, fake.CodexToken("R2", "s1", "c@x", "acct", exp)
	})
	old := fake.JWT(fake.Reply{"sub": "s1"})
	a := rota.NewAccount(3, "codex", &rota.Token{
		Access: fake.JWT(fake.Reply{"exp": 1}), Refresh: "R1", ExpiresAt: 1,
		Extra: map[string]string{"id_token": old},
	})
	changed, err := rota.Refresh(ctx, a)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if a.Token.Refresh != "R2" {
		t.Fatalf("refresh = %q", a.Token.Refresh)
	}
	if got := a.Extra["id_token"]; got == "" || got == old {
		t.Fatalf("id_token not replaced: %q", got)
	}
	if s.Hits(tokenPath) != 1 {
		t.Fatalf("token endpoint hit %d times", s.Hits(tokenPath))
	}
}

func TestCodexRefresh_ReusedTokenIsDead(t *testing.T) {
	s := server(t)
	s.Handle(tokenPath, func(_ *http.Request, _ map[string]any) (int, any) {
		return 400, fake.OAuthReject("refresh_token_reused", "")
	})
	a := rota.NewAccount(3, "codex", &rota.Token{Access: "old", Refresh: "R1", ExpiresAt: 1})
	changed, err := rota.Refresh(ctx, a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestCodexStagePlan_TouchesNoDiskReturnsAuthJSON(t *testing.T) {
	s := server(t)
	a := fresh()
	home := t.TempDir()
	cmd, files, err := rota.StagePlan(ctx, a, home)
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil || cmd.Bin != "codex" {
		t.Fatalf("cmd = %+v", cmd)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("StagePlan wrote into home: %v", entries)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	f := files[0]
	if filepath.Base(f.Path) != "auth.json" || f.Mode != 0o600 {
		t.Fatalf("path=%q mode=%o", f.Path, f.Mode)
	}
	d := decodeAuth(t, f.Content)
	if d.Tokens.RefreshToken != "R1" || d.Tokens.AccountID != "acct" || d.AuthMode != "chatgpt" {
		t.Fatalf("doc = %+v", d)
	}
	at, err := time.Parse(time.RFC3339, d.LastRefresh)
	if err != nil {
		t.Fatalf("last_refresh %q: %v", d.LastRefresh, err)
	}
	if !at.Equal(pinned) {
		t.Fatalf("last_refresh = %s, want %s", at, pinned)
	}
	if s.Hits(tokenPath) != 0 {
		t.Fatalf("an account with an id_token needs no refresh; endpoint hit %d times", s.Hits(tokenPath))
	}
}

func TestCodexStagePlan_RefusesEmptyHome(t *testing.T) {
	server(t)
	if _, _, err := rota.StagePlan(ctx, fresh(), ""); err == nil {
		t.Fatal("StagePlan accepted an empty home")
	}
}

func TestCodexPlan_RepairsMissingIDTokenByRefreshing(t *testing.T) {
	s := server(t)
	reply := fake.CodexToken("R2", "s1", "c@x", "acct", exp)
	s.Handle(tokenPath, func(_ *http.Request, body map[string]any) (int, any) {
		want(t, body, "grant_type", "refresh_token")
		want(t, body, "refresh_token", "R1")
		return 200, reply
	})
	a := account("R1", nil)
	_, files, err := rota.StagePlan(ctx, a, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Hits(tokenPath) != 1 {
		t.Fatalf("token endpoint hit %d times, want exactly one repair", s.Hits(tokenPath))
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	d := decodeAuth(t, files[0].Content)
	if d.Tokens.IDToken == "" || d.Tokens.IDToken != reply["id_token"] {
		t.Fatalf("id_token = %q, want the one the refresh returned", d.Tokens.IDToken)
	}
	if d.Tokens.RefreshToken != "R2" || a.Token.Refresh != "R2" {
		t.Fatalf("file refresh=%q account refresh=%q", d.Tokens.RefreshToken, a.Token.Refresh)
	}
}

func TestCodexPlan_NoIDTokenNoRefreshIsReauth(t *testing.T) {
	s := server(t)
	a := account("", nil)
	_, _, err := rota.StagePlan(ctx, a, t.TempDir())
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("err = %v, want ErrReauth", err)
	}
	if !strings.Contains(err.Error(), "id_token") {
		t.Fatalf("message does not name the missing id_token: %v", err)
	}
	if s.Hits(tokenPath) != 0 {
		t.Fatalf("nothing to refresh with, yet endpoint hit %d times", s.Hits(tokenPath))
	}
}

func TestCodexStage_WritesAuthJSONAndEnv(t *testing.T) {
	server(t)
	a := fresh()
	home := filepath.Join(t.TempDir(), "codex-3") // Stage creates it
	cmd, err := rota.Stage(a, home)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode = %o", st.Mode().Perm())
	}
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d := decodeAuth(t, raw); d.Tokens.RefreshToken != "R1" {
		t.Fatalf("staged refresh = %q", d.Tokens.RefreshToken)
	}
	if cmd.Bin != "codex" || !slices.Contains(cmd.Env, "CODEX_HOME="+home) {
		t.Fatalf("cmd = %+v", cmd)
	}
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_BASE_URL"} {
		if !slices.Contains(cmd.Drop, k) {
			t.Fatalf("Drop lacks %s: %v", k, cmd.Drop)
		}
	}
}

func TestCodexAdopt_TakesRotatedRefreshToken(t *testing.T) {
	a := staged()
	home := homeWith(t, "auth.json", cliFile("R3", "acct"))
	if err := rota.Adopt(a, home); err != nil {
		t.Fatal(err)
	}
	if a.Token.Refresh != "R3" {
		t.Fatalf("refresh = %q, want the CLI's R3", a.Token.Refresh)
	}
	// The CLI records no expiry, so it comes from the access token's exp.
	if a.Token.Access != fake.JWT(fake.Reply{"exp": exp}) || a.Token.ExpiresAt != exp*1000 {
		t.Fatalf("access=%q expiresAt=%d", a.Token.Access, a.Token.ExpiresAt)
	}
	if a.Extra["id_token"] != "idt" {
		t.Fatalf("id_token = %q", a.Extra["id_token"])
	}
}

func TestCodexAdopt_IgnoresFileOfAnotherAccount(t *testing.T) {
	a := staged()
	home := homeWith(t, "auth.json", cliFile("R3", "other"))
	if err := rota.Adopt(a, home); err != nil {
		t.Fatal(err)
	}
	if a.Token.Refresh != "R1" {
		t.Fatalf("refresh = %q; another account's file must not be adopted", a.Token.Refresh)
	}
}

func TestCodexAdopt_IgnoresFileThatPredatesLogin(t *testing.T) {
	a := staged()
	a.Staged = "-"
	home := homeWith(t, "auth.json", cliFile("R3", "acct"))
	if err := rota.Adopt(a, home); err != nil {
		t.Fatal(err)
	}
	if a.Token.Refresh != "R1" {
		t.Fatalf("refresh = %q; a file older than the login must not be adopted", a.Token.Refresh)
	}
}

func TestCodexAdoptFrom_ReadsAnyFS(t *testing.T) {
	a := staged()
	fsys := fstest.MapFS{"auth.json": &fstest.MapFile{Data: cliFile("R3", "acct")}}
	if err := rota.AdoptFrom(a, fsys); err != nil {
		t.Fatal(err)
	}
	if a.Token.Refresh != "R3" {
		t.Fatalf("refresh = %q, want R3 from the in-memory home", a.Token.Refresh)
	}
}

func TestCodexModelsFor_ReadsModelsCache(t *testing.T) {
	cache := `{"models":[{"slug":"m-a","visibility":"list"},{"slug":"m-b","visibility":"hidden"},{"slug":"","visibility":"list"}]}`
	home := homeWith(t, "models_cache.json", []byte(cache))
	a := fresh()
	if got := ids(rota.ModelsFor(a, home)); !slices.Equal(got, []string{"m-a"}) {
		t.Fatalf("ModelsFor with cache = %v, want [m-a]", got)
	}
	// No cache: the provider's own list, which ships five models.
	shipped := ids(rota.Models("codex"))
	if len(shipped) != 5 {
		t.Fatalf("shipped catalog = %v", shipped)
	}
	if got := ids(rota.ModelsFor(a, t.TempDir())); !slices.Equal(got, shipped) {
		t.Fatalf("ModelsFor without cache = %v, want %v", got, shipped)
	}
}

func TestCodexDefaults_EmptyModelMediumEffort(t *testing.T) {
	model, effort := rota.Defaults("codex")
	if model != "" || effort != "medium" {
		t.Fatalf("Defaults = (%q, %q)", model, effort)
	}
	if efforts := rota.Efforts("codex"); !slices.Contains(efforts, "ultra") {
		t.Fatalf("Efforts = %v, want ultra among them", efforts)
	}
}
