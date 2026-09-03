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

## Found by the first Windows run, fixed in v1.0.4

9. **Upload paths with forward slashes were refused on Windows.**
   `wire.StageUploads` compared the caller's `notes/a.txt` with
   `filepath.Clean`'s `notes\a.txt` and refused it. Paths are now judged in
   the platform's own form, and a rooted path with no drive (`\abs`), which
   Windows does not call absolute, is refused too.
10. **Process discovery shelled out to `ps`.** On Windows nothing was
    listed. The process table is now read through PowerShell, with the
    working directory reported as unknown rather than guessed.

The rest of that run was the test harness: every fake vendor CLI was a
shell script. They are now the test binary itself, playing a small spec
(`internal/fakecli`), so the same suite runs on every platform and CI
tests Windows in full.

## Security review of the HTTP server, fixed in v1.0.6

Two read-only reviews of `rota serve`, one on auth and transport, one on
the path from an authenticated request to the child CLI. Twenty-three
findings, five high, all fixed with tests except three that are documented.

High:

11. The brute-force block was checked before the token and keyed on the
    peer address, so ten bad tokens from anyone sharing that address (a
    proxy, or a web page on the loopback) locked the operator out for an
    hour. The right token is now always admitted; only guesses are blocked.
12. A run waiting for a concurrency slot held the store's exclusive lock,
    so one extra concurrent run stalled every other request and every
    local `rota` command until a slot freed. The slot is taken before the
    store, with a cancellable wait; the forced-refresh listing likewise.
13. `PATCH` accepted any absolute `config_dir` and `DELETE` removed it, so a
    caller could delete the operator's home, the store, or another
    account's credentials. A config directory may no longer be rota's own
    (another home, the store, an ancestor of it), must sit inside a root
    when roots are set, and removal only deletes homes rota created.
14. Relative `add_dirs`, `plugin_dirs`, `images`, `settings`, `mcp_config`
    and grok's debug file were checked against the server's cwd but handed
    to the CLI raw, which resolved them against the run's cwd: `../..`
    from a deep cwd escaped the roots. Every checked path now reaches the
    CLI in its resolved absolute form.
15. Documented, not gated: roots confine what a request names, not what
    an agent with a shell reads. Run the server as a user that can read
    only the roots and the store.

Medium: the limiter dropped its whole table when flooded (now evicts the
addresses with the fewest failures); six error paths returned raw internal
errors (now "internal error", text in the log); JSON uploads had no caps
(now the same 32 files and 16 MB each as multipart, enforced in
`StageUploads`); a settings or MCP file could be rewritten between the vet
and the CLI reading it (the vetted document is now passed inline); the
settings deny list missed keys that load plugins and project MCP servers;
`--token` on the command line is visible in the process table (warned at
startup, `ROTA_TOKEN` recommended); config files were read whole before the
size check and blocked on a FIFO (stat first, regular files only); no
`WaitDelay`, so a helper holding the pipes pinned a handler and its slot
forever (5 seconds now).

Low: token length leaked through comparison timing (hashed first); the
playground could be framed (`frame-ancestors 'none'`, `X-Frame-Options`);
history kept upload bytes in localStorage (names only now); broken login
bodies were read as empty (refused); a multipart run read its request from
the query string (part only); JSON replies lacked `Cache-Control: no-store`;
hermetic mode was defeated by a duplicate `CLAUDE_CONFIG_DIR` (dropped);
`worktree` accepted paths (names only); resume transcripts were copied
before validation (after it now).

## Inventory corrections (SDK is right)

- `Complete` on codex takes `ExpiresAt` from `expires_in` when the reply
  carries one; the JWT `exp` claim is the fallback used on adoption, where
  the CLI records no expiry (`lib/oauth.go:99-105`).
- `HTTPError.Body` keeps the raw reply body; trimming and the 300-character
  cut happen only in `Error()`, which appends `...` after the cut
  (`lib/httpx.go:61-82`).
- `wire.Account` has no `Label` field; `sessions.Scan(st, recent int)`
  takes a count and returns no error.
