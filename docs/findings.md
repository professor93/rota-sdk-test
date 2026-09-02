# Findings

What the suite turned up in `github.com/professor93/rota v1.0.0`, in the
order found. A finding is behaviour that contradicts the SDK's documented
contract; an inventory correction is where the contract was described
wrongly and the SDK is right.

Both findings are fixed in v1.0.1, each with a test in rota and one here:
`TestModels_CopiesAliasesToo` and `TestSpecCheck_RootsRefuseBeforeAnyFileIsRead`.
The module is pinned to v1.0.1.

## Findings

1. **`Models` returns a shallow copy.** `rota.Models("claude")` copies the
   slice of `Model` values but each `Aliases` slice still shares its backing
   array with the SDK's own table (`lib/claude.go:210`, `lib/codex.go:230`,
   `lib/grok.go:129`). `rota.Models("claude")[0].Aliases[0] = "x"` changes
   what the next caller sees. The doc says catalog values come back as
   copies. Severity: low. Fix: copy `Aliases` per model.

2. **Config files are read before roots are checked.** `planFor` runs
   `checkSuppliedConfig` (`lib/run.go:338`) before `checkPaths`
   (`lib/run.go:349`), and `checkSuppliedConfig` opens a caller-named
   `settings` or `mcp_config` file through `readConfigFile`
   (`lib/run.go:577`, `lib/run.go:590`). A mediated caller with
   `Limits.Roots` set therefore gets a verdict about a file outside the
   roots before the roots refuse it: a missing file yields "no such file", a
   settings file carrying `env` yields "settings may not carry env", and
   only a clean existing file yields `ErrOutsideRoots`. That is an existence
   and key-shape oracle on files the caller is not allowed to name.
   Severity: medium for a confined server. Fix: check paths against the
   roots before any file is opened. Pinned by
   `TestSpecCheck_FilesCheckedAgainstRoots`, which uses clean existing files
   so the roots verdict is what it sees.

## Inventory corrections (SDK is right)

- `Complete` on codex takes `ExpiresAt` from `expires_in` when the reply
  carries one; the JWT `exp` claim is the fallback used on adoption, where
  the CLI records no expiry (`lib/oauth.go:99-105`).
- `HTTPError.Body` keeps the raw reply body; trimming and the 300-character
  cut happen only in `Error()`, which appends `...` after the cut
  (`lib/httpx.go:61-82`).
- `wire.Account` has no `Label` field; `sessions.Scan(st, recent int)`
  takes a count and returns no error.
