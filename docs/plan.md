# rota SDK consumer suite: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A separate Go module that imports the published `github.com/professor93/rota v1.0.0` and pins every exported method of `lib`, `rotation` and `store` under every condition its contract names, offline; plus opt-in live and real-login suites.

**Architecture:** Black-box tests in external test packages (`sdk_test`, `rotation_test`, `store_test`, `live_test`). The world is replaced through rota's own injection points using `internal/fake`: a composable fake provider, an httptest server playing the claude and codex endpoints, fake vendor CLIs as shell scripts, and a pinned clock. Nothing in rota is modified by this module.

**Tech Stack:** Go 1.27, standard library only. `go test`, `go vet`, `gofmt`.

**Spec:** `docs/spec.md` in this module. **API inventory (read it, it is the contract):** `/Users/inoyatulloh/.claude/jobs/f4cd78ab/tmp/sdk-inventory.md`. **Published source for when the inventory and behaviour disagree:** `/Users/inoyatulloh/go/pkg/mod/github.com/professor93/rota@v1.0.0/` (read-only).

## Global Constraints

- Module `rotatest`, `go 1.27`, `require github.com/professor93/rota v1.0.0`, no other dependency, no `replace`.
- Each task's test file lives in its own package directory so tasks compile independently: `sdk/<name>/<name>_test.go` with `package <name>_test`, where `<name>` is the file stem named in the task with underscores removed (`login`, `claude`, `codex`, `grokkimi`, `token`, `account`, `quotacatalog`, `staging`, `spec`, `run`, `registryjsonerrors`). `rotation/`, `store/`, `live/` stay as named. Helpers a test file needs are defined in that same file; nothing is shared between test packages except `internal/fake`.
- Every test function is named `Test<Symbol>_<Condition>` where `<Symbol>` is the exported rota symbol under test (`Begin`, `Refresh`, `SpecCheck`, `StoreRun`, `Move`, ...) and `<Condition>` says what is pinned. The matrix is generated from these names.
- No `t.Parallel()` anywhere: rota's injection points are package-level.
- Every package-level knob is set through `fake.Restore`, `fake.Clock`, `fake.Registry`, or a `Server.Claude/Codex` call, all of which restore at cleanup. Never assign `rota.Now`, `rota.HTTPClient`, `rota.DefaultRegistry`, `rota.DefaultProvider`, `rota.ExpiryBuffer`, `rota.ClaudeEndpoints.*`, `rota.CodexEndpoints.*` directly.
- Fake providers are named `t-<something>` and registered only after `fake.Registry(t)`.
- Comments are short and say why. No mention of any code-generation tool, assistant, or model vendor anywhere in this module, in code, comments, commit messages or docs. Commit subjects are short, no body.
- When a test fails against v1.0.0: first read the published source to decide whether the inventory or the SDK is right. If the SDK is right, fix the expectation and note the correction in the task report. If the SDK is wrong, keep the test as written, mark it with `t.Skip("finding: <one line>")` at the top so the suite stays green, and report it as a FINDING with file:line in the published source. Never weaken a test to make a real bug pass.
- Run `gofmt -l .`, `go vet ./...` and `go test ./<your package>/...` before reporting.

## Shared helpers (already written): `internal/fake`

```go
func Restore[T any](t testing.TB, p *T, v T)                 // set for the test, restore at cleanup
func Clock(t testing.TB, at time.Time) (advance func(time.Duration))  // pins rota.Now
func Registry(t testing.TB) *rota.Registry                   // scoped copy of DefaultRegistry with the builtins

func New(name string) *Provider   // bare provider: Name_, Kind, URL, BeginErr, CompleteErr, Identity, Token func(code) *rota.Token, Command func(a, home) (*rota.Command, error), BaseEnv; Calls() []string
// Optional abilities, each embeds rota.Provider and stacks:
Refresher{Provider, Fn}  Identifier{Provider, Fn}  Meter{Provider, Fn}  Catalog{Provider, ModelList, EffortList, DefModel, DefEffort}
AccountCatalog{Provider, Fn}  Adopter{Provider, Fn}  FSAdopter{Provider, Fn}  Delegator{Provider, Plan}
Planner{Provider, Fn}  Flavored{Provider, Name_}  Floor{Provider, Is}  SignIn{Provider, Fn}
// Wrappers embed the rota.Provider interface, so stacking two keeps only the outer ability. Combined abilities are concrete types:
FlavoredCatalog{*Provider, FlavorName, ModelList, EffortList, DefModel, DefEffort}   // Flavored + Catalog
func Flavor(p *Provider, flavor string) *FlavoredCatalog  // t-model-1 (alias one), t-model-2; efforts low, high; defaults t-model-1/low
func Claude(p *Provider) *FlavoredCatalog                 // Flavor(p, "claude")
Owning{*Provider, AdoptFn, RefreshFn}                     // Adopter + Refresher, records "adopt"/"refresh" in Calls()
// Any other mix: a local struct embedding *fake.Provider or *fake.FlavoredCatalog plus the methods.

func NewServer(t) *Server                 // httptest; Handle(path, Handler); Requests(); Hits(path)
type Handler func(r *http.Request, body map[string]any) (status int, reply any)  // body: JSON object or parsed form
func (s *Server) Claude(t)                // ClaudeEndpoints → s.URL+/authorize,/token,/profile,/usage; HTTPClient → s.Client()
func (s *Server) Codex(t)                 // CodexEndpoints → s.URL+/authorize,/codex/token
type Reply = map[string]any
func ClaudeToken(access, refresh string, expiresIn int, uuid, email, org string) Reply
func ClaudeProfile(uuid, email, org string) Reply
func ClaudeUsage(fiveHour, sevenDay float64, resetsAt string) Reply
func OAuthReject(code, description string) Reply
func JWT(claims Reply) string
func CodexToken(refresh, sub, email, accountID string, exp int64) Reply

func CLI(t, name, script string) (dir string)   // executable on this process's PATH; put dir in BaseEnv too
func BaseEnv(dir string) []string               // {"PATH=<dir>:/usr/bin:/bin"}
func ClaudeResult(exitCode int) string          // script: result event with STDIN=<prompt> ARGS=<argv>, "fake-stderr" on stderr
func Lines(lines ...string) string              // script printing these lines
```

A complete test, the pattern every task follows:

```go
package sdk_test

import (
	"context"
	"errors"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

func TestRefresh_NonRefresherIsReauthAndDead(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	fake.Clock(t, time.Unix(1_700_000_000, 0))
	a := rota.NewAccount(1, "t-plain", &rota.Token{Access: "a", Refresh: "r", ExpiresAt: 1})
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}
```

Running a fake CLI through `Run`, the pattern for every `Run` test:

```go
dir := fake.CLI(t, "t-cli", fake.ClaudeResult(0))
fake.Registry(t)
p := fake.New("t-run")
p.BaseEnv = fake.BaseEnv(dir)
rota.Register(fake.Claude(p))
a := rota.NewAccount(7, "t-run", &rota.Token{Access: "tok"})
res, err := rota.Run(context.Background(), a, "", nil, rota.Spec{Prompt: "hi"}, nil, nil)
// res.Result == "STDIN=hi ARGS=<claude argv>", res.SessionID == "s-fake", res.Stderr == "fake-stderr"
```

---

### Task 1: `sdk/login_test.go` — Begin, Complete, Login

**Files:** Create `sdk/login_test.go` (package `sdk_test`).

Tests, one per line, each pinned from inventory §1.2:

- `TestBegin_EmptyProviderUsesDefaultProvider`: `fake.Registry(t)`; `fake.Restore(t, &rota.DefaultProvider, "t-d")`; register `t-d`; `Begin(ctx, "")` → `l.Provider == "t-d"`.
- `TestBegin_UnknownProviderIsInvalidRequest`: `errors.Is(err, rota.ErrInvalidRequest)`; message contains `claude`, `codex`, `grok`, `kimi` in that order.
- `TestBegin_IDIsSixHexChars`: `len(l.ID) == 6`, every rune in `0-9a-f`.
- `TestBegin_KindDefaultsToCode`: bare fake → `Kind == "code"`.
- `TestBegin_KindComesFromState`: fake with `Kind: "device"` → `l.Kind == "device"`.
- `TestBegin_DelegatedFollowsDelegatorNotKind`: bare with `Kind: "apikey"` → `Delegated false`; `fake.Delegator{Provider: bare}` with `Kind: "apikey"` → `Delegated true`.
- `TestBegin_BuiltinKinds`: table: claude code/false, codex code/false, grok apikey/true, kimi delegated/true (use `s.Claude(t)`/`s.Codex(t)` with a `fake.NewServer` so no real URL is built from nothing; no request is made by Begin).
- `TestBegin_CreatedAtFollowsInjectedNow`: `fake.Clock(t, time.Unix(1_700_000_000, 0))` → `CreatedAt == 1_700_000_000_000`.
- `TestBegin_ProviderErrorPropagates`: `BeginErr: sentinel` → `errors.Is(err, sentinel)`.
- `TestComplete_TrimsCode`: `Complete(ctx, "  c1\n")` → `p.Calls()` ends with `"complete:c1"`.
- `TestComplete_UnknownProviderInLoginFails`: `l.Provider = "nope"` → error.
- `TestComplete_ProviderErrorPropagatesUnchanged`: `CompleteErr: rota.ErrAuthPending` → `errors.Is(err, rota.ErrAuthPending)`.
- `TestComplete_NoAccessTokenIsInvalidRequest`: `Token: func(string) *rota.Token { return &rota.Token{} }` → `ErrInvalidRequest`, message contains `no access token`.
- `TestComplete_DelegatedTokenNeedsNoAccess`: token `{Delegated: true}` → nil error.
- `TestComplete_IdentifiesWhenTokenHasNoIdentity`: `fake.Identifier{Provider: bare, Fn: returns {UUID:"u"}}` → `tok.Identity.UUID == "u"`.
- `TestComplete_IdentifyErrorIsDiscarded`: Identifier Fn returns error → Complete nil error, `tok.Identity == nil`.
- `TestComplete_TokenIdentityWinsOverIdentifier`: bare `Identity: {UUID:"from-token"}` wrapped in Identifier whose Fn fails the test if called.

- [ ] Write the file. `go vet ./sdk/ && go test ./sdk/ -run 'TestBegin|TestComplete' -v`. Report.

### Task 2: `sdk/claude_test.go` — the real claude provider against the fake server

**Files:** Create `sdk/claude_test.go`.

Setup in every test: `s := fake.NewServer(t); s.Claude(t)`; handlers on `/token`, `/profile`, `/usage`. Account for refresh tests: `rota.NewAccount(1, "claude", &rota.Token{Access: "A0", Refresh: "R0", ExpiresAt: 1})` with `fake.Clock(t, time.Unix(1_700_000_000, 0))` so it is expired.

- `TestClaudeBegin_URLIsAuthorizeWithPKCE`: URL has prefix `s.URL+"/authorize"`; query has `code_challenge`, `state`, `redirect_uri`, `client_id`; `l.State` non-empty.
- `TestClaudeComplete_ExchangesCodeWithVerifier`: `/token` handler asserts `body["grant_type"] == "authorization_code"`, `body["code"] == "C1"`, `body["code_verifier"]` non-empty; returns `fake.ClaudeToken("A1","R1",3600,"u1","e@x","o1")`; token: `Access A1`, `Refresh R1`, `ExpiresAt == now+3600s in ms`, `Scopes == [user:inference user:profile]`, `Identity {u1 e@x o1}`.
- `TestClaudeComplete_CodeWithStateSuffixIsSplit`: code `"C1#ST"` → server body `code == "C1"` and `state == "ST"`.
- `TestClaudeComplete_FallsBackToProfileForIdentity`: token reply without `account` → `/profile` hit with header `Authorization: Bearer A1` → identity from `fake.ClaudeProfile`.
- `TestClaudeComplete_ProfileWithoutUUIDLeavesIdentityEmpty`: profile `{}` → nil error, `Identity == nil` or empty.
- `TestClaudeComplete_InvalidGrantOnCodeIsOAuthError`: 400 `fake.OAuthReject("invalid_grant","gone")` → `errors.As(err, &oe)` with `oe.Code == "invalid_grant"`, not `ErrDeadToken`.
- `TestClaudeComplete_ExpiredAndDeniedAreOAuthErrors`: `expired_token` → `oe.Error()` contains `expired`; `access_denied` → contains `denied`.
- `TestClaudeComplete_AuthorizationPendingIsSentinel`: `authorization_pending` and `slow_down` → `errors.Is(err, rota.ErrAuthPending)`, and `errors.As(&oe)` false.
- `TestClaudeComplete_ServerErrorIsHTTPError`: 500 body `"  boom  "` → `HTTPError{Status:500, Body:"boom"}`, `Error() == "http 500: boom"`; a 400-char body is cut to 300 in `Error()`.
- `TestClaudeComplete_MalformedJSONFails`: 200 `"not json"` → error.
- `TestClaudeComplete_ReplyWithoutAccessTokenFails`: 200 `{}` → error containing `no access token`.
- `TestClaudeRefresh_RotatesAccessKeepsRefreshWhenAbsent`: `/token` asserts `grant_type == "refresh_token"` and `refresh_token == "R0"`; reply `{"access_token":"A2","expires_in":60}` → `changed true`, `Access A2`, `Refresh R0`, `ExpiresAt == now+60s ms`, `Dead false`.
- `TestClaudeRefresh_NewRefreshTokenReplacesOld`: reply with `refresh_token R2` → `Refresh R2`.
- `TestClaudeRefresh_InvalidGrantKillsLineage`: 400 `invalid_grant` → `changed true`, `errors.Is(err, rota.ErrReauth)`, `a.Dead`.
- `TestClaudeRefresh_ScavengesCodeFromObjectShapedError`: 400 `{"error":{"type":"invalid_grant","message":"gone"}}` → same as above.
- `TestClaudeRefresh_TransientFailureLeavesAccountAlone`: 500 → `changed false`, `!errors.Is(err, rota.ErrReauth)`, `errors.As(&he)`, `Access A0`, `Dead false`.
- `TestClaudeRefresh_HonoursContextDeadline`: handler blocks on a channel closed at cleanup; ctx with 200 ms timeout → `errors.Is(err, context.DeadlineExceeded)`, `Dead false`.
- `TestClaudeUsage_ParsesWindowsNoteAndExtra`: `/usage` returns exactly the inventory §3.2 usage fixture as a raw string; assert 3 windows in order `5h 12.5 Primary ResetsAt set`, `7d 40 ResetsAt zero`, `Fable 91 Scoped`; `Note == "extra usage 12.50 / 100.00 USD"`; `Extra == {12.5, 100, USD}`.
- `TestClaudeUsage_SendsBetaHeaderAndBearer`: request header `anthropic-beta == oauth-2025-04-20`, `Authorization == Bearer A0`.
- `TestClaudeUsage_ErrorStatusIsHTTPError`: 429 → `HTTPError.Status == 429`.
- `TestClaudeUsage_OversizeReplyIsRefused`: 200 body of 5 MiB → error, not a hang (bound the test with a 10 s context).
- `TestClaudeLaunch_EnvAndDrops`: `rota.Stage(a, "")` ok; `Env` contains `CLAUDE_CODE_OAUTH_TOKEN=A0`; `Drop` contains `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, and every `rota.NetworkRedirecting()` entry; no `CLAUDE_CONFIG_DIR` in `Env`.
- `TestClaudeLaunch_ConfigDirComesFromAccountNotHome`: `a.ConfigDir = "/tmp/x"`, `Stage(a, t.TempDir())` → `Env` has `CLAUDE_CONFIG_DIR=/tmp/x`.
- `TestClaudeStagePlan_NoFiles`: `StagePlan` → command non-nil, `len(files) == 0`.
- `TestClaudeStage_DeadIsReauth`: `a.Dead = true` → `ErrReauth`.

- [ ] Write. `go test ./sdk/ -run TestClaude -v`. Report.

### Task 3: `sdk/codex_test.go` — the real codex provider against the fake server

**Files:** Create `sdk/codex_test.go`.

Setup: `s := fake.NewServer(t); s.Codex(t)`; handler on `/codex/token` (form-encoded body arrives as `body[...]` strings). `exp := int64(4102444800)`.

- `TestCodexBegin_URLIsAuthorizeWithPKCE`: prefix `s.URL+"/authorize"`, query has `code_challenge`, `redirect_uri` containing `localhost`.
- `TestCodexComplete_FormEncodedExchange`: handler asserts `grant_type authorization_code`, `code C1`, `code_verifier` non-empty, `redirect_uri` non-empty, and `Content-Type` starts with `application/x-www-form-urlencoded`; reply `fake.CodexToken("R1","s1","c@x","acct",exp)`; token: `Access` is the JWT, `Refresh R1`, `ExpiresAt == exp*1000`, `Identity.UUID == "s1"`, `Identity.Email == "c@x"`, `Extra["id_token"]` non-empty.
- `TestCodexComplete_AcceptsWholeRedirectURL`: code `"http://localhost:1455/auth/callback?code=C9&state=S"` → server sees `code == "C9"`.
- `TestCodexComplete_InvalidGrantIsOAuthError`: 400 `fake.OAuthReject("invalid_grant","bad code")` → `oe.Code == "invalid_grant"`, `oe.Description == "bad code"`.
- `TestCodexRefresh_SendsScopeAndRotates`: account `{Access: fake.JWT({exp:1}), Refresh:"R1", ExpiresAt:1, Extra:{"id_token": fake.JWT({sub:"s1"})}}`; handler asserts `grant_type refresh_token`, `refresh_token R1`, `scope == "openid profile email"`; reply `fake.CodexToken("R2",...)` → `changed`, `Refresh R2`, `Extra["id_token"]` replaced.
- `TestCodexRefresh_ReusedTokenIsDead`: 400 `{"error":"refresh_token_reused"}` → `ErrReauth`, `Dead`.
- `TestCodexStagePlan_TouchesNoDiskReturnsAuthJSON`: fresh account with `id_token` in Extra; `home := t.TempDir()`; `StagePlan` → home still empty (`os.ReadDir` len 0); one file `Path` base `auth.json`, `Mode == 0600`; content JSON decodes with `tokens.refresh_token == "R1"`, `tokens.account_id == "acct"`, `auth_mode == "chatgpt"`, `last_refresh` parses RFC3339 and equals the pinned clock.
- `TestCodexStagePlan_RefusesEmptyHome`: `home ""` → error.
- `TestCodexPlan_RepairsMissingIDTokenByRefreshing`: Extra without `id_token`, refresh `R1`, server returns `CodexToken("R2",...)` → `StagePlan` ok, `s.Hits("/codex/token") == 1`, file content has the new id_token.
- `TestCodexPlan_NoIDTokenNoRefreshIsReauth`: Extra empty, `Refresh ""` → `ErrReauth`, message mentions `id_token`.
- `TestCodexStage_WritesAuthJSONAndEnv`: `Stage(a, home)` → `<home>/auth.json` exists with mode `0600`; `Env` contains `CODEX_HOME=<home>`; `Drop` contains `OPENAI_API_KEY`, `CODEX_API_KEY`, `CODEX_ACCESS_TOKEN`, `OPENAI_BASE_URL`.
- `TestCodexAdopt_TakesRotatedRefreshToken`: account `Staged ""`, `Refresh R1`, `Extra["account_id"]` as staged; write `<home>/auth.json` `{"tokens":{"refresh_token":"R3","access_token":fake.JWT({exp}),"id_token":"idt","account_id":"acct"}}` → `rota.Adopt(a, home)` nil; `a.Token.Refresh == "R3"`.
- `TestCodexAdopt_IgnoresFileOfAnotherAccount`: file `account_id "other"` → refresh unchanged.
- `TestCodexAdopt_IgnoresFileThatPredatesLogin`: `a.Staged = "-"` → refresh unchanged.
- `TestCodexAdoptFrom_ReadsAnyFS`: same document in `fstest.MapFS{"auth.json": ...}` → adopted.
- `TestCodexModelsFor_ReadsModelsCache`: write `<home>/models_cache.json` `{"models":[{"slug":"m-a","visibility":"list"},{"slug":"m-b","visibility":"hidden"},{"slug":"","visibility":"list"}]}` (if the SDK expects a different envelope, read `lib/codex.go` in the published source and match it) → `rota.ModelsFor(a, home)` IDs `[m-a]`; missing cache → the shipped five.
- `TestCodexDefaults_EmptyModelMediumEffort`: `Defaults("codex") == ("", "medium")`; `Efforts` has `ultra`.

- [ ] Write. `go test ./sdk/ -run TestCodex -v`. Report.

### Task 4: `sdk/grok_kimi_test.go` — apikey and delegated providers

**Files:** Create `sdk/grok_kimi_test.go`.

- `TestGrokBegin_IsAPIKeyAndDelegated`: `Kind apikey`, `Delegated true`.
- `TestGrokComplete_EmptyKeyIsDelegatedToken`: code `"  "` → `tok.Delegated`, `Access ""`.
- `TestGrokComplete_KeyMustStartWithXai`: `"sk-1"` → `ErrInvalidRequest`, message contains `console.x.ai`.
- `TestGrokComplete_KeyIdentityIsStable`: `"xai-abc"` twice → same `Identity.UUID` with prefix `key-`; `"xai-def"` differs; `ExpiresAt == 0`; `Access == "xai-abc"`.
- `TestGrokLaunch_RefusesEmptyHome`.
- `TestGrokLaunch_EnvHasKeyAndHome`: `Env` has `XAI_API_KEY=xai-abc` and `GROK_HOME=<home>`; `Drop` has the network list.
- `TestGrokLaunch_NonKeyAccessIsReauth`: non-delegated `Access "nope"` → `ErrReauth`.
- `TestGrokLaunch_DelegatedUnsignedStillLaunches`: `Delegated` account, no auth.json → `Stage` nil error.
- `TestGrokSignedIn_NeedsAuthJSON`: `p, _ := rota.Lookup("grok"); c := p.(rota.SignInChecker)`; delegated account without file → error; with `<home>/auth.json` → nil; non-delegated → nil.
- `TestGrokLoginPlan_DeviceCode`: `rota.LoginPlanFor(delegated grok account, home)` → `ok`, `Bin == "grok"`, `Args` contains `login` and `--device-code`, `Env` has `GROK_HOME=<home>`, `Drop` contains `XAI_API_KEY`.
- `TestLoginPlanFor_FalseForNonDelegatedAndForClaude`.
- `TestGrokAdopt_ReadsDelegatedAuthJSON`: write inventory §3.2 grok document → `Adopt` → `a.UUID == "00000000-0000-4000-8000-000000000001"`, `a.Email == "someone@example.com"`, `a.Token.Refresh` still empty (not rota's to hold).
- `TestGrokResolveModel_FloorPassesUnknownRefusesOtherProviders`: `ResolveModel("grok","grok-9") == ("grok-9", nil)`; `ResolveModel("grok","claude-sonnet-5")` → `ErrInvalidRequest` containing `claude`.
- `TestKimiBegin_IsDelegated`: `Kind delegated`, `Delegated true`.
- `TestKimiComplete_RefusesAnythingPasted`: `"x"` → `ErrInvalidRequest` containing `nothing pasted`; `""` → delegated token.
- `TestKimiLaunch_RefusesEmptyHomeAndNonDelegated`: both error; non-delegated is `ErrReauth`.
- `TestKimiSignedIn_ThreeStates`: none → `ErrReauth` containing `signed in`; `credentials/kimi-code.json` only → `ErrReauth` containing `finish`; plus `config.toml` → nil.
- `TestKimi_HasNoCatalog`: `Models nil`, `Efforts nil`, `Defaults ("","")`, `ResolveEffort("kimi","high")` → `ErrInvalidRequest` containing `effort`.
- `TestKimiLaunch_EnvHasHome`: `KIMI_CODE_HOME=<home>`.
- `TestDelegatedAccount_NeverExpiresRefreshesOrLimits`: delegated account with `ExpiresAt 1` → `Expired false`; `Refresh` → `(false, nil)`; `Status OK`; `rota.Encode(a)` contains `"delegated":true`.

- [ ] Write. `go test ./sdk/ -run 'TestGrok|TestKimi|TestLoginPlanFor|TestDelegated' -v`. Report.

### Task 5: `sdk/token_test.go` — Expired, Refresh, Apply, When, NowMS, ExpiryBuffer

**Files:** Create `sdk/token_test.go`.

- `TestExpired_ZeroExpiryNever` / `TestExpired_DelegatedNever` / `TestExpired_UsesBufferAndNow`: clock at T; `ExpiresAt = T+4min` → expired with the 5 min default; `fake.Restore(t, &rota.ExpiryBuffer, time.Minute)` → not expired; `T+6min` → not expired at default.
- `TestNowMS_FollowsInjectedNow`.
- `TestRefresh_FreshTokenAsksNobody`: `ExpiresAt` far future, Refresher Fn fails the test if called → `(false, nil)`.
- `TestRefresh_UnknownProviderFails`.
- `TestRefresh_NonRefresherIsReauthAndDead` (the pattern above).
- `TestRefresh_NoRefreshTokenIsReauthAndDead`: Refresher present, `Refresh ""`.
- `TestRefresh_DeadTokenBecomesReauth`: Fn returns `rota.ErrDeadToken` → `changed true`, `ErrReauth`, `Dead`.
- `TestRefresh_TransientErrorLeavesAccount`: Fn returns `sentinel` → `changed false`, `errors.Is(err, sentinel)`, `Dead false`, token unchanged.
- `TestRefresh_EmptyAccessIsPlainError`: Fn returns `&rota.Token{}` → `changed false`, err not `ErrReauth`, not `ErrDeadToken`.
- `TestRefresh_SuccessAppliesAndClearsDead`: `a.Dead = true` beforehand, Fn returns `{Access:"A2"}` → `changed`, nil, `Access A2`, `Dead false`.
- `TestApply_KeepsRefreshExpiryScopesWhenAbsent`: apply `{Access:"A2"}` onto `{Access:"A1",Refresh:"R1",ExpiresAt:9,Scopes:[a]}` → `R1`, `9`, `[a]` kept.
- `TestApply_ReplacesWhenPresent`: `{Access:"A2",Refresh:"R2",ExpiresAt:10,Scopes:[b]}` → all replaced.
- `TestApply_FoldsIdentityAndMergesExtraAndSetsDelegated`: identity fields land on `a.UUID/Email/Org`; `Extra` merged with existing keys kept; `Delegated` follows the token both ways.
- `TestApply_ClearsDead`.
- `TestToken_RoundTripsThroughEncode`: `rota.Encode` then `rota.UnmarshalLenient` → deep-equal including `Delegated`, `Identity`, `Extra`.
- `TestWhen_NeverFailsToDecode`: `""`, `"yesterday"`, `null`, `42` inside a `Window` → zero `ResetsAt`, nil error.
- `TestWhen_ParsesRFC3339WithOffsetToUTC`: `"2099-01-02T03:04:05.5+02:00"` → equals `2099-01-02T01:04:05.5Z`.
- `TestWindow_MarshalsMinimalShape`: `Encode(rota.Window{Name:"5h"}) == {"name":"5h","percent":0}`.

- [ ] Write. `go test ./sdk/ -run 'TestExpired|TestNowMS|TestRefresh|TestApply|TestToken|TestWhen|TestWindow' -v`. Report.

### Task 6: `sdk/account_test.go` — Account methods, FindID, MatchIdentity, NewAccount

**Files:** Create `sdk/account_test.go`.

- `TestNewAccount_AppliesTokenAndMarksStaged`: `Staged == "-"`, `Access` set, identity folded.
- `TestFindID_FindsOrNil`.
- `TestMatchIdentity_NilIdentityMatchesNothing` / `_OnlySameProvider` / `_UUIDWinsOverEmail` (same email different uuid → no match; same uuid different email → match) / `_EmailWhenNoUUID` / `_NeitherMatchesNothing`.
- `TestLabel_EmailThenUUIDThenID`: email; 13-char UUID → first 12 + `...`; 12-char UUID unchanged; none → `account-<id>`.
- `TestString_IsProviderSlashLabel`.
- `TestPercent_NilQuotaZero` / `_MaxOfUnscopedWindows` / `_IgnoresScoped` / `_NeverNegative`.
- `TestStatus_DeadIsReauth` / `_SpentWindowIsLimited` (Percent 100, ResetsAt zero) / `_SpentWindowPastResetIsOK` (clock after ResetsAt) / `_SpentWindowBeforeResetIsLimited` / `_ScopedWindowNeverLimits` / `_UnderIsOK`.
- `TestCheckProject_RelativeConfigDirRefused` (message contains `absolute`) / `_RelativeCwdRefused` / `_SameDirRefused` (`/a/b` vs `/a/b/`, message contains `credential`) / `_DifferentAbsoluteOK` / `_EmptyOK`.
- `TestStagedSuperseded_SetsMarker`: `Staged = "abc"` then call → `"-"`.

- [ ] Write. `go test ./sdk/ -run 'TestNewAccount|TestFindID|TestMatchIdentity|TestLabel|TestString|TestPercent|TestStatus|TestCheckProject|TestStagedSuperseded' -v`. Report.

### Task 7: `sdk/quota_catalog_test.go` — Usage, Metered, catalog and flavor functions

**Files:** Create `sdk/quota_catalog_test.go`.

- `TestUsage_UnknownProviderFails` / `_NonMeterIsNilNil` / `_MeterResultVerbatim` (quota pointer and error both pass through; Fn receives `a.Token.Access`) / `_NeverCaches` (two calls, two Fn calls).
- `TestMetered_OnlyClaudeAmongBuiltins` / `_UnknownFalse` / `_FakeMeterTrue`.
- `TestModels_UnknownAndNoCatalogAreNil` / `_BuiltinLists` (claude ids `claude-opus-5` alias `opus`, `claude-fable-5` alias `fable`, `claude-sonnet-5` alias `sonnet`, `claude-haiku-4-5-20251001` alias `haiku`; codex five ids; grok `grok-4.6`, `grok-4.5`; kimi nil) / `_ReturnsCopies` (mutate the returned slice's first ID, call again, unchanged).
- `TestModelsFor_AccountCatalogOnlyWithNonEmptyHome`: `AccountCatalog.Fn` returns `[{ID:"from-"+home}]` for non-empty home, nil for empty → home `""` gives catalog list, home `"h"` gives `from-h`; Fn returning nil for a set home falls back.
- `TestEfforts_Builtins` (claude `low medium high xhigh max`; codex adds `ultra`; grok `low medium high xhigh`; kimi nil) / `TestDefaults_Builtins` (claude `claude-opus-5/high`, codex `""/medium`, grok `grok-4.6/high`, kimi `""/""`).
- `TestResolveModel_NoCatalogPassesAnything` / `_EmptyWantIsDefault` / `_IDCaseInsensitive` / `_AliasToCanonical` / `_UnknownListsAccepted` (message has `it accepts:` and every id) / `_FloorScansOtherProviders` (fake `Floor{Is:true}` over a `Catalog`; want `claude-opus-5` → `ErrInvalidRequest` naming `claude`; want `zzz` passes).
- `TestResolveEffort_NoLevelsWithWantIsError` / `_NoLevelsEmptyOK` / `_DefaultWhenEmpty` / `_CaseInsensitive` / `_UnknownListsAccepted`.
- `TestResolved_ResolvesAgainstHomeAndBlanksEffortForKimi`: fake `Flavored "kimi"` with a `Catalog` having efforts → effort `""`; for `fake.Claude` → alias resolved and default effort filled.
- `TestFlavor_BuiltinsFlavoredAndUnknownEmpty`: four builtins; `Flavored{Name_:"codex"}` on a `t-` name → `codex`; bare `t-` → `""`; unknown → `""`.
- `TestFlavorsOf_UnknownNilKnownCopy`: `FlavorsOf("zzz") == nil`; `FlavorsOf("sandbox")` non-nil, mutating it does not change the next call.
- `TestRestrictedFields_NameSpecJSONTags`: every name is the `json` tag of some `rota.Spec` field (reflect), and the set contains `sandbox` and `permission_mode`.
- `TestPermissionModes_PerFlavorCopies`: claude `[acceptEdits auto bypassPermissions manual dontAsk plan]`; grok `[default acceptEdits auto dontAsk bypassPermissions plan]`; kimi `[plan acceptEdits dontAsk auto bypassPermissions]`; codex nil; copies.
- `TestSandboxes_OnlyCodex`: `[read-only workspace-write danger-full-access]`; others nil. `TestTakesSandbox_CodexAndGrok`.
- `TestNetworkRedirecting_ListAndCopy`: exact 11 names from inventory §1.7; mutation does not persist.

- [ ] Write. `go test ./sdk/ -run 'TestUsage|TestMetered|TestModels|TestEfforts|TestDefaults|TestResolve|TestFlavor|TestRestrictedFields|TestPermissionModes|TestSandboxes|TestTakesSandbox|TestNetworkRedirecting' -v`. Report.

### Task 8: `sdk/staging_test.go` — Stage, StagePlan, Adopt, AdoptFrom, Environ, OwnsCredentials

**Files:** Create `sdk/staging_test.go`. Fakes only (builtins are covered in Tasks 2–4).

- `TestStage_DeadIsReauth` / `_UnknownProviderFails` / `_CreatesHome0700` (`os.Stat(home).Mode().Perm() == 0700`, `Calls()` has `launch`) / `_MkdirFailureSurfaces` (home is a path under an existing regular file → error, `launch` not called) / `_EmptyHomeMakesNoDirectory`.
- `TestStagePlan_DeadIsReauth` / `_PlannerTouchesNoDisk` (Planner Fn returns command + one StagedFile; home dir stays empty; files returned verbatim) / `_NonPlannerLaunchesWithNilFiles` (bare → `launch` called, `files == nil`) / `_PlannerErrorPropagates`.
- `TestAdopt_EmptyHomeIsNilEvenForUnknownProvider` / `_UnknownProviderFails` / `_NonAdopterIsNil` / `_AdopterGetsAccountAndHome` / `_AdopterErrorPropagates`.
- `TestAdoptFrom_UnknownProviderFails` / `_NonFSAdopterIsNil` / `_FSAdopterGetsFS` (`fstest.MapFS`, Fn reads a file from it).
- `TestEnviron_ReplacesDropsAndKeepsOnePerKey`: inherited `[A=1 B=2 C=3 D=4]`, cmd `Env [A=9 X=7] Drop [C]` → result has `B=2 D=4 A=9 X=7`, no `C`, no duplicate `A`. `_NilInheritedIsJustEnv`. `_NilCommandPanics` (recover).
- `TestOwnsCredentials_AdopterOrDelegator`: claude false; codex, grok, kimi true; fake bare false; `Adopter` true; `Delegator` true; unknown false.
- `TestLoginPlanFor_NeedsDelegatedAccountAndDelegator`: bare + delegated account → false; `Delegator` + non-delegated → false; `Delegator` + delegated → the plan.

- [ ] Write. `go test ./sdk/ -run 'TestStage|TestAdopt|TestEnviron|TestOwnsCredentials|TestLoginPlanFor_Needs' -v`. Report.

### Task 9: `sdk/spec_test.go` — Spec.Check, CheckFor, For, and every refusal

**Files:** Create `sdk/spec_test.go`. Use `fake.Claude(fake.New("t-cl"))` for the claude flavor; build codex, grok and kimi flavors as `fake.Catalog{Provider: fake.Flavored{Provider: p, Name_: "<flavor>"}, ...}` (kimi without a catalog, as `fake.Flavored` alone). `chk := func(s rota.Spec, lim *rota.Limits) error { return s.Check("t-cl", lim) }`.

- `TestSpecFor_FillsCwdFromAccountOnlyWhenEmpty`: returns a copy; receiver untouched.
- `TestSpecCheck_WritesNoTempFiles`: count entries in `os.TempDir()` before and after three checks (grok flavor with a prompt, codex flavor with `JSONSchema`) → equal.
- `TestSpecCheck_NegativeTimeoutRefusedFirst`: `TimeoutSeconds -1` and empty prompt → message mentions timeout.
- `TestSpecCheck_BlankPromptRequired`: `"  \n"` → `ErrInvalidRequest` containing `prompt`.
- `TestSpecCheck_ExtraNeedsAllowRawFlags`: `Extra [--foo]` with `&rota.Limits{}` → `ErrInvalidRequest` containing `args`; with `AllowRawFlags` → nil; with nil limits → nil.
- `TestSpecCheck_ReservedFlagsAlwaysRefused`: table over `-p --print --single --prompt-file --prompt-json --output-format --input-format --json --color -o --output-last-message --bare --betas -C --cd --cloud --bg --background` and `--output-format=text`, each with `AllowRawFlags: true` → `ErrInvalidRequest` containing `rota sets it`.
- `TestSpecCheck_FieldMustBelongToFlavor`: for every name in `rota.RestrictedFields()`: set that Spec field by json tag via reflection to a plausible non-zero value (string `"x"`, bool `true`, int `1`, `[]string{"x"}`, `json.RawMessage("{}")`, `map[string]string{"k":"v"}`, `[]json.RawMessage{...}`), add `Prompt "hi"`, then for each of the four flavors: if the flavor is in `rota.FlavorsOf(name)` the error, if any, must NOT mention `does not understand`; otherwise the error must be `ErrInvalidRequest` mentioning `does not understand` and the field name.
- `TestSpecCheck_EmptySliceCountsAsSet`: codex flavor with `Tools: []string{}` → refused as a claude field; `Tools: nil` → not that error.
- `TestSpecCheck_CwdMustExistAndBeADirectory`: nonexistent → `ErrInvalidRequest` containing `existing directory`; a file path → same.
- `TestSpecCheck_RootsConfineDirectories`: `Roots [/tmp/root]` (use `t.TempDir()` as root); cwd inside → ok; cwd a sibling whose name has the root as prefix (`root2`) → `ErrOutsideRoots`; empty Roots → ok anywhere.
- `TestSpecCheck_RootsResolveSymlinks`: symlink inside root pointing outside → `ErrOutsideRoots`.
- `TestSpecCheck_FilesCheckedAgainstRoots`: `Images`, `Settings` as a JSON string path, `MCPConfig` string path, and grok `Debug "/outside/x"` each outside → `ErrOutsideRoots`; grok `Debug "true"` not checked.
- `TestSpecCheck_SettingsDenylistDefault`: each of `env apiKeyHelper awsAuthRefresh awsCredentialExport hooks permissions otelHeadersHelper statusLine forceLoginMethod` inline with `&rota.Limits{}` → `ErrInvalidRequest`; `{"theme":"dark"}` → ok.
- `TestSpecCheck_SettingsDenylistReplaced`: `SettingsDenyKeys [theme]` → `{"env":{}}` ok, `{"theme":1}` refused.
- `TestSpecCheck_SettingsFileIsVetted` (file with `hooks` → refused) / `_SettingsFileOver1MBRefused` / `_SettingsFileUnreadableRefused` (directory path).
- `TestSpecCheck_MCPInlineRefused` (`ErrInvalidRequest` containing `path`) / `_MCPFileWithCommandRefused` (`{"mcpServers":{"x":{"command":"rm"}}}`) / `_MCPFileWithURLOnlyOK`.
- `TestSpecCheck_SettingSourcesNeedRawFlags` / `_PluginURLsRefused` / `_CodexConfigRefused` / `_NilLimitsSkipsSuppliedConfigChecks` (inline `env` settings with nil limits → ok).
- `TestSpecCheck_DangerousGates`: table from inventory §1.8: each (flavor, spec) → `ErrDangerous` with `&rota.Limits{}`, nil with `AllowDangerous: true`.
- `TestSpecCheck_EnumRefusals`: claude/grok/kimi `PermissionMode "weird"` and codex `Sandbox "weird"` → `ErrInvalidRequest` containing `is not one of`; grok `Sandbox "weird"` accepted.
- `TestSpecCheck_UnknownFlavorIsUnsupported`: bare fake, no Flavored → `ErrUnsupported`.
- `TestSpecCheck_ModelAndEffortResolved`: `Model "nope"` → `ErrInvalidRequest` listing accepted; `Model "one"` ok; `Effort "medium"` (not in `low high`) → refused.
- `TestSpecCheckFor_UsesAccountHomeCatalog`: `AccountCatalog` over `fake.Claude` returning `[{ID:"only-"+home}]` → `CheckFor(a, "h", nil)` with `Model "only-h"` ok, with `Model "t-model-1"` refused.

- [ ] Write. `go test ./sdk/ -run TestSpec -v`. Report.

### Task 10: `sdk/run_test.go` — Run against fake CLIs

**Files:** Create `sdk/run_test.go`. Base pattern from the top of this plan. Scripts print with `printf`; remember `$(cat)` consumes stdin. Keep every script fast; timeouts use `sleep 5` with a 1 s limit.

- `TestRun_NilCommandNeedsBaseEnv`: `p.BaseEnv = nil` → `ErrInvalidRequest` containing `BaseEnv`; script not run.
- `TestRun_SuppliedCommandRunsWithOnlyItsEnv`: script `cat >/dev/null; printf '{"type":"result","result":"%s"}\n' "LEAK=$LEAK T=$T_TOKEN"`; `t.Setenv("LEAK","1")`; `cmd := &rota.Command{Bin:"t-cli", Env:[T_TOKEN=x], BaseEnv: fake.BaseEnv(dir)}` → `Result == "LEAK= T=x"`.
- `TestRun_SignInCheckerConsulted`: `fake.SignIn` Fn returns `sentinel` → `errors.Is(err, sentinel)`; nil → runs.
- `TestRun_MissingBinaryIsUnsupported`: `Command` Fn returns `Bin "t-nope"` → `ErrUnsupported` containing `not found in PATH`.
- `TestRun_PromptOnStdinArgvVisibleFieldsFilled`: `Spec{Prompt:"hi", Model:"one", Effort:"high"}` → `Result` starts `STDIN=hi ARGS=` and contains `--model t-model-1`; `res.Model == "t-model-1"`, `res.Effort == "high"`, `Account 7`, `Provider "t-run"`, `SessionID "s-fake"`, `NumTurns 1`, `CostUSD 0.5`, `Subtype "success"`, `!IsError`, `ExitCode 0`, `Stderr "fake-stderr"`, `DurationMS >= 0`, `Events == nil`, `!Truncated`.
- `TestRun_ResumeLastBecomesContinue`: `Resume "last"` → ARGS has `--continue`, no `--resume`.
- `TestRun_NonZeroExitIsAResultNotAnError`: `fake.ClaudeResult(3)` → `err == nil`, `IsError`, `ExitCode 3`, `Result` still the event text; a script printing nothing and exiting 2 → `Result == "fake-stderr"` (fallback to stderr).
- `TestRun_TimeoutKillsAndReturnsResult`: script `sleep 5`; `TimeoutSeconds 1` → `errors.Is(err, context.DeadlineExceeded)`, `res != nil`, elapsed under 3 s.
- `TestRun_ContextCancelKills`: cancel after 200 ms → `context.Canceled`, elapsed under 3 s.
- `TestRun_CwdIsSymlinkResolved`: script prints `pwd -P` into `result`; `Cwd` is a symlink to a temp dir → `Result` equals the resolved dir.
- `TestRun_HermeticSetsTempConfigDirAndRemovesIt`: script prints `$CLAUDE_CONFIG_DIR`; `Hermetic true`, `ScratchDir t.TempDir()` → `Result` is a path under ScratchDir that no longer exists after Run.
- `TestRun_BufferedArrayOutputIsParsed`: script prints `[{"type":"system"},{"type":"result","result":"ANSWER","session_id":"s1","total_cost_usd":0.5}]` → `Result ANSWER`, `SessionID s1`; `Events nil` without `IncludeEvents`, two events with it.
- `TestRun_StreamingEventsIncludedOnlyWhenAsked`: three JSON lines; `Stream true` alone → `Events nil`; `Stream + IncludeEvents` → `len(Events) == 3`.
- `TestRun_SpacedJSONStillParsed`: `{"type" : "result","result":"ANSWER"}` → `ANSWER`.
- `TestRun_MaxEventsTruncates`: three events, `Limits{MaxEvents:2}`, `Stream+IncludeEvents` → `len(Events) <= 2`, `Truncated`, `err == nil`.
- `TestRun_MaxEventLineTruncates`: a line of 100 000 `x` characters then a result line; `MaxEventLine 1024`, `Stream` → `Truncated`, `err == nil`.
- `TestRun_MaxStderrKeepsTheTail`: script writes 10 000 bytes of `a` then `TAIL` to stderr; `MaxStderr 512` → `Stderr` has prefix `[` and contains `dropped`, ends with `TAIL`, `Truncated`.
- `TestRun_MaxBufferedOutputTruncates`: non-streaming script prints 1 MiB of `y` then a result line; `MaxBufferedOutput 4096` → `Truncated`.
- `TestRun_DefaultLimitsLeaveSmallOutputAlone`: `lim nil` → `!Truncated`.
- `TestRun_ScratchFilesAreRemoved`: grok flavor fake (`Flavored "grok"` + `Catalog` with `grok-4.6`); `ScratchDir := t.TempDir()`; script `cat >/dev/null; printf '{"text":"ok","stopReason":"end_turn","sessionId":"s"}\n'` → after Run `os.ReadDir(ScratchDir)` is empty.
- `TestRun_CodexEventStream`: codex flavor fake; lines `thread.started t-1`, `item.completed agent_message CODEX`, `turn.completed usage {"input_tokens":5}` → `SessionID t-1`, `Result CODEX`, `Usage` contains `input_tokens`; with a `turn.failed` line → `IsError`.
- `TestRun_GrokBufferedShape`: `{"text":"SHAPE","stopReason":"error","sessionId":"01a0","usage":{},"num_turns":1,"total_cost_usd":0.03}` → `Result SHAPE`, `IsError`, `SessionID 01a0`, `NumTurns 1`.
- `TestRun_KimiProseBecomesResult`: kimi flavor (`fake.Flavored` alone); script prints `just words` and `and more` → `Result == "just words\nand more"`.
- `TestRun_StructuredOutputCaptured`: result event with `"structured_output":{"k":1}` → `string(res.Structured)` contains `"k":1`.

- [ ] Write. `go test ./sdk/ -run TestRun -v`. Report.

### Task 11: `sdk/registry_json_errors_test.go` — Registry, JSON helpers, error constructors

**Files:** Create `sdk/registry_json_errors_test.go`.

- `TestNewRegistry_EmptyNoDefault`: `Lookup("")` fails; after `r.Default = "t-a"` and `Register`, ok. `TestRegistryRegister_ReplacesByNameOnly`: register `fake.Meter{...}` as `t-m`, then a bare `t-m` → `rota.Metered("t-m") == false`. `TestRegistryProviders_Sorted`. `TestLookup_EmptyUsesDefaultProviderNotRegistryDefault`: `fake.Registry(t)`; `fake.Restore(t, &rota.DefaultProvider, "t-x")`; registry `Default` left as is → `rota.Lookup("")` returns `t-x`. `TestLookup_UnknownListsKnown`. `TestProviders_BuiltinsPresent`: `claude codex grok kimi` all in `rota.Providers()`.
- `TestEncode_NilSliceIsNull` (`Encode(struct{A []int}{})` contains `"A":null`) / `TestEncodeIndent_TwoSpaces` / `TestEncodeTo_AppendsNewline`.
- `TestUnmarshalLenient_CaseInsensitiveDuplicatesAndBadUTF8`: `{"Access_Token":"a","access_token":"b","bad":"\xff"}` into a struct with `json:"access_token"` → `"b"`, no error. `TestDecodeLenient_Reader`. `TestLenientOptions_DecodesWithJSONv2`: `jsonv2.Unmarshal(data, &v, rota.LenientOptions())` works case-insensitively.
- `TestAccount_WireShapeIsExact` and `TestResult_WireShapeIsExact`: the two byte-exact strings from inventory §1.10.
- `TestSentinels_AreDistinct`: pairwise `errors.Is` false across all ten. `TestInvalid_IsInvalidRequestWithoutPrefix`. `TestWrapNoAccount_` (`ErrNoAccount`, contains `7`) / `TestWrapNoLogin_` (`ErrNoLogin`, contains the id) / `TestWrapReauth_` (`ErrReauth`, contains `log in again` and the account string) / `TestWrapNoBinary_` (`ErrUnsupported`, contains `not found in PATH`).
- `TestHTTPError_MessageTrimsAndTruncates`: `Body` 400 chars → `Error()` length `len("http 500: ")+300`. `TestOAuthError_KeepsCode`: `Error()` non-empty, `Code` preserved (construct via a fake server in Task 2 style: 400 `access_denied` on a claude code grant).

- [ ] Write. `go test ./sdk/ -run 'TestNewRegistry|TestRegistry|TestLookup|TestProviders|TestEncode|TestUnmarshalLenient|TestDecodeLenient|TestLenientOptions|TestAccount_Wire|TestResult_Wire|TestSentinels|TestInvalid|TestWrap|TestHTTPError|TestOAuthError' -v`. Report.

### Task 12: `rotation/rotation_test.go` — package rotation

**Files:** Create `rotation/rotation_test.go` (package `rotation_test`) and `rotation/mem_test.go` with a `memBackend` (see Task 13; copy it, do not import across test packages).

- `TestCutoff_RangeAndDefault` (0, -1, 101 → 100; 70 → 70). `TestSpent_UsesPercentAgainstCutoff` (no quota never spent). `TestInQueue_OrderAtLeastOne` (0 and -1 out). `TestAvailable_InQueueNotDeadNotSpent`.
- `TestSort_OrderedFirstThenOrderThenID` / `TestQueue_FiltersAndReturnsNewSlice` (mutating the result does not change the input).
- `TestPick_EmptyQueueMessage` (`ErrNone`, contains `give one an order`) / `TestPick_ExhaustedMessage` (contains `spent or needs re-auth`) / `TestPick_FirstAvailable`.
- `TestNext_OnePastHighest` (empty → 1).
- `TestChoose_ByIDAndNoAccount` (`ErrNoAccount`). `TestChoose_RotationRefreshesMeteredThenPicks`: store from `memBackend` blob with two `t-m` accounts (`fake.Meter` counting calls, registered after `fake.Registry(t)`), first at 100% from the meter → picks the second; meter called once per account. `TestChoose_SkipsBusyAccounts`: a `fake.Adopter` provider (owns credentials) with `st.Hold(a)` held → `Choose` picks another; when all busy → `ErrNone` containing `already running`.
- `TestBackfill_NumbersByIDOnce` / `_LeavesOrderedStore` / `_SkipsEmptyStore`.
- `TestParsePlace_AllForms` (table incl. ` First `, `before:3`, `after:7`, `0`, `out`) / `TestParsePlace_Refusals` (`""`, `-1`, `x`, `1.5`, `before`, `before:`, `before:x`, `after:0`, `between:2` → `ErrInvalidRequest`).
- `TestMove_NumberShiftsLaterAccounts` / `_PastEndIsLast` / `_OutClosesGap` / `_FirstAndLast` / `_UpDownTradeWithNeighbour` / `_UpAtTopChangesNothing` / `_UpDownNeedAPlace` (`ErrInvalidRequest`, nothing changed) / `_BeforeAfterAnother` / `_RelativeToSelfOrOutsideIsError` / `_RepairsTiesAndGaps` / `_SamePlaceShiftsNothing`. Assert `Moved.Was`, `Now`, `Shifted` ids.

- [ ] Write. `go vet ./rotation/ && go test ./rotation/ -v`. Report.

### Task 13: `store/store_test.go` — package store

**Files:** Create `store/mem_test.go` (a `memBackend`: `sync.Mutex` lock, `blob []byte`, `home string`, counters `loads/saves/locks`, `failOn string` in `load|save|lock`; `HomeRoot()` returns `home`) and `store/store_test.go` (package `store_test`). Seed stores from raw JSON blobs. Register fakes after `fake.Registry(t)`; a provider that owns credentials is `fake.Adopter{Provider: p, Fn: ...}`.

- `TestNewStore_LockErrorReturnsNoStore` / `_LoadErrorCloses` (`saves == 0`, a later `Save` on nothing) / `_CorruptBytes` (message contains `corrupt`) / `_EmptyProviderDefaults` (blob account without provider → `rota.DefaultProvider`) / `_NextIDRaisedPastMaxID`.
- `TestOpen_FirstRunIsEmpty`: `store.Open(t.TempDir())` → no accounts, `NextID == 0`, and no `accounts.json` written yet. `TestDefaultDir_HonoursROTAHOME`: `t.Setenv("ROTA_HOME", dir)` → `DefaultDir() == dir`.
- `TestFileBackend_SaveIsAtomic0600`: after `Save`, `accounts.json` mode `0600`, no `*.tmp` left. `TestFileBackend_LoadSweepsOldTemp`: create `accounts.json-old.tmp` with mtime 2 h ago → gone after `Load`; a fresh one stays.
- `TestStoreSave_AfterReleaseRefuses` (message contains `reopen`) / `TestStoreClose_Idempotent`.
- `TestStoreHome_ConfigDirOrRootNoCreate`.
- `TestStoreRemove_UnknownIsNoAccount` / `_BusyIsErrBusy` (owning provider, `Hold` taken) / `_DeletesHomeAndRetiresID` (home dir gone; `NextID == id+1`; after `Save` and reopen the id is not reused by `FinishLogin`).
- `TestStoreBeginLogin_UnknownProviderParksNothing` (no `pending.json`) / `_ParksPendingJSON0600` / `_SecondStoreOnSameHomeCanFinish` (two `NewStore` over two `memBackend`s sharing `home`; second calls `FinishLogin(l.ID, "c1")` → account added).
- `TestStoreFinishLogin_UnknownIDIsNoLogin` / `_RejectedCodeKeepsPending` (`"bad"` then `"c1"` succeeds) / `_AuthPendingKeepsPending` / `_MatchUpdatesInPlace` (same identity → same pointer, `added false`, token replaced) / `_NoMatchAddsFreshIDAndWipesStaleHome` (pre-create `<home>/<provider>-<id>/junk`; after add it is gone) / `_ResetsQuotaAndStaged` (`Quota nil`, `QuotaAt 0`, `Staged "-"`) / `_ExpiredPendingIsNoLogin` (write `pending.json` by hand with `createdAt` 16 min ago; use the shape a real `BeginLogin` writes, copy it from disk first).
- `TestStoreRefresh_SkipsDeadUnmeteredFreshAndBusy` (counting `fake.Meter`) / `_ForceIgnoresTTL` / `_PanicInProviderBecomesError` / `_SavesOnceWhenChanged` (`saves == 1`) / `_CollectsErrorsNeverFatal`.
- `TestStoreMaintain_AdoptsThenRefreshesAndSkipsBusy`: `Adopter`+`Refresher` fake recording `adopt`/`refresh` order; account with `ExpiresAt 1`.
- `TestStoreRun_DeadIsReauth` / `_BusyIsErrBusy` / `_AdoptsBeforeRefresh` (order slice) / `_SavesBeforeRun` (`saves` incremented before the CLI runs: the CLI script reads the blob path? simpler: `saves >= 1` when the fake CLI starts, checked by a `memBackend.onSave` hook that records the time and a script that sleeps 100 ms) / `_StageErrorStillSaves` / `_ReleasesLockSoSaveRefuses` / `_ChildEnvIsHostEnvWithoutHiddenNames` (`t.Setenv("ROTA_HOME", ...)` and `store.HideFromAgents("T_SECRET")`, `t.Setenv("T_SECRET","1")`; script prints both → empty).
- `TestStorePrepare_ReturnsBinaryEnvAndClaim` / `_LookPathFailureReleasesClaim` (`Busy` false afterwards).
- `TestStoreHoldBusy_NoOpForNonOwners` / `_ClaimForOwners` (second `Hold` `ok == false`, `Busy` true, released → false) / `_MkdirFailureDegradesToOK`.
- `TestHideFromAgents_DedupesAndHostEnvRecomputes`.
- `TestStore_IDsAreNeverReused`: login → id 1; remove; login → id 2.

- [ ] Write. `go vet ./store/ && go test ./store/ -v`. Report.

### Task 14: `cmd/matrix` and `cmd/login`

**Files:** Create `cmd/matrix/main.go`, `cmd/login/main.go`.

`cmd/matrix`: runs `go test -list '.*'` once per package under `./sdk/...`, `./rotation/`, `./store/` and `./live/` (discover the packages with `go list ./sdk/... ./rotation/ ./store/ ./live/`; parse lines starting with `Test`), groups by the `<Symbol>` before the first `_`, and prints Markdown: a heading `# Coverage matrix`, then one section per package with a table `| Symbol | Conditions | Tests |` where Conditions is the count and Tests the names joined by `, `. Skipped-by-finding tests are not distinguishable from names alone; that is fine.

`cmd/login` (usage `login begin | finish <id> <code> | verify`): `ROTA_HOME` must be set and point at an existing directory, else exit 2 with a message. `begin`: `store.Open(""); s.BeginLogin(ctx, "claude")`; prints `id: <ID>` and `url: <URL>`. `finish`: `s.FinishLogin(ctx, id, code)`; prints `account: #<id> <label> added=<bool>`. `verify`: opens the store, for the single account: `rotation.Cutoff`, `s.Refresh(ctx, true)` and prints the quota windows, then `s.Run(ctx, a, rota.Spec{Prompt: "Reply with exactly the word OK.", TimeoutSeconds: 120}, nil, os.Stdout)` (note: `Run` releases the lock) and prints `result: <Result>` and `exit: <ExitCode>`; then reopens the store, `s.Remove(a.ID)`, `s.Save()`, prints `removed`. All errors go to stderr with exit 1.

- [ ] Write both. `go vet ./cmd/... && go run ./cmd/matrix | head`. Report.

### Task 15: `live/live_test.go` — real accounts (`ROTA_LIVE=1`)

**Files:** Create `live/live_test.go` (package `live_test`). `if os.Getenv("ROTA_LIVE") == "" { t.Skip("set ROTA_LIVE=1 to run against ~/.rota") }` at the top of every test. Open with `store.Open("")`. Never copy a token out of the store; never call `rota.Refresh` directly on these accounts; go through `s.Refresh`, `s.Run`, `rotation.Choose`.

- `TestLiveStore_OpensAndListsAccounts`: at least one account; each `wire.Describe(a)` has a non-empty `Label` or email.
- `TestLiveRefresh_ClaudeQuotaHasWindows`: `s.Refresh(ctx, true)` → every claude account has `Quota` with a `5h` window and `QuotaAt` within the last minute; errors slice printed with `t.Logf`, failing only if a claude account got no quota.
- `TestLivePick_AgreesWithChoose`: `rotation.Pick(s.Accounts)` and `rotation.Choose(ctx, s, 0)` name the same account when nothing is busy.
- `TestLiveRun_EachAccountAnswersASmallPrompt`: for each of ids 1, 2, 3, 8 (skip any missing with `t.Logf`): fresh `store.Open("")`, `s.Run(ctx, a, rota.Spec{Prompt: "Reply with exactly the word OK and nothing else.", TimeoutSeconds: 180}, nil, io.Discard)` → `err nil`, `!IsError`, `ExitCode 0`, `Result` contains `OK`, `SessionID` non-empty for claude and codex; log `Model`, `CostUSD`, `DurationMS`. Run sequentially.
- `TestLiveSessions_ScanListsSomething`: `sessions.Scan(s, 24*time.Hour)` returns without error and lists at least one instance or session after the runs above (skip if none, with a log).

- [ ] Write. `go vet ./live/ && go test ./live/` (skips without the env). Report.

## Self-review

Spec coverage: pin (Task 0 scaffold, done), offline layers 1–3 (Tasks 1–11), rotation and store (12–13), live (15), real login tool (14), matrix (14), naming rule (Global Constraints). Placeholders: none; every test names its assertion. Type consistency: helpers named as in `internal/fake`; test symbols as in the inventory.
