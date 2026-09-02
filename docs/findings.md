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

## Found by fuzzing the output parser, fixed in v1.0.2

Fuzz targets in rota's own `lib/fuzz_test.go` (streaming and buffered
`readOutput`, `absorb`, `tailBuffer`) turned up six ways a vendor CLI's
output could make the reply unencodable or the parser slow. Each made
`Encode(Result)` fail, which the server swallowed and the command line
reported as an error, losing the run's answer.

3. A line starting with `{` or `[` that was not valid JSON (a half line,
   a NUL byte, invalid UTF-8, a repeated name) was kept verbatim in
   `Events`. Now it is kept as a JSON string of the text.
4. `usage` and `structured_output` were read leniently and written
   strictly. Now an unencodable value is dropped; the outcome fields stay.
5. Prose output with invalid UTF-8 became an invalid `Result` string.
6. The stderr tail was cut on a byte, not a rune, so any non-ASCII stderr
   over the cap broke the reply.
7. A nested array inside an event array was decoded again at every level:
   a 10000-deep line with a 1 MB payload took 44 seconds. Only objects are
   absorbed now.
8. A valid document near the decoder's 10000-level depth limit passed
   validation but overflowed once nested inside the reply. Eight levels of
   headroom are kept.

Also in v1.0.2: `rota set -h` prints usage without an account id; the kimi
contract test allows the permission mode it asks for; CI on three
platforms with a release workflow gated on this suite.

## Inventory corrections (SDK is right)

- `Complete` on codex takes `ExpiresAt` from `expires_in` when the reply
  carries one; the JWT `exp` claim is the fallback used on adoption, where
  the CLI records no expiry (`lib/oauth.go:99-105`).
- `HTTPError.Body` keeps the raw reply body; trimming and the 300-character
  cut happen only in `Error()`, which appends `...` after the cut
  (`lib/httpx.go:61-82`).
- `wire.Account` has no `Label` field; `sessions.Scan(st, recent int)`
  takes a count and returns no error.
