package account_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// at is the instant every clock-driven test pins.
var at = time.Unix(1_700_000_000, 0)

// acct is an account with an identity, for the matching tests.
func acct(id int, provider, uuid, email string) *rota.Account {
	a := rota.NewAccount(id, provider, &rota.Token{Access: "A"})
	a.UUID = uuid
	a.Email = email
	return a
}

// quota is an account carrying exactly these windows.
func quota(windows ...rota.Window) *rota.Account {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	a.Quota = &rota.Quota{Windows: windows}
	return a
}

func TestNewAccount_AppliesTokenAndMarksStaged(t *testing.T) {
	a := rota.NewAccount(7, "t-new", &rota.Token{
		Access:   "A1",
		Refresh:  "R1",
		Identity: &rota.Identity{UUID: "u1", Email: "e@x", Org: "o1"},
		Extra:    map[string]string{"k": "v"},
	})
	if a.ID != 7 || a.Provider != "t-new" {
		t.Fatalf("id=%d provider=%q", a.ID, a.Provider)
	}
	if a.Staged != "-" {
		t.Fatalf("staged = %q, want the superseded marker -", a.Staged)
	}
	if a.Token.Access != "A1" || a.Token.Refresh != "R1" {
		t.Fatalf("token not applied: %+v", a.Token)
	}
	if a.UUID != "u1" || a.Email != "e@x" || a.Org != "o1" || a.Extra["k"] != "v" {
		t.Fatalf("identity or extra not folded: %+v", a)
	}
}

func TestFindID_FindsOrNil(t *testing.T) {
	list := []*rota.Account{acct(1, "t-x", "", ""), acct(3, "t-x", "", "")}
	if got := rota.FindID(list, 3); got != list[1] {
		t.Fatalf("FindID(3) = %v, want the second account", got)
	}
	if got := rota.FindID(list, 2); got != nil {
		t.Fatalf("FindID(2) = %v, want nil", got)
	}
	if got := rota.FindID(nil, 1); got != nil {
		t.Fatalf("FindID over nil = %v, want nil", got)
	}
}

func TestMatchIdentity_NilIdentityMatchesNothing(t *testing.T) {
	list := []*rota.Account{acct(1, "t-x", "u1", "e@x")}
	if got := rota.MatchIdentity(list, "t-x", nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestMatchIdentity_OnlySameProvider(t *testing.T) {
	list := []*rota.Account{acct(1, "t-a", "u1", "e@x")}
	if got := rota.MatchIdentity(list, "t-b", &rota.Identity{UUID: "u1", Email: "e@x"}); got != nil {
		t.Fatalf("matched across providers: %v", got)
	}
	if got := rota.MatchIdentity(list, "t-a", &rota.Identity{UUID: "u1"}); got != list[0] {
		t.Fatalf("same provider: got %v", got)
	}
}

func TestMatchIdentity_UUIDWinsOverEmail(t *testing.T) {
	list := []*rota.Account{acct(1, "t-x", "u1", "e@x")}
	if got := rota.MatchIdentity(list, "t-x", &rota.Identity{UUID: "u2", Email: "e@x"}); got != nil {
		t.Fatalf("same email, different uuid matched: %v", got)
	}
	if got := rota.MatchIdentity(list, "t-x", &rota.Identity{UUID: "u1", Email: "other@x"}); got != list[0] {
		t.Fatalf("same uuid, different email did not match: %v", got)
	}
}

func TestMatchIdentity_EmailWhenNoUUID(t *testing.T) {
	list := []*rota.Account{acct(1, "t-x", "u1", "e@x"), acct(2, "t-x", "", "f@x")}
	if got := rota.MatchIdentity(list, "t-x", &rota.Identity{Email: "f@x"}); got != list[1] {
		t.Fatalf("got %v, want account 2", got)
	}
	if got := rota.MatchIdentity(list, "t-x", &rota.Identity{Email: "e@x"}); got != list[0] {
		t.Fatalf("got %v, want account 1", got)
	}
	if got := rota.MatchIdentity(list, "t-x", &rota.Identity{Email: "nobody@x"}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestMatchIdentity_NeitherMatchesNothing(t *testing.T) {
	// An empty identity must not match an account that is itself unnamed.
	list := []*rota.Account{acct(1, "t-x", "", "")}
	if got := rota.MatchIdentity(list, "t-x", &rota.Identity{Org: "o1"}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestLabel_EmailThenUUIDThenID(t *testing.T) {
	a := acct(9, "t-x", "abcdefghijklm", "e@x")
	if got := a.Label(); got != "e@x" {
		t.Fatalf("email label = %q", got)
	}
	a.Email = ""
	if got := a.Label(); got != "abcdefghijkl..." {
		t.Fatalf("13-char uuid label = %q, want abcdefghijkl...", got)
	}
	a.UUID = "abcdefghijkl"
	if got := a.Label(); got != "abcdefghijkl" {
		t.Fatalf("12-char uuid label = %q, want it unchanged", got)
	}
	a.UUID = ""
	if got := a.Label(); got != "account-9" {
		t.Fatalf("bare label = %q, want account-9", got)
	}
}

func TestString_IsProviderSlashLabel(t *testing.T) {
	a := acct(4, "t-x", "", "e@x")
	if got := a.String(); got != "t-x/e@x" {
		t.Fatalf("got %q", got)
	}
	a.Email = ""
	if got := a.String(); got != "t-x/account-4" {
		t.Fatalf("got %q", got)
	}
}

func TestPercent_NilQuotaZero(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	if got := a.Percent(); got != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestPercent_MaxOfUnscopedWindows(t *testing.T) {
	a := quota(rota.Window{Name: "5h", Percent: 12.5}, rota.Window{Name: "7d", Percent: 40}, rota.Window{Name: "1m", Percent: 3})
	if got := a.Percent(); got != 40 {
		t.Fatalf("got %v, want 40", got)
	}
}

func TestPercent_IgnoresScoped(t *testing.T) {
	a := quota(rota.Window{Name: "5h", Percent: 12.5}, rota.Window{Name: "model", Percent: 99, Scoped: true})
	if got := a.Percent(); got != 12.5 {
		t.Fatalf("got %v, want 12.5", got)
	}
	// Only scoped windows: nothing covers the whole account, so 0.
	if got := quota(rota.Window{Name: "model", Percent: 99, Scoped: true}).Percent(); got != 0 {
		t.Fatalf("scoped-only got %v, want 0", got)
	}
}

func TestPercent_NeverNegative(t *testing.T) {
	a := quota(rota.Window{Name: "5h", Percent: -5}, rota.Window{Name: "7d", Percent: -1})
	if got := a.Percent(); got != 0 {
		t.Fatalf("got %v, want 0", got)
	}
}

func TestStatus_DeadIsReauth(t *testing.T) {
	a := quota(rota.Window{Name: "5h", Percent: 100})
	a.Dead = true
	if got := a.Status(); got != rota.StatusReauth {
		t.Fatalf("got %q, want reauth even with a spent window", got)
	}
}

func TestStatus_SpentWindowIsLimited(t *testing.T) {
	a := quota(rota.Window{Name: "5h", Percent: 100})
	if got := a.Status(); got != rota.StatusLimited {
		t.Fatalf("got %q, want limited", got)
	}
}

func TestStatus_SpentWindowPastResetIsOK(t *testing.T) {
	fake.Clock(t, at)
	a := quota(rota.Window{Name: "5h", Percent: 100, ResetsAt: rota.When{Time: at.Add(-time.Minute)}})
	if got := a.Status(); got != rota.StatusOK {
		t.Fatalf("got %q, want ok once the window has reset", got)
	}
}

func TestStatus_SpentWindowBeforeResetIsLimited(t *testing.T) {
	advance := fake.Clock(t, at)
	a := quota(rota.Window{Name: "5h", Percent: 100, ResetsAt: rota.When{Time: at.Add(time.Hour)}})
	if got := a.Status(); got != rota.StatusLimited {
		t.Fatalf("got %q, want limited before the reset", got)
	}
	// The same reading flips once the clock passes the reset.
	advance(time.Hour + time.Second)
	if got := a.Status(); got != rota.StatusOK {
		t.Fatalf("got %q, want ok after the reset", got)
	}
}

func TestStatus_ScopedWindowNeverLimits(t *testing.T) {
	a := quota(rota.Window{Name: "model", Percent: 100, Scoped: true})
	if got := a.Status(); got != rota.StatusOK {
		t.Fatalf("got %q, want ok", got)
	}
}

func TestStatus_UnderIsOK(t *testing.T) {
	a := quota(rota.Window{Name: "5h", Percent: 99.9})
	if got := a.Status(); got != rota.StatusOK {
		t.Fatalf("got %q, want ok", got)
	}
	if got := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"}).Status(); got != rota.StatusOK {
		t.Fatalf("no quota got %q, want ok", got)
	}
}

func TestCheckProject_RelativeConfigDirRefused(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	a.ConfigDir = "rel/cfg"
	err := a.CheckProject()
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckProject_RelativeCwdRefused(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	a.Cwd = "rel/work"
	err := a.CheckProject()
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckProject_SameDirRefused(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	a.ConfigDir = "/a/b"
	a.Cwd = "/a/b/"
	err := a.CheckProject()
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckProject_DifferentAbsoluteOK(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	a.ConfigDir = "/a/cfg"
	a.Cwd = "/a/work"
	if err := a.CheckProject(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckProject_EmptyOK(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	if err := a.CheckProject(); err != nil {
		t.Fatal(err)
	}
	a.Cwd = "/only/cwd"
	if err := a.CheckProject(); err != nil {
		t.Fatalf("cwd alone: %v", err)
	}
}

func TestStagedSuperseded_SetsMarker(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A"})
	a.Staged = "abc"
	a.StagedSuperseded()
	if a.Staged != "-" {
		t.Fatalf("staged = %q, want -", a.Staged)
	}
}
