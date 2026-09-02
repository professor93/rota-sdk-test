# rota-sdk-test

Black-box tests of `github.com/professor93/rota` from a separate module, the
way any application would import it. Proves two things: the published module
works as pinned, and every exported method behaves as documented under every
condition its contract names.

## Pin

`go.mod` requires `github.com/professor93/rota v1.0.0` from the public proxy.
No `replace`. `go.work.local` (opt-in via `GOWORK`) points at `../cswapgo`
for fixing a bug the suite finds; the fix ships as a new rota version, never
a reused one, and the pin moves.

## Layout

    internal/fake/   fake providers, httptest OAuth/usage server, fake CLIs
    sdk/             lib, one file per theme
    rotation/        rotation over lib values
    store/           store with FileBackend and an in-memory Backend
    live/            ROTA_LIVE=1: the real ~/.rota store
    cmd/login        ROTA_LOGIN=1: real claude login into a temporary store
    cmd/matrix       docs/matrix.md from test names
    docs/            this spec, the matrix

## Offline

Three layers, all without network:

1. Real `claude` and `codex` code paths with `rota.HTTPClient`,
   `rota.ClaudeEndpoints` and `rota.CodexEndpoints` aimed at an httptest
   server: Begin/Complete with PKCE, refresh that rotates, dead lineage, 401
   / 429 / 5xx, malformed bodies, `HTTPError` and `OAuthError`, quota
   windows and `Percent`.
2. `grok` and `kimi` through apikey and delegated login kinds, `LoginPlan`,
   and staging: `Stage`, `StagePlan`, `Adopt`, `AdoptFrom`.
3. Two registered fakes: one implementing every optional interface, one
   implementing none, so both sides of every interface check run. Fake CLI
   scripts per flavor drive `Run`: success, error result, non-zero exit,
   context timeout, every `Limits` bound with `Result.Truncated`, the events
   writer, and `Spec.Check` refusals.

Package-level injection points (`HTTPClient`, `Now`, `ExpiryBuffer`,
`DefaultRegistry`, endpoints) are shared state: tests that set them run
serially and restore on cleanup.

## Live (`ROTA_LIVE=1`)

Opens the real store with `store.Open("")` and works only through it, so
token rotation stays owned by the store. Refreshes quota, checks `Pick` and
`Choose`, runs one tiny prompt on claude #1, claude #2, codex #3 and grok #8,
inspects `Result`, sessions and `wire.Describe`. Sequential; the store lock
serialises anyway.

## Real login (`ROTA_LOGIN=1`)

Temporary `ROTA_HOME`. `cmd/login begin` parks a claude login and prints the
URL; the code is pasted into `cmd/login finish <id> <code>` in a second
process, which is the parked-login handoff for real. Then identity match,
usage, one tiny run, remove, delete.

## Naming and matrix

Every test is `Test<Symbol>_<Condition>`; `make matrix` groups them by symbol
into `docs/matrix.md`, so "every method, every condition" is checkable
against the API listing rather than claimed.

## Findings

Bugs found go into rota with a test of their own and ship as v1.0.1 and up.
