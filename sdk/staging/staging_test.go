package staging_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

var ctx = context.Background()

// account is a live token on provider, the only shape these verbs look at.
func account(provider string) *rota.Account {
	return rota.NewAccount(1, provider, &rota.Token{Access: "tok", Refresh: "r"})
}

// delegated is an account rota holds no credential for.
func delegated(provider string) *rota.Account {
	return rota.NewAccount(2, provider, &rota.Token{Delegated: true})
}

// bare registers a plain fake under name inside a scoped registry.
func bare(t *testing.T, name string) *fake.Provider {
	t.Helper()
	fake.Registry(t)
	p := fake.New(name)
	rota.Register(p)
	return p
}

func TestStage_DeadIsReauth(t *testing.T) {
	bare(t, "t-s")
	a := account("t-s")
	a.Dead = true
	_, err := rota.Stage(a, "")
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("err=%v", err)
	}
}

func TestStage_UnknownProviderFails(t *testing.T) {
	fake.Registry(t)
	_, err := rota.Stage(account("t-nope"), "")
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
}

func TestStage_CreatesHome0700(t *testing.T) {
	p := bare(t, "t-s")
	home := filepath.Join(t.TempDir(), "home")
	if _, err := rota.Stage(account("t-s"), home); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%v", st.Mode())
	}
	if !slices.Contains(p.Calls(), "launch") {
		t.Fatalf("calls=%v", p.Calls())
	}
}

func TestStage_MkdirFailureSurfaces(t *testing.T) {
	p := bare(t, "t-s")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory cannot be made under a regular file.
	if _, err := rota.Stage(account("t-s"), filepath.Join(file, "home")); err == nil {
		t.Fatal("mkdir under a file succeeded")
	}
	if slices.Contains(p.Calls(), "launch") {
		t.Fatalf("launch ran after mkdir failed: %v", p.Calls())
	}
}

func TestStage_EmptyHomeMakesNoDirectory(t *testing.T) {
	p := bare(t, "t-s")
	seen := "unset"
	p.Command = func(_ *rota.Account, home string) (*rota.Command, error) {
		seen = home
		return &rota.Command{Bin: "t-cli"}, nil
	}
	// MkdirAll("") fails, so a nil error means no directory was attempted.
	if _, err := rota.Stage(account("t-s"), ""); err != nil {
		t.Fatal(err)
	}
	if seen != "" {
		t.Fatalf("launch saw home %q", seen)
	}
}

func TestStagePlan_DeadIsReauth(t *testing.T) {
	bare(t, "t-s")
	a := account("t-s")
	a.Dead = true
	_, _, err := rota.StagePlan(ctx, a, "")
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("err=%v", err)
	}
}

func TestStagePlan_PlannerTouchesNoDisk(t *testing.T) {
	fake.Registry(t)
	p := fake.New("t-s")
	want := []rota.StagedFile{{Path: "auth.json", Mode: 0o600, Content: []byte(`{"k":1}`)}}
	rota.Register(fake.Planner{Provider: p, Fn: func(_ context.Context, _ *rota.Account, home string) (*rota.Command, []rota.StagedFile, error) {
		return &rota.Command{Bin: "t-cli", Env: []string{"T_HOME=" + home}}, want, nil
	}})
	home := t.TempDir()
	cmd, files, err := rota.StagePlan(ctx, account("t-s"), home)
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("cmd=%+v files=%+v", cmd, files)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("home touched: %v", entries)
	}
	if slices.Contains(p.Calls(), "launch") {
		t.Fatalf("a planner's Launch ran: %v", p.Calls())
	}
}

func TestStagePlan_NonPlannerLaunchesWithNilFiles(t *testing.T) {
	p := bare(t, "t-s")
	cmd, files, err := rota.StagePlan(ctx, account("t-s"), "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil || files != nil {
		t.Fatalf("cmd=%+v files=%+v", cmd, files)
	}
	if !slices.Contains(p.Calls(), "launch") {
		t.Fatalf("calls=%v", p.Calls())
	}
}

func TestStagePlan_PlannerErrorPropagates(t *testing.T) {
	fake.Registry(t)
	sentinel := errors.New("plan failed")
	rota.Register(fake.Planner{Provider: fake.New("t-s"), Fn: func(context.Context, *rota.Account, string) (*rota.Command, []rota.StagedFile, error) {
		return nil, nil, sentinel
	}})
	_, _, err := rota.StagePlan(ctx, account("t-s"), "")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
}

func TestAdopt_EmptyHomeIsNilEvenForUnknownProvider(t *testing.T) {
	fake.Registry(t)
	if err := rota.Adopt(account("t-nope"), ""); err != nil {
		t.Fatal(err)
	}
}

func TestAdopt_UnknownProviderFails(t *testing.T) {
	fake.Registry(t)
	err := rota.Adopt(account("t-nope"), t.TempDir())
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
}

func TestAdopt_NonAdopterIsNil(t *testing.T) {
	bare(t, "t-s")
	if err := rota.Adopt(account("t-s"), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestAdopt_AdopterGetsAccountAndHome(t *testing.T) {
	fake.Registry(t)
	var gotAcct *rota.Account
	var gotHome string
	rota.Register(fake.Adopter{Provider: fake.New("t-s"), Fn: func(a *rota.Account, home string) error {
		gotAcct, gotHome = a, home
		return nil
	}})
	a := account("t-s")
	home := t.TempDir()
	if err := rota.Adopt(a, home); err != nil {
		t.Fatal(err)
	}
	if gotAcct != a || gotHome != home {
		t.Fatalf("account=%p want %p home=%q want %q", gotAcct, a, gotHome, home)
	}
}

func TestAdopt_AdopterErrorPropagates(t *testing.T) {
	fake.Registry(t)
	sentinel := errors.New("adopt failed")
	rota.Register(fake.Adopter{Provider: fake.New("t-s"), Fn: func(*rota.Account, string) error { return sentinel }})
	if err := rota.Adopt(account("t-s"), t.TempDir()); !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
}

func TestAdoptFrom_UnknownProviderFails(t *testing.T) {
	fake.Registry(t)
	err := rota.AdoptFrom(account("t-nope"), fstest.MapFS{})
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
}

func TestAdoptFrom_NonFSAdopterIsNil(t *testing.T) {
	bare(t, "t-s")
	if err := rota.AdoptFrom(account("t-s"), fstest.MapFS{}); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptFrom_FSAdopterGetsFS(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.FSAdopter{Provider: fake.New("t-s"), Fn: func(a *rota.Account, fsys fs.FS) error {
		raw, err := fs.ReadFile(fsys, "auth.json")
		if err != nil {
			return err
		}
		a.Email = string(raw)
		return nil
	}})
	a := account("t-s")
	fsys := fstest.MapFS{"auth.json": &fstest.MapFile{Data: []byte("who@example.com")}}
	if err := rota.AdoptFrom(a, fsys); err != nil {
		t.Fatal(err)
	}
	if a.Email != "who@example.com" {
		t.Fatalf("email=%q", a.Email)
	}
}

func TestEnviron_ReplacesDropsAndKeepsOnePerKey(t *testing.T) {
	got := rota.Environ(
		[]string{"A=1", "B=2", "C=3", "D=4"},
		&rota.Command{Env: []string{"A=9", "X=7"}, Drop: []string{"C"}},
	)
	want := []string{"B=2", "D=4", "A=9", "X=7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestEnviron_NilInheritedIsJustEnv(t *testing.T) {
	got := rota.Environ(nil, &rota.Command{Env: []string{"A=1"}, Drop: []string{"A"}})
	if !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Fatalf("got=%v", got)
	}
}

func TestEnviron_NilCommandPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil command did not panic")
		}
	}()
	rota.Environ([]string{"A=1"}, nil)
}

func TestOwnsCredentials_AdopterOrDelegator(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-bare"))
	rota.Register(fake.Adopter{Provider: fake.New("t-adopt"), Fn: func(*rota.Account, string) error { return nil }})
	rota.Register(fake.Delegator{Provider: fake.New("t-deleg")})
	for _, tc := range []struct {
		provider string
		want     bool
	}{
		{"claude", false},
		{"codex", true},
		{"grok", true},
		{"kimi", true},
		{"t-bare", false},
		{"t-adopt", true},
		{"t-deleg", true},
		{"t-nope", false},
	} {
		if got := rota.OwnsCredentials(tc.provider); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.provider, got, tc.want)
		}
	}
}

func TestLoginPlanFor_NeedsDelegatedAccountAndDelegator(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-bare"))
	plan := rota.LoginPlan{Bin: "t-cli", Args: []string{"login"}, Env: []string{"T_HOME=h"}, Drop: []string{"T_KEY"}}
	rota.Register(fake.Delegator{Provider: fake.New("t-deleg"), Plan: plan})
	if _, ok := rota.LoginPlanFor(delegated("t-bare"), "h"); ok {
		t.Fatal("a provider without a delegated login got a plan")
	}
	if _, ok := rota.LoginPlanFor(account("t-deleg"), "h"); ok {
		t.Fatal("a non-delegated account got a plan")
	}
	got, ok := rota.LoginPlanFor(delegated("t-deleg"), "h")
	if !ok || !reflect.DeepEqual(got, plan) {
		t.Fatalf("ok=%v plan=%+v", ok, got)
	}
}
