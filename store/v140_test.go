// These pin what rota 1.4.0's betas changed about the store as an
// application sees it: an account on a Console API key runs on its key and
// nothing else (beta.1), an organisation's cached policy and remote
// settings are each account home's own (beta.2), and a launch that panics
// lets go of the account it claimed (beta.2).
//
// Every account and setting is written as the store's JSON holds it, so
// nothing here names anything the pinned module lacks, and each test skips
// on a module older than the beta that brought what it pins.

package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// envClaude is a claude CLI that answers with what it was given to sign in
// with and where it was pointed. An unset variable reads "unset".
const envClaude = `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"KEY=%s OAUTH=%s AUTH=%s CFG=%s","num_turns":1}\n' "${ANTHROPIC_API_KEY-unset}" "${CLAUDE_CODE_OAUTH_TOKEN-unset}" "${ANTHROPIC_AUTH_TOKEN-unset}" "$CLAUDE_CONFIG_DIR"
`

// An account on a Console API key runs like any claude account, in a home
// of its own mirrored from the person's directory, on ANTHROPIC_API_KEY
// alone: whatever else could sign Claude Code in is dropped, even a
// long-lived token the account somehow holds, and no login is written into
// its home.
func TestStoreRun_AKeyAccountRunsOnItsKeyAlone(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	claudeWorld(t)
	fake.CLI(t, "claude", envClaude)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "inherited")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited")
	fake.Registry(t)
	const key = "sk-ant-api03-SUITEONLY0123456789abcdefSUITEONLY-Ab12"
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","credential":"apikey","uuid":"apikey-1",
 "token":{"accessToken":"`+key+`"},"long":{"accessToken":"long-tok","expiresAt":4102444800000}}]}`)

	res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(b.home, "claude-1")
	if want := "KEY=" + key + " OAUTH=unset AUTH=unset CFG=" + own; res.Result != want {
		t.Fatalf("the CLI saw %q, want %q", res.Result, want)
	}
	if _, err := os.Lstat(filepath.Join(own, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("no login is written into a key account's home: %v", err)
	}
}

// policy is every name Claude Code caches an organisation's policy and
// remote settings under: the files themselves and what it keeps beside
// them. nearPolicy only look like them, and are shared like anything else.
var (
	policy     = []string{"policy-limits.json", "policy-limits.json.signature.json", "remote-settings.json", "remote-settings.json.synced-at"}
	nearPolicy = []string{"policy-limits.jsonx", "remote-settings-other.json"}
)

// One organisation's cached policy or remote settings never applies under
// another account: a home mirrored from the person's directory links none
// of them, and still links everything else.
func TestStoreRun_ClaudeMirrorLinksNoOrganisationPolicy(t *testing.T) {
	gateRelease(t, "1.4.0-beta.2")
	noKeychain(t)
	src := claudeWorld(t)
	for _, name := range append(append([]string(nil), policy...), nearPolicy...) {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fake.CLI(t, "claude", envClaude)
	fake.Registry(t)
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","token":{"accessToken":"tok","expiresAt":0}}]}`)

	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(b.home, "claude-1")
	for _, name := range policy {
		if _, err := os.Lstat(filepath.Join(own, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must be the home's own, not linked: %v", name, err)
		}
	}
	for _, name := range nearPolicy {
		if target, err := os.Readlink(filepath.Join(own, name)); err != nil || target != filepath.Join(src, name) {
			t.Fatalf("%s is shared like anything else: %q %v", name, target, err)
		}
	}
}

// A home an older rota mirrored holds links for them, and they go on the
// first launch that finds the home quiet, while a real file of that name
// in the home stays as it is and the person's own files are not touched.
func TestStoreRun_ClaudeHomeLosesOlderLinksToOrganisationPolicy(t *testing.T) {
	gateRelease(t, "1.4.0-beta.2")
	noKeychain(t)
	src := claudeWorld(t)
	for _, name := range policy {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fake.CLI(t, "claude", envClaude)
	fake.Registry(t)
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"claude","token":{"accessToken":"tok","expiresAt":0}}]}`)
	own := filepath.Join(b.home, "claude-1")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	linked, real := policy[1:], policy[0]
	for _, name := range linked {
		if err := os.Symlink(filepath.Join(src, name), filepath.Join(own, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(own, real), []byte("the home's own"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range linked {
		if _, err := os.Lstat(filepath.Join(own, name)); !os.IsNotExist(err) {
			t.Fatalf("the link an older rota made for %s goes: %v", name, err)
		}
	}
	if fi, err := os.Lstat(filepath.Join(own, real)); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the home's own %s stays: %v %v", real, fi, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(own, real)); string(raw) != "the home's own" {
		t.Fatalf("as it was: %q", raw)
	}
	for _, name := range policy {
		if raw, err := os.ReadFile(filepath.Join(src, name)); err != nil || string(raw) != name {
			t.Fatalf("the person's %s is untouched: %q %v", name, raw, err)
		}
	}
}

// A launch that panics with the account claimed lets the claim go on its
// way out, and the panic reaches the caller as it was. A server recovers a
// handler's panic and goes on serving; an account left claimed would look
// busy until it restarted, and one whose claim is exclusive could not be
// run at all. The launch is made to panic where every launch saves the
// store, after its claim is taken.
func TestStoreRun_APanickingLaunchLetsGoOfTheAccount(t *testing.T) {
	gateRelease(t, "1.4.0-beta.2")
	for _, c := range []struct{ what, provider string }{
		{"an exclusive claim", "t-alone"},
		{"a shared claude claim", "claude"},
	} {
		t.Run(c.what, func(t *testing.T) {
			if c.provider == "claude" {
				noKeychain(t)
				claudeWorld(t)
				fake.CLI(t, "claude", envClaude)
			}
			fake.Registry(t)
			// Its CLI keeps its own credential file, which is what makes
			// its claim exclusive; a provider without one is never claimed.
			rota.Register(&fake.Owning{Provider: fake.New("t-alone")})
			s, b := seed(t, `{"accounts":[{"id":1,"provider":"`+c.provider+`","token":{"accessToken":"tok","expiresAt":0}}]}`)
			boom := errors.New("a save panicking on purpose")
			b.onSave = func() {
				b.onSave = nil
				panic(boom)
			}
			a := s.Find(1)
			if v := panicked(func() { _, _ = s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil) }); v != boom {
				t.Fatalf("the launch's own panic reaches the caller, as it was: %v", v)
			}
			if s.Busy(a) {
				t.Fatal("the account is let go of on the way out")
			}
			release, ok := s.Hold(a)
			release()
			if !ok {
				t.Fatal("so nothing is in the way of the next one")
			}
		})
	}
}

// A sweep claims an account whose CLI keeps its own credential file while
// it reads that file back; a panic there lets the claim go too, and still
// reaches the caller.
func TestMaintain_APanickingSweepLetsGoOfTheAccount(t *testing.T) {
	gateRelease(t, "1.4.0-beta.2")
	fake.Registry(t)
	boom := errors.New("an adoption panicking on purpose")
	rota.Register(&fake.Owning{
		Provider:  fake.New("t-owns"),
		AdoptFn:   func(*rota.Account, string) error { panic(boom) },
		RefreshFn: func(context.Context, *rota.Account) (*rota.Token, error) { return nil, errors.New("not asked") },
	})
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-owns","token":{"accessToken":"tok","refreshToken":"r1"}}]}`)
	a := s.Find(1)
	if v := panicked(func() { s.Maintain(ctx) }); v != boom {
		t.Fatalf("the panic reaches the caller, as it was: %v", v)
	}
	release, ok := s.Hold(a)
	release()
	if !ok {
		t.Fatal("the account is let go of on the way out")
	}
}

// panicked runs do and returns what it panicked with, nil when it did not.
func panicked(do func()) (v any) {
	defer func() { v = recover() }()
	do()
	return nil
}

// noKeychain skips where a claude account launched through the store asks
// the macOS keychain for the login it keeps in its home: these tests never
// touch the keychain, and Linux runs them.
func noKeychain(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("a claude home's login is looked for in the keychain on macOS")
	}
}

// gateRelease skips unless the module under test is at least version,
// reading pre-releases as releases do: 1.4.0-beta.2 comes before
// 1.4.0-beta.3, which comes before 1.4.0. The package's own since reads
// "1.4.0-beta.3" as later than "1.4.0" and would skip these once 1.4.0 is
// out, so they gate here instead.
func gateRelease(t *testing.T, version string) {
	t.Helper()
	if releaseBefore(rota.Version, version) {
		t.Skipf("rota %s predates %s, which this pins", rota.Version, version)
	}
}

// releaseBefore reports whether version a comes before b by semantic
// versioning: the numbers first, then a release after any pre-release of
// it, then the pre-release's dotted parts, numbers by value and below
// words.
func releaseBefore(a, b string) bool {
	an, ap := splitRelease(a)
	bn, bp := splitRelease(b)
	for i := range max(len(an), len(bn)) {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			return x < y
		}
	}
	switch {
	case ap == "" || ap == bp:
		return false
	case bp == "":
		return true
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := range min(len(as), len(bs)) {
		if as[i] == bs[i] {
			continue
		}
		x, xn := releaseNumber(as[i])
		y, yn := releaseNumber(bs[i])
		switch {
		case xn && yn:
			return x < y
		case xn != yn:
			return xn
		}
		return as[i] < bs[i]
	}
	return len(as) < len(bs)
}

// splitRelease is a version's numbers and its pre-release part, without a
// leading v or any build metadata.
func splitRelease(v string) ([]int, string) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, pre, _ := strings.Cut(v, "-")
	var nums []int
	for _, part := range strings.Split(core, ".") {
		n, _ := releaseNumber(part)
		nums = append(nums, n)
	}
	return nums, pre
}

// releaseNumber reads a part made of digits alone.
func releaseNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
