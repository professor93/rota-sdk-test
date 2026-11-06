// These pin a claude account on a Claude Console API key, new in rota
// 1.4.0-beta.1, through what the pinned module already has: the account is
// built from the JSON a store keeps it as, so the package compiles against
// any module and each test skips on one older than the beta. The calls new
// in the beta — Paste, APIKey, LoginKey, PasteLong, HoldsSecret — are in
// v140_local_test.go, behind the local tag.

package apikey_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

var ctx = context.Background()

// key carries SUITEONLY in its middle, which nothing rota says may contain:
// its last four characters are all of it anything shows.
const key = "sk-ant-api03-SUITEONLY0123456789abcdefSUITEONLY0123456789-Ab12"

// keyAccount is an account on key as a store keeps it, plus whatever extra
// fields a test adds.
func keyAccount(t *testing.T, extra string) *rota.Account {
	t.Helper()
	var a rota.Account
	raw := `{"id":5,"provider":"claude","credential":"apikey","uuid":"apikey-5"` + extra + `,"token":{"accessToken":"` + key + `"}}`
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	return &a
}

// TestStage_AKeyAccountRunsOnItsKeyAlone: the key in ANTHROPIC_API_KEY and
// everything else that could sign Claude Code in dropped — even a
// long-lived token the account somehow holds, because a key account is
// billed to its key and to nothing else. Nothing is written into the home.
func TestStage_AKeyAccountRunsOnItsKeyAlone(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	fake.Registry(t)
	a := keyAccount(t, `,"long":{"accessToken":"long-tok","expiresAt":4102444800000}`)
	home := t.TempDir()
	cmd, err := rota.Stage(a, home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "ANTHROPIC_API_KEY="+key) {
		t.Fatalf("the key is the credential: %v", cmd.Env)
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") || strings.Contains(e, "long-tok") {
			t.Fatalf("nothing but the key signs in: %v", cmd.Env)
		}
	}
	for _, v := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"} {
		if !slices.Contains(cmd.Drop, v) {
			t.Fatalf("an inherited %s is dropped: %v", v, cmd.Drop)
		}
	}
	if slices.Contains(cmd.Drop, "ANTHROPIC_API_KEY") {
		t.Fatalf("the key itself is not dropped: %v", cmd.Drop)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("no login is written into its home: %v %v", entries, err)
	}
}

// TestStagePlan_AKeyAccountStagesNoFile: the plan an application carries
// out itself has nothing to write either.
func TestStagePlan_AKeyAccountStagesNoFile(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	fake.Registry(t)
	cmd, files, err := rota.StagePlan(ctx, keyAccount(t, ""), t.TempDir())
	if err != nil || len(files) != 0 || !slices.Contains(cmd.Env, "ANTHROPIC_API_KEY="+key) {
		t.Fatalf("%v %v %v", cmd, files, err)
	}
}

// TestUsage_AKeyAccountReadsNothing: a key has no plan behind it and no
// usage to read, so nothing is asked.
func TestUsage_AKeyAccountReadsNothing(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	fake.Registry(t)
	s := fake.NewServer(t)
	s.Claude(t)
	s.Handle("/usage", func(*http.Request, map[string]any) (int, any) {
		return 200, fake.ClaudeUsage(10, 20, "")
	})
	q, err := rota.Usage(ctx, keyAccount(t, ""))
	if q != nil || err != nil || s.Hits("/usage") != 0 {
		t.Fatalf("quota %v, err %v, asked %d times", q, err, s.Hits("/usage"))
	}
}

// TestLabel_AKeyAccountShowsOnlyItsLastFour: the name somebody gave it,
// else "api key" and the key's last four characters, which are the only
// part of a key anything shows.
func TestLabel_AKeyAccountShowsOnlyItsLastFour(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	if got := keyAccount(t, "").Label(); got != "api key ...Ab12" {
		t.Fatalf("label %q", got)
	}
	if got := keyAccount(t, `,"name":"ci"`).Label(); got != "ci" {
		t.Fatalf("named: %q", got)
	}
}

// TestCheckProject_AKeyAccountTakesNoRemoteControl: Claude Code's Remote
// Control needs a claude.ai login and refuses a key.
func TestCheckProject_AKeyAccountTakesNoRemoteControl(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	err := keyAccount(t, `,"remoteControl":true`).CheckProject()
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "API key") || strings.Contains(err.Error(), "SUITEONLY") {
		t.Fatalf("%v", err)
	}
	if err := keyAccount(t, "").CheckProject(); err != nil {
		t.Fatalf("without it the account is fine: %v", err)
	}
}

// TestEncode_AKeyAccountKeepsWhatItIs: written back the way a store keeps
// it, the account still says it runs on a key, and keeps its name.
func TestEncode_AKeyAccountKeepsWhatItIs(t *testing.T) {
	gateRelease(t, "1.4.0-beta.1")
	raw, err := json.Marshal(keyAccount(t, `,"name":"ci"`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"credential":"apikey"`, `"name":"ci"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s lacks %s", raw, want)
		}
	}
}

// gateRelease skips unless the module under test is at least version,
// reading pre-releases as releases do: 1.4.0-beta.2 comes before
// 1.4.0-beta.3, which comes before 1.4.0.
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
