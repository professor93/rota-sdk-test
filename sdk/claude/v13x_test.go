package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
	"rotatest/internal/release"
)

// What 1.3.0, 1.3.1 and 1.3.2 promise a program that keeps a claude
// account's login in a home of its own: the stored route, the credential
// store as Claude Code keeps it, adoption of what Claude Code did there, and
// the variables that must never reach the CLI. Each case names the release
// it pins and skips against an older module.

// livingClaude is an account whose login can be kept in a home: a refresh
// token and an expiry an hour after the pinned clock.
func livingClaude() *rota.Account {
	return rota.NewAccount(1, "claude", &rota.Token{Access: "A0", Refresh: "R0", ExpiresAt: atMS + 3_600_000})
}

// homeWith is a home whose credential store holds this login entry, plus
// whatever else the store keeps beside it.
func homeWith(login map[string]any, beside map[string]any) fstest.MapFS {
	doc := map[string]any{}
	for k, v := range beside {
		doc[k] = v
	}
	if login != nil {
		doc["claudeAiOauth"] = login
	}
	raw, _ := json.Marshal(doc)
	return fstest.MapFS{".credentials.json": &fstest.MapFile{Data: raw, Mode: 0o600}}
}

// loginEntry is a claudeAiOauth entry as Claude Code writes it. until is its
// refreshTokenExpiresAt, left out when zero.
func loginEntry(access, refresh string, expires, until int64) map[string]any {
	l := map[string]any{"accessToken": access, "refreshToken": refresh, "expiresAt": expires, "scopes": []string{"user:inference"}}
	if until != 0 {
		l["refreshTokenExpiresAt"] = until
	}
	return l
}

// A home is where the stored route keeps a login, and a home is all it
// needs from the account: a refresh token and an expiry, alive. Windows keeps
// the environment route for claude in every case.
func TestStoresLogin_ClaudeNeedsAHomeARefreshTokenAndAnExpiry(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	home := t.TempDir()
	stored := runtime.GOOS != "windows"
	if got := rota.StoresLogin(livingClaude(), home); got != stored {
		t.Fatalf("a living login with a home: %v, want %v", got, stored)
	}
	if rota.StoresLogin(livingClaude(), "") {
		t.Fatal("no home, no stored login")
	}
	noRefresh := livingClaude()
	noRefresh.Token.Refresh = ""
	noExpiry := livingClaude()
	noExpiry.Token.ExpiresAt = 0
	dead := livingClaude()
	dead.Dead = true
	for name, a := range map[string]*rota.Account{"no refresh token": noRefresh, "no expiry": noExpiry, "dead": dead} {
		if rota.StoresLogin(a, home) {
			t.Fatalf("%s: a bare token is not a login Claude Code can keep", name)
		}
	}
	if !rota.SharedHome("claude") || rota.SharedHome("codex") || rota.SharedHome("grok") {
		t.Fatal("only claude shares a home between many processes")
	}
}

// StagePlan on the stored route points Claude Code at the home, gives it no
// token, drops one it would inherit, and plans the credential store holding
// the login until the application records that it wrote it. It touches no
// disk.
func TestStagePlan_ClaudeStoredRoutePlansTheLoginUntilItIsWritten(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	if runtime.GOOS == "windows" {
		t.Skip("windows keeps the environment route for claude")
	}
	setup(t)
	home := t.TempDir()
	a := livingClaude()
	cmd, files, err := rota.StagePlan(context.Background(), a, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR="+home) {
		t.Fatalf("the CLI is pointed at the home: %v", cmd.Env)
	}
	if slices.ContainsFunc(cmd.Env, func(e string) bool { return strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") }) {
		t.Fatalf("no token in the environment on the stored route: %v", cmd.Env)
	}
	if !slices.Contains(cmd.Drop, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("an inherited token would outrank the stored login: drop %v", cmd.Drop)
	}
	if len(files) != 1 || files[0].Path != ".credentials.json" || files[0].Mode != 0o600 {
		t.Fatalf("one credential store, private: %+v", files)
	}
	var doc struct {
		Login struct {
			AccessToken  string  `json:"accessToken"`
			RefreshToken string  `json:"refreshToken"`
			ExpiresAt    float64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(files[0].Content, &doc); err != nil {
		t.Fatalf("the planned store: %v\n%s", err, files[0].Content)
	}
	if doc.Login.AccessToken != "A0" || doc.Login.RefreshToken != "R0" || int64(doc.Login.ExpiresAt) != a.Token.ExpiresAt {
		t.Fatalf("the planned login is the account's: %s", files[0].Content)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("StagePlan writes nothing: %v", entries)
	}

	a.StagedWritten()
	if _, files, err = rota.StagePlan(context.Background(), a, home); err != nil || len(files) != 0 {
		t.Fatalf("a home holding the current login is planned nothing: %+v %v", files, err)
	}
}

// JoinHome starts Claude Code on whatever the home holds, whatever state the
// account is in: a dead account's home somebody signed in again inside is
// joined with nothing of the account's handed over.
func TestJoinHome_ClaudeJoinsWhateverTheHomeHolds(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	home := t.TempDir()
	dead := livingClaude()
	dead.Dead = true
	cmd := rota.JoinHome(dead, home)
	if runtime.GOOS == "windows" {
		if cmd != nil {
			t.Fatalf("windows joins no home: %+v", cmd)
		}
		return
	}
	if cmd == nil || !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR="+home) || !slices.Contains(cmd.Drop, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("pointed at the home, no token: %+v", cmd)
	}
	if slices.ContainsFunc(cmd.Env, func(e string) bool { return strings.Contains(e, "A0") || strings.Contains(e, "R0") }) {
		t.Fatalf("nothing of the account's credential: %v", cmd.Env)
	}
	if rota.JoinHome(dead, "") != nil {
		t.Fatal("no home, nothing to join")
	}
	if rota.JoinHome(rota.NewAccount(2, "grok", &rota.Token{Access: "xai"}), home) != nil {
		t.Fatal("a provider whose home is not shared has nothing to join")
	}
}

// The credential store is more than the login: Claude Code keeps the OAuth
// tokens of MCP servers there. A planned login replaces the login entry and
// nothing else, byte for byte and in place.
func TestMergeClaudeCredentials_ReplacesOnlyTheLogin(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	existing := `{"mcpOAuth":{"srv":{"accessToken":"mcp-secret","expiresAt":1.50}},"claudeAiOauth":{"accessToken":"old"},"zeta":[1, 2]}`
	staged := `{"claudeAiOauth":{"accessToken":"new","refreshToken":"R1","expiresAt":5}}`
	got, err := rota.MergeClaudeCredentials([]byte(existing), []byte(staged))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"mcpOAuth":{"srv":{"accessToken":"mcp-secret","expiresAt":1.50}},"claudeAiOauth":{"accessToken":"new","refreshToken":"R1","expiresAt":5},"zeta":[1, 2]}`
	if string(got) != want {
		t.Fatalf("merged\n%s\nwant\n%s", got, want)
	}
	for _, none := range []string{"", "   ", "{not json"} {
		got, err := rota.MergeClaudeCredentials([]byte(none), []byte(staged))
		if err != nil || !strings.Contains(string(got), `"refreshToken":"R1"`) || !json.Valid(got) {
			t.Fatalf("%q is a store holding nothing: %s %v", none, got, err)
		}
	}
	if _, err := rota.MergeClaudeCredentials([]byte(existing), []byte(`{"mcpOAuth":{}}`)); !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("a planned store with no login is a caller's mistake: %v", err)
	}
}

// A store holds a login Claude Code can use when it has a refresh token and
// an expiry; an empty refresh token is how Claude Code gives one up.
func TestHoldsLogin_ClaudeIsARefreshTokenAndAnExpiry(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	a := livingClaude()
	cases := []struct {
		name string
		fsys fstest.MapFS
		want bool
	}{
		{"a login", homeWith(loginEntry("A", "R", 5, 0), nil), true},
		{"blanked", homeWith(loginEntry("", "", 0, 0), nil), false},
		{"no expiry", homeWith(loginEntry("A", "R", 0, 0), nil), false},
		{"no login entry", homeWith(nil, map[string]any{"mcpOAuth": map[string]any{}}), false},
		{"no store", fstest.MapFS{}, false},
	}
	for _, c := range cases {
		if got := rota.HoldsLogin(a, c.fsys); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if rota.HoldsLogin(rota.NewAccount(2, "grok", &rota.Token{Access: "x"}), homeWith(loginEntry("A", "R", 5, 0), nil)) {
		t.Error("a provider whose home is not shared holds no login there")
	}
}

// A refresh by Claude Code keeps the login's refreshTokenExpiresAt, and that
// is how a rotation is told from a new login: it is taken with no network
// call, and the account learns when its login ends.
func TestAdoptFrom_ClaudeTakesARotationWithoutAskingAnybody(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	s := setup(t)
	until := atMS + 30*86_400_000
	a := livingClaude()
	a.StagedWritten()
	if err := rota.AdoptFrom(a, homeWith(loginEntry("A0", "R0", a.Token.ExpiresAt, until), nil)); err != nil {
		t.Fatal(err)
	}
	if a.LoginUntil().UnixMilli() != until {
		t.Fatalf("the account learns when its login ends: %v", a.LoginUntil())
	}
	later := a.Token.ExpiresAt + 8*3_600_000
	if err := rota.AdoptFrom(a, homeWith(loginEntry("A1", "R1", later, until), nil)); err != nil {
		t.Fatal(err)
	}
	if a.Token.Access != "A1" || a.Token.Refresh != "R1" || a.Token.ExpiresAt != later {
		t.Fatalf("the rotation is taken: %+v", a.Token)
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("with no network call: %d requests", n)
	}
}

// A login with another refreshTokenExpiresAt is somebody running /login
// inside the home. Nothing of it is taken until the application has asked
// the provider whose it is; then Accept takes it, and Refuse leaves the
// account's tokens exactly as they were and names who the home is signed in
// as.
func TestAdoptFrom_ClaudeNewLoginIsAskedAboutBeforeItIsTaken(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	s := setup(t)
	s.Handle("/profile", func(r *http.Request, _ map[string]any) (int, any) {
		if r.Header.Get("Authorization") != "Bearer A9" {
			return 401, fake.Reply{"error": "who"}
		}
		return 200, fake.ClaudeProfile("u-other", "other@x", "org-2")
	})
	fresh := func() *rota.Account {
		a := livingClaude()
		a.UUID = "u1"
		a.Extra = map[string]string{}
		a.StagedWritten()
		if err := rota.AdoptFrom(a, homeWith(loginEntry("A0", "R0", a.Token.ExpiresAt, atMS+86_400_000), nil)); err != nil {
			t.Fatal(err)
		}
		return a
	}
	home := homeWith(loginEntry("A9", "R9", atMS+7_200_000, atMS+90*86_400_000), nil)

	a := fresh()
	err := rota.AdoptFrom(a, home)
	var nl *rota.NewLogin
	if !errors.As(err, &nl) || !errors.Is(err, rota.ErrNewLogin) || nl.Access != "A9" {
		t.Fatalf("a new login comes back to be asked about: %v", err)
	}
	if a.Token.Refresh != "R0" || a.Token.Access != "A0" {
		t.Fatalf("nothing of it is taken yet: %+v", a.Token)
	}
	who, err := nl.Identify(context.Background())
	if err != nil || who == nil || who.Email != "other@x" {
		t.Fatalf("one read of the profile with the login's own token: %+v %v", who, err)
	}
	refusal := nl.Refuse(a, who)
	if !errors.Is(refusal, rota.ErrForeignLogin) || !strings.Contains(refusal.Error(), "other@x") {
		t.Fatalf("a refusal names who the home is signed in as: %v", refusal)
	}
	if a.Token.Refresh != "R0" || a.Token.Access != "A0" || a.Dead {
		t.Fatalf("and leaves the account as it was: %+v dead=%v", a.Token, a.Dead)
	}

	a = fresh()
	if err := rota.AdoptFrom(a, home); !errors.As(err, &nl) {
		t.Fatalf("asked again: %v", err)
	}
	nl.Accept(a)
	if a.Token.Refresh != "R9" || a.Token.Access != "A9" || a.LoginUntil().UnixMilli() != atMS+90*86_400_000 {
		t.Fatalf("accepted, the login is the account's: %+v until %v", a.Token, a.LoginUntil())
	}
}

// A dead account is never refreshed, and comes back only by a fresh login:
// any living login in its home is somebody signing it in again, and taking
// it brings the account back.
func TestAdoptFrom_ClaudeDeadAccountSignedInAgainInsideItsHome(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	a := livingClaude()
	a.StagedWritten()
	a.Dead, a.DeadReason = true, "invalid_grant"
	err := rota.AdoptFrom(a, homeWith(loginEntry("A5", "R5", atMS+3_600_000, 0), nil))
	var nl *rota.NewLogin
	if !errors.As(err, &nl) {
		t.Fatalf("a living login in a dead account's home is a new login: %v", err)
	}
	if !a.Dead {
		t.Fatal("not taken before it is confirmed")
	}
	nl.Accept(a)
	if a.Dead || a.DeadReason != "" || a.Token.Refresh != "R5" {
		t.Fatalf("someone signed it in: dead=%v %q %+v", a.Dead, a.DeadReason, a.Token)
	}
}

// Claude Code blanks the login in its store when a refresh of its own is
// refused. AdoptFrom reads and never refreshes, so the account whose current
// login that was is dead — and since 1.3.1 says Claude Code did it — while a
// blanked older login only means the home holds nothing usable.
func TestAdoptFrom_ClaudeBlankedLogin(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	blanked := homeWith(loginEntry("", "", 0, 0), map[string]any{"mcpOAuth": map[string]any{"srv": map[string]any{"accessToken": "m"}}})

	a := livingClaude()
	a.StagedWritten()
	if err := rota.AdoptFrom(a, blanked); err != nil {
		t.Fatal(err)
	}
	if !a.Dead {
		t.Fatal("the account's current login blanked: dead")
	}
	if !release.Older(rota.Version, "1.3.1") && !strings.Contains(a.DeadReason, "Claude Code cleared the login") {
		t.Fatalf("and says why: %q", a.DeadReason)
	}

	b := livingClaude() // a home older than the account's current login
	if err := rota.AdoptFrom(b, blanked); err != nil {
		t.Fatal(err)
	}
	if b.Dead {
		t.Fatal("a blanked login that is not the current one kills nothing")
	}
	if _, files, err := rota.StagePlan(context.Background(), b, "/home-of-b"); err != nil || (runtime.GOOS != "windows" && len(files) != 1) {
		t.Fatalf("the next staging writes the current login: %d files %v", len(files), err)
	}
}

// A store that is there and cannot be read may be Claude Code halfway
// through writing it: nothing changes, and the error says so.
func TestAdoptFrom_ClaudeUnreadableStoreChangesNothing(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	setup(t)
	a := livingClaude()
	a.StagedWritten()
	before := *a
	err := rota.AdoptFrom(a, fstest.MapFS{".credentials.json": &fstest.MapFile{Data: []byte(`{"claudeAiOauth":{"refreshTok`)}})
	if !errors.Is(err, rota.ErrUnreadableLogin) {
		t.Fatalf("err %v", err)
	}
	if a.Token.Access != before.Token.Access || a.Token.Refresh != before.Token.Refresh ||
		a.Token.ExpiresAt != before.Token.ExpiresAt || a.Staged != before.Staged || a.Dead {
		t.Fatalf("nothing changed: %+v", a)
	}
}

// The login an application wrote into a home and then refreshed away is
// certainly spent: the provider issued its successor, which the account
// holds. That is the one login that may be written over while something runs
// there.
func TestClaudeHomeSpent_TheWrittenLoginRefreshedAway(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	s := setup(t)
	s.Handle("/token", func(*http.Request, map[string]any) (int, any) {
		return 200, fake.ClaudeToken("A1", "R1", 3600, "", "", "")
	})
	a := livingClaude()
	a.Token.ExpiresAt = atMS - 1 // due
	written := homeWith(loginEntry("A0", "R0", a.Token.ExpiresAt, 0), nil)
	a.StagedWritten()
	if rota.ClaudeHomeSpent(a, written) {
		t.Fatal("the current login is not spent")
	}
	if _, err := rota.Refresh(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if a.Token.Refresh != "R1" {
		t.Fatalf("refreshed: %+v", a.Token)
	}
	if !rota.ClaudeHomeSpent(a, written) {
		t.Fatal("the write the account refreshed away is spent")
	}
	if rota.ClaudeHomeSpent(a, homeWith(loginEntry("A1", "R1", atMS+3_600_000, 0), nil)) {
		t.Fatal("a home already holding the successor is not")
	}
	if rota.ClaudeHomeSpent(a, homeWith(loginEntry("Ax", "Rx", atMS, 0), nil)) {
		t.Fatal("a login the application never wrote proves nothing")
	}
}

// These no longer reach a claude account's CLI on any route: each would
// change how it signs in, stop it reading its login at all, or tell it that
// it runs inside Claude Code. 1.3.1 dropped the first six, 1.3.2 the
// federation variables and CLAUDECODE.
func TestEnviron_ClaudeDropsWhatWouldChangeHowItSignsIn(t *testing.T) {
	release.Since(t, rota.Version, "1.3.1")
	setup(t)
	vars := []string{
		"CLAUDE_CODE_SIMPLE", "ANTHROPIC_PROFILE", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_MANTLE",
		"CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN",
	}
	if !release.Older(rota.Version, "1.3.2") {
		vars = append(vars, "ANTHROPIC_FEDERATION_RULE_ID", "ANTHROPIC_ORGANIZATION_ID", "CLAUDECODE")
	}
	inherited := []string{"PATH=/usr/bin:/bin", "KEEP=me"}
	for i, v := range vars {
		inherited = append(inherited, v+"=inherited-"+strconv.Itoa(i))
	}
	creds := rota.ClaudeCredentialVars()
	for _, v := range vars {
		if !slices.Contains(creds, v) {
			t.Errorf("ClaudeCredentialVars lacks %s", v)
		}
	}
	routes := map[string]func() (*rota.Command, error){
		"environment": func() (*rota.Command, error) { return rota.Stage(livingClaude(), "") },
		"stored": func() (*rota.Command, error) {
			cmd, _, err := rota.StagePlan(context.Background(), livingClaude(), t.TempDir())
			return cmd, err
		},
		"joined": func() (*rota.Command, error) { return rota.JoinHome(livingClaude(), t.TempDir()), nil },
	}
	for name, route := range routes {
		cmd, err := route()
		if err != nil {
			t.Fatal(err)
		}
		if cmd == nil {
			continue // windows joins no home
		}
		env := rota.Environ(inherited, cmd)
		for _, v := range vars {
			if slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, v+"=") }) {
				t.Errorf("%s route: %s reaches the CLI", name, v)
			}
		}
		if !slices.Contains(env, "KEEP=me") {
			t.Errorf("%s route: the rest of the environment is kept: %v", name, env)
		}
	}
}

// Since 1.3.0 finishing an ordinary claude login reads the profile once with
// the fresh token, for the plan and the organisation a stored login carries,
// which the credential store then says as Claude Code would. A profile that
// cannot be read never fails a login.
func TestClaudeComplete_ReadsThePlanOnceAndNeverFailsOnIt(t *testing.T) {
	release.Since(t, rota.Version, "1.3.0")
	s := setup(t)
	s.Handle("/token", reply(200, fake.ClaudeToken("A1", "R1", 3600, "u1", "c@x", "o1")))
	s.Handle("/profile", reply(200, fake.Reply{
		"account":      fake.Reply{"uuid": "u1", "email": "c@x"},
		"organization": fake.Reply{"uuid": "o1", "name": "Acme", "organization_type": "claude_max", "rate_limit_tier": "default_claude_max_20x"},
	}))
	tok, err := begin(t).Complete(context.Background(), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Hits("/profile") != 1 {
		t.Fatalf("the profile is read once: %d", s.Hits("/profile"))
	}
	a := rota.NewAccount(1, "claude", tok)
	if runtime.GOOS != "windows" {
		_, files, err := rota.StagePlan(context.Background(), a, t.TempDir())
		if err != nil || len(files) != 1 {
			t.Fatalf("%d files %v", len(files), err)
		}
		var doc struct {
			Login struct {
				Plan string `json:"subscriptionType"`
				Tier string `json:"rateLimitTier"`
			} `json:"claudeAiOauth"`
		}
		if err := json.Unmarshal(files[0].Content, &doc); err != nil || doc.Login.Plan != "max" || doc.Login.Tier != "default_claude_max_20x" {
			t.Fatalf("the stored login carries the plan: %v\n%s", err, files[0].Content)
		}
	}

	s.Handle("/profile", reply(500, fake.Reply{"error": "down"}))
	tok, err = begin(t).Complete(context.Background(), "C2")
	if err != nil {
		t.Fatalf("a profile that cannot be read never fails a login: %v", err)
	}
	if tok.Access != "A1" || tok.Identity == nil || tok.Identity.UUID != "u1" {
		t.Fatalf("the login is whole without it: %+v", tok)
	}
}
