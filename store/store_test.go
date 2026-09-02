package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/store"
	"rotatest/internal/fake"
)

var ctx = context.Background()

// open locks a memBackend into a store that closes with the test.
func open(t *testing.T, b *memBackend) *store.Store {
	t.Helper()
	s, err := store.NewStore(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seed opens a store over a raw JSON blob, the way any account state is
// built without a login.
func seed(t *testing.T, blob string) (*store.Store, *memBackend) {
	t.Helper()
	b := &memBackend{blob: []byte(blob), home: t.TempDir()}
	return open(t, b), b
}

// login runs a whole login on s and returns the account it landed on.
func login(t *testing.T, s *store.Store, provider, code string) (*rota.Account, bool) {
	t.Helper()
	l, err := s.BeginLogin(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	a, added, err := s.FinishLogin(ctx, l.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	return a, added
}

// lockFreeWithin fails unless b's lock can be taken promptly: the way to see
// from outside that a store released it.
func lockFreeWithin(t *testing.T, b *memBackend, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		if unlock, err := b.Lock(); err == nil {
			unlock()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("the lock was not released")
	}
}

func pendingPath(b *memBackend) string { return filepath.Join(b.home, "pending.json") }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// recorder collects strings from provider callbacks, which Store.Refresh
// runs in parallel.
type recorder struct {
	mu sync.Mutex
	v  []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.v = append(r.v, s)
	r.mu.Unlock()
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.v...)
}

// rota detects an optional ability with a plain type assertion, so a
// wrapper embedding the rota.Provider interface shows only its own method.
// These embed the concrete fake instead, so the abilities really combine.

// owner is a provider whose CLI owns its credential file: an Adopter, so
// rota.OwnsCredentials is true and the run claim is real.
type owner struct {
	*fake.Provider
	adopt func(a *rota.Account, home string) error
}

func (o owner) Adopt(a *rota.Account, home string) error {
	if o.adopt != nil {
		return o.adopt(a, home)
	}
	return nil
}

// ownerRefresher owns credentials and can refresh, which is what Maintain
// and Run order against each other.
type ownerRefresher struct {
	owner
	refresh func(ctx context.Context, a *rota.Account) (*rota.Token, error)
}

func (o ownerRefresher) Refresh(ctx context.Context, a *rota.Account) (*rota.Token, error) {
	return o.refresh(ctx, a)
}

// ownerMeter owns credentials and is metered, so a busy account can be
// skipped by a usage refresh.
type ownerMeter struct {
	owner
	quota func(ctx context.Context, access string) (*rota.Quota, error)
}

func (o ownerMeter) Quota(ctx context.Context, access string) (*rota.Quota, error) {
	return o.quota(ctx, access)
}

// claudeOwner is an ownerRefresher that speaks the claude CLI vocabulary,
// so a run on it builds an argv and reaches the fake CLI.
type claudeOwner struct{ ownerRefresher }

func (claudeOwner) Flavor() string { return "claude" }
func (claudeOwner) Models() []rota.Model {
	return []rota.Model{{ID: "t-model-1", Aliases: []string{"one"}}, {ID: "t-model-2"}}
}
func (claudeOwner) Efforts() []string                { return []string{"low", "high"} }
func (claudeOwner) Defaults() (model, effort string) { return "t-model-1", "low" }

// ordered is an owner that records adopt/refresh in the order rota asks.
func ordered(name string, calls *recorder) ownerRefresher {
	return ownerRefresher{
		owner: owner{Provider: fake.New(name), adopt: func(*rota.Account, string) error {
			calls.add("adopt")
			return nil
		}},
		refresh: func(context.Context, *rota.Account) (*rota.Token, error) {
			calls.add("refresh")
			return &rota.Token{Access: "new", Refresh: "r-new", ExpiresAt: rota.NowMS() + 3_600_000}, nil
		},
	}
}

// quotaOK is a meter answering one window at pct.
func quotaOK(pct float64) *rota.Quota {
	return &rota.Quota{Windows: []rota.Window{{Name: "w", Percent: pct, Primary: true}}}
}

// --- NewStore, Open, DefaultDir ---------------------------------------------

func TestNewStore_LockErrorReturnsNoStore(t *testing.T) {
	b := &memBackend{home: t.TempDir(), failOn: "lock"}
	s, err := store.NewStore(b)
	if err == nil || s != nil {
		t.Fatalf("store=%v err=%v, want no store and an error", s, err)
	}
	if b.loads != 0 {
		t.Fatalf("loads = %d; nothing may be read without the lock", b.loads)
	}
}

func TestNewStore_LoadErrorCloses(t *testing.T) {
	b := &memBackend{home: t.TempDir(), failOn: "load"}
	s, err := store.NewStore(b)
	if err == nil || s != nil {
		t.Fatalf("store=%v err=%v, want no store and an error", s, err)
	}
	if b.saves != 0 {
		t.Fatalf("saves = %d, want 0: a store that never loaded must not write", b.saves)
	}
	// The failed open closed itself, so the lock is free again and a later
	// open does not queue behind a store that no longer exists.
	lockFreeWithin(t, b, 2*time.Second)
	b.failOn = ""
	open(t, b).Close()
	if b.saves != 0 {
		t.Fatalf("saves = %d after a reopen, want 0", b.saves)
	}
}

func TestNewStore_CorruptBytes(t *testing.T) {
	b := &memBackend{home: t.TempDir(), blob: []byte(`{"accounts":[`)}
	s, err := store.NewStore(b)
	if err == nil || s != nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("store=%v err=%v, want an error naming corrupt bytes", s, err)
	}
	lockFreeWithin(t, b, 2*time.Second)
}

func TestNewStore_EmptyProviderDefaults(t *testing.T) {
	// Pinned to a name of our own, so a literal "claude" would not pass.
	fake.Restore(t, &rota.DefaultProvider, "t-default")
	s, _ := seed(t, `{"accounts":[{"id":1,"token":{"accessToken":"a"}},{"id":2,"provider":"t-p"}]}`)
	if got := s.Find(1).Provider; got != rota.DefaultProvider {
		t.Fatalf("provider = %q, want rota.DefaultProvider %q", got, rota.DefaultProvider)
	}
	if got := s.Find(2).Provider; got != "t-p" {
		t.Fatalf("a named provider is kept, got %q", got)
	}
}

func TestNewStore_NextIDRaisedPastMaxID(t *testing.T) {
	s, _ := seed(t, `{"nextId":2,"accounts":[{"id":7,"provider":"t-p"},{"id":3,"provider":"t-p"}]}`)
	if s.NextID != 8 {
		t.Fatalf("NextID = %d, want 8 (one past the highest id)", s.NextID)
	}
	// A stored NextID above every id is what a retired id looks like; it is
	// kept rather than lowered.
	s, _ = seed(t, `{"nextId":10,"accounts":[{"id":3,"provider":"t-p"}]}`)
	if s.NextID != 10 {
		t.Fatalf("NextID = %d, want the stored 10", s.NextID)
	}
}

func TestOpen_FirstRunIsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.Accounts) != 0 || s.NextID != 0 {
		t.Fatalf("accounts=%d NextID=%d, want an empty store", len(s.Accounts), s.NextID)
	}
	if exists(filepath.Join(dir, "accounts.json")) {
		t.Fatal("opening writes nothing; accounts.json must not exist yet")
	}
}

func TestDefaultDir_HonoursROTAHOME(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ROTA_HOME", dir)
	got, err := store.DefaultDir()
	if err != nil || got != dir {
		t.Fatalf("DefaultDir() = %q, %v; want %q", got, err, dir)
	}
}

// --- FileBackend -------------------------------------------------------------

func TestFileBackend_SaveIsAtomic0600(t *testing.T) {
	dir := t.TempDir()
	b, err := store.NewFileBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Save([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "accounts.json")
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("accounts.json: %v mode %v, want 0600", err, fi.Mode())
	}
	if tmp, _ := filepath.Glob(path + "-*.tmp"); len(tmp) != 0 {
		t.Fatalf("temp files left behind: %v", tmp)
	}
	// A second save replaces the whole blob.
	if err := b.Save([]byte(`{"nextId":3}`)); err != nil {
		t.Fatal(err)
	}
	if raw, _ := b.Load(); string(raw) != `{"nextId":3}` {
		t.Fatalf("Load = %q after the second save", raw)
	}
}

func TestFileBackend_LoadSweepsOldTemp(t *testing.T) {
	dir := t.TempDir()
	b, err := store.NewFileBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "accounts.json-old.tmp")
	fresh := filepath.Join(dir, "accounts.json-new.tmp")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	raw, err := b.Load()
	if err != nil || raw != nil {
		t.Fatalf("Load = %q, %v; want (nil, nil) on a first run", raw, err)
	}
	if exists(old) {
		t.Fatal("a temp file two hours old must be swept")
	}
	if !exists(fresh) {
		t.Fatal("a fresh temp file may belong to a write in flight and must stay")
	}
}

// --- Save, Close, Home -------------------------------------------------------

func TestStoreSave_AfterReleaseRefuses(t *testing.T) {
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-p"}]}`)
	if err := s.Release(); err != nil {
		t.Fatal(err)
	}
	err := s.Save()
	if err == nil || !strings.Contains(err.Error(), "reopen") {
		t.Fatalf("Save after Release = %v, want a refusal that says to reopen", err)
	}
	if b.saves != 0 {
		t.Fatalf("saves = %d, want 0", b.saves)
	}
	if s.Find(1) == nil {
		t.Fatal("what was loaded stays readable after Release")
	}
}

func TestStoreClose_Idempotent(t *testing.T) {
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	for i := range 3 {
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	if err := s.Release(); err != nil {
		t.Fatalf("Release after Close: %v", err)
	}
	if b.locks != 1 {
		t.Fatalf("locks = %d, want the one taken by NewStore", b.locks)
	}
	lockFreeWithin(t, b, 2*time.Second)
}

func TestStoreHome_ConfigDirOrRootNoCreate(t *testing.T) {
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	a := &rota.Account{ID: 3, Provider: "t-p"}
	if got, want := s.Home(a), filepath.Join(b.home, "t-p-3"); got != want {
		t.Fatalf("Home = %q, want %q", got, want)
	}
	if exists(s.Home(a)) {
		t.Fatal("Home must not create the directory")
	}
	a.ConfigDir = filepath.Join(t.TempDir(), "own")
	if got := s.Home(a); got != a.ConfigDir {
		t.Fatalf("Home = %q, want the account's ConfigDir %q", got, a.ConfigDir)
	}
	if exists(a.ConfigDir) {
		t.Fatal("Home must not create ConfigDir either")
	}
}

// --- Remove ------------------------------------------------------------------

func TestStoreRemove_UnknownIsNoAccount(t *testing.T) {
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-p"}]}`)
	err := s.Remove(9)
	if !errors.Is(err, rota.ErrNoAccount) || !strings.Contains(err.Error(), "9") {
		t.Fatalf("Remove(9) = %v, want ErrNoAccount naming 9", err)
	}
	if len(s.Accounts) != 1 {
		t.Fatal("nothing may be removed")
	}
}

func TestStoreRemove_BusyIsErrBusy(t *testing.T) {
	fake.Registry(t)
	rota.Register(owner{Provider: fake.New("t-own")})
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-own","token":{"accessToken":"a"}}]}`)
	a := s.Find(1)
	release, ok := s.Hold(a)
	if !ok {
		t.Fatal("nothing else holds the account")
	}
	defer release()
	marker := filepath.Join(s.Home(a), "auth.json")
	if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.Remove(1)
	if !errors.Is(err, rota.ErrBusy) || !strings.Contains(err.Error(), "stop it") {
		t.Fatalf("Remove while held = %v, want ErrBusy", err)
	}
	if s.Find(1) == nil || !exists(marker) {
		t.Fatal("a refused removal must delete and forget nothing")
	}
	release()
	if err := s.Remove(1); err != nil {
		t.Fatalf("Remove after release: %v", err)
	}
}

func TestStoreRemove_DeletesHomeAndRetiresID(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-p","token":{"accessToken":"a"}}]}`)
	home := s.Home(s.Find(1))
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(1); err != nil {
		t.Fatal(err)
	}
	if exists(home) {
		t.Fatal("the private home goes with the account")
	}
	if s.Find(1) != nil || len(s.Accounts) != 0 {
		t.Fatal("the account must be forgotten")
	}
	if s.NextID != 2 {
		t.Fatalf("NextID = %d, want 2: the id is retired", s.NextID)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	again := open(t, b)
	a, added := login(t, again, "t-p", "c1")
	if !added || a.ID != 2 {
		t.Fatalf("after reopen a login got id %d (added=%v), want a fresh 2", a.ID, added)
	}
}

// --- BeginLogin ---------------------------------------------------------------

func TestStoreBeginLogin_UnknownProviderParksNothing(t *testing.T) {
	fake.Registry(t)
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	_, err := s.BeginLogin(ctx, "t-nope")
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if exists(pendingPath(b)) {
		t.Fatal("a login that never began must not be parked")
	}
}

func TestStoreBeginLogin_ParksPendingJSON0600(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	l, err := s.BeginLogin(ctx, "t-p")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(pendingPath(b))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("pending.json: %v mode %v, want 0600", err, fi.Mode())
	}
	raw, _ := os.ReadFile(pendingPath(b))
	var parked map[string]*rota.Login
	if err := json.Unmarshal(raw, &parked); err != nil {
		t.Fatalf("pending.json is not a map of logins: %v\n%s", err, raw)
	}
	p := parked[l.ID]
	if p == nil || p.Provider != "t-p" || p.URL != l.URL || p.State["verifier"] != "v" || p.CreatedAt != l.CreatedAt {
		t.Fatalf("parked %+v, want the login as returned %+v", p, l)
	}
	if b.saves != 0 {
		t.Fatalf("saves = %d; a pending login lives beside the homes, not in the blob", b.saves)
	}
}

func TestStoreBeginLogin_SecondStoreOnSameHomeCanFinish(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	home := t.TempDir()
	b1 := &memBackend{home: home}
	b2 := &memBackend{home: home}
	s1 := open(t, b1)
	s2 := open(t, b2)
	l, err := s1.BeginLogin(ctx, "t-p")
	if err != nil {
		t.Fatal(err)
	}
	a, added, err := s2.FinishLogin(ctx, l.ID, "c1")
	if err != nil || !added || a.ID != 1 || a.Token.Access != "c1" {
		t.Fatalf("a=%+v added=%v err=%v; the other process must be able to finish", a, added, err)
	}
	if s2.Find(1) == nil || b2.saves != 1 {
		t.Fatalf("the finishing store keeps and saves the account: saves=%d", b2.saves)
	}
	if exists(pendingPath(b2)) {
		t.Fatal("the pending entry is consumed")
	}
}

// --- FinishLogin --------------------------------------------------------------

func TestStoreFinishLogin_UnknownIDIsNoLogin(t *testing.T) {
	fake.Registry(t)
	s, _ := seed(t, `{}`)
	_, _, err := s.FinishLogin(ctx, "zzzzzz", "c1")
	if !errors.Is(err, rota.ErrNoLogin) || !strings.Contains(err.Error(), "zzzzzz") {
		t.Fatalf("err = %v, want ErrNoLogin naming the id", err)
	}
}

func TestStoreFinishLogin_RejectedCodeKeepsPending(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	l, err := s.BeginLogin(ctx, "t-p")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.FinishLogin(ctx, l.ID, "bad")
	if err == nil || errors.Is(err, rota.ErrNoLogin) || !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("a rejected code = %v, want the provider's refusal, not ErrNoLogin", err)
	}
	if !exists(pendingPath(b)) {
		t.Fatal("the pending entry must survive a typo")
	}
	a, added, err := s.FinishLogin(ctx, l.ID, "c1")
	if err != nil || !added || a.Token.Access != "c1" {
		t.Fatalf("retry: a=%+v added=%v err=%v", a, added, err)
	}
}

func TestStoreFinishLogin_AuthPendingKeepsPending(t *testing.T) {
	fake.Registry(t)
	p := fake.New("t-dev")
	p.Kind = "device"
	p.CompleteErr = rota.ErrAuthPending
	rota.Register(p)
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	l, err := s.BeginLogin(ctx, "t-dev")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, _, err := s.FinishLogin(ctx, l.ID, ""); !errors.Is(err, rota.ErrAuthPending) {
			t.Fatalf("poll #%d: err = %v, want ErrAuthPending passed through", i+1, err)
		}
	}
	if !exists(pendingPath(b)) || b.saves != 0 {
		t.Fatalf("an unapproved login stays parked and nothing is saved (saves=%d)", b.saves)
	}
}

func TestStoreFinishLogin_MatchUpdatesInPlace(t *testing.T) {
	fake.Registry(t)
	p := fake.New("t-p")
	p.Identity = &rota.Identity{UUID: "u1", Email: "e@x"}
	rota.Register(p)
	s, _ := seed(t, `{"accounts":[
		{"id":1,"provider":"t-p","uuid":"u1","token":{"accessToken":"old","refreshToken":"r-old"}},
		{"id":2,"provider":"t-other","uuid":"u1","token":{"accessToken":"x"}}]}`)
	before := s.Find(1)
	a, added := login(t, s, "t-p", "c1")
	if a != before || added {
		t.Fatalf("a=%p before=%p added=%v; the same identity updates in place", a, before, added)
	}
	if a.Token.Access != "c1" || a.Token.Refresh != "r-c1" || a.Email != "e@x" {
		t.Fatalf("token not replaced: %+v", a.Token)
	}
	if len(s.Accounts) != 2 || s.Find(2).Token.Access != "x" {
		t.Fatal("the same identity under another provider is a different account and untouched")
	}
}

func TestStoreFinishLogin_NoMatchAddsFreshIDAndWipesStaleHome(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	stale := filepath.Join(b.home, "t-p-1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "junk"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, added := login(t, s, "t-p", "c1")
	if !added || a.ID != 1 || a.Provider != "t-p" || a.Order != 1 {
		t.Fatalf("a=%+v added=%v, want a fresh id 1 at the end of the queue", a, added)
	}
	if exists(stale) {
		t.Fatal("a home left at this id by an older account must be wiped before use")
	}
	if b.saves != 1 || s.NextID != 2 {
		t.Fatalf("saves=%d NextID=%d, want 1 and 2", b.saves, s.NextID)
	}
}

func TestStoreFinishLogin_ResetsQuotaAndStaged(t *testing.T) {
	fake.Registry(t)
	p := fake.New("t-p")
	p.Identity = &rota.Identity{UUID: "u1"}
	rota.Register(p)
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-p","uuid":"u1","dead":true,"staged":"abcd",
		"quota":{"windows":[{"name":"w","percent":50}]},"quotaAt":123,
		"token":{"accessToken":"old","refreshToken":"r-old"}}]}`)
	a, added := login(t, s, "t-p", "c1")
	if added {
		t.Fatal("same uuid must match")
	}
	if a.Quota != nil || a.QuotaAt != 0 {
		t.Fatalf("quota=%v quotaAt=%d, want the reading dropped", a.Quota, a.QuotaAt)
	}
	if a.Staged != "-" {
		t.Fatalf("Staged = %q, want %q: whatever was staged belongs to the old login", a.Staged, "-")
	}
	if a.Dead {
		t.Fatal("a fresh login revives the account")
	}
}

func TestStoreFinishLogin_ExpiredPendingIsNoLogin(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	// Expiry is measured on the wall clock, not rota.Now: a pinned rota.Now
	// far in the past would otherwise keep the entry alive.
	fake.Clock(t, time.Unix(1_700_000_000, 0))
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	l, err := s.BeginLogin(ctx, "t-p")
	if err != nil {
		t.Fatal(err)
	}
	// Age the entry in the shape BeginLogin wrote it.
	raw, err := os.ReadFile(pendingPath(b))
	if err != nil {
		t.Fatal(err)
	}
	var parked map[string]*rota.Login
	if err := json.Unmarshal(raw, &parked); err != nil {
		t.Fatal(err)
	}
	parked[l.ID].CreatedAt = time.Now().Add(-16 * time.Minute).UnixMilli()
	raw, _ = json.Marshal(parked)
	if err := os.WriteFile(pendingPath(b), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.FinishLogin(ctx, l.ID, "c1")
	if !errors.Is(err, rota.ErrNoLogin) {
		t.Fatalf("err = %v, want ErrNoLogin for a login older than 15 minutes", err)
	}
}

// --- Refresh -------------------------------------------------------------------

func TestStoreRefresh_SkipsDeadUnmeteredFreshAndBusy(t *testing.T) {
	fake.Registry(t)
	var calls recorder
	meter := func(_ context.Context, access string) (*rota.Quota, error) {
		calls.add(access)
		return quotaOK(7), nil
	}
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: meter})
	rota.Register(fake.New("t-plain"))
	rota.Register(ownerMeter{owner: owner{Provider: fake.New("t-om")}, quota: meter})
	fresh := time.Now().UnixMilli()
	s, _ := seed(t, fmt.Sprintf(`{"accounts":[
		{"id":1,"provider":"t-m","dead":true,"token":{"accessToken":"dead"}},
		{"id":2,"provider":"t-plain","token":{"accessToken":"plain"}},
		{"id":3,"provider":"t-m","quotaAt":%d,"quota":{"windows":[{"name":"w","percent":1}]},"token":{"accessToken":"fresh"}},
		{"id":4,"provider":"t-om","token":{"accessToken":"busy"}},
		{"id":5,"provider":"t-m","token":{"accessToken":"live"}}]}`, fresh))
	release, ok := s.Hold(s.Find(4))
	if !ok {
		t.Fatal("nothing else holds account 4")
	}
	defer release()
	if errs := s.Refresh(ctx, false); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if got := calls.all(); !slices.Equal(got, []string{"live"}) {
		t.Fatalf("meter asked for %v, want only the live account", got)
	}
	for _, id := range []int{1, 2, 4} {
		if s.Find(id).Quota != nil {
			t.Fatalf("account %d must be left alone", id)
		}
	}
	if s.Find(3).QuotaAt != fresh || s.Find(3).Quota.Windows[0].Percent != 1 {
		t.Fatal("a reading younger than QuotaTTL is kept")
	}
	if a := s.Find(5); a.Quota == nil || a.Quota.Windows[0].Percent != 7 || a.QuotaAt == 0 {
		t.Fatalf("the live account gets a reading: %+v", a)
	}
}

func TestStoreRefresh_ForceIgnoresTTL(t *testing.T) {
	fake.Registry(t)
	calls := 0
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(context.Context, string) (*rota.Quota, error) {
		calls++
		return quotaOK(3), nil
	}})
	was := time.Now().Add(-time.Minute).UnixMilli()
	s, _ := seed(t, fmt.Sprintf(`{"accounts":[{"id":1,"provider":"t-m","quotaAt":%d,"quota":{"windows":[]},"token":{"accessToken":"a"}}]}`, was))
	if errs := s.Refresh(ctx, false); len(errs) != 0 || calls != 0 {
		t.Fatalf("errs=%v calls=%d; a minute-old reading is served from cache", errs, calls)
	}
	if errs := s.Refresh(ctx, true); len(errs) != 0 || calls != 1 {
		t.Fatalf("errs=%v calls=%d; force must re-read", errs, calls)
	}
	if a := s.Find(1); a.QuotaAt <= was || a.Quota.Windows[0].Percent != 3 {
		t.Fatalf("the forced reading must replace the cached one: %+v", a)
	}
}

func TestStoreRefresh_PanicInProviderBecomesError(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(context.Context, string) (*rota.Quota, error) {
		panic("kaboom")
	}})
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-m","token":{"accessToken":"a"}}]}`)
	errs := s.Refresh(ctx, false)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "panic while refreshing") || !strings.Contains(errs[0].Error(), "kaboom") {
		t.Fatalf("errs = %v, want one error carrying the panic", errs)
	}
	if s.Find(1).Quota != nil || b.saves != 0 {
		t.Fatalf("nothing changed, nothing saved: quota=%v saves=%d", s.Find(1).Quota, b.saves)
	}
}

func TestStoreRefresh_SavesOnceWhenChanged(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(context.Context, string) (*rota.Quota, error) {
		return quotaOK(2), nil
	}})
	s, b := seed(t, `{"accounts":[
		{"id":1,"provider":"t-m","token":{"accessToken":"a"}},
		{"id":2,"provider":"t-m","token":{"accessToken":"b"}}]}`)
	if errs := s.Refresh(ctx, false); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if b.saves != 1 {
		t.Fatalf("saves = %d, want exactly 1 for two changed accounts", b.saves)
	}
	if s.Find(1).Quota == nil || s.Find(2).Quota == nil {
		t.Fatal("both readings must be taken")
	}
	if errs := s.Refresh(ctx, false); len(errs) != 0 || b.saves != 1 {
		t.Fatalf("errs=%v saves=%d; a pass that changes nothing saves nothing", errs, b.saves)
	}
}

func TestStoreRefresh_CollectsErrorsNeverFatal(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Meter{Provider: fake.New("t-bad"), Fn: func(context.Context, string) (*rota.Quota, error) {
		return nil, errors.New("429")
	}})
	rota.Register(fake.Meter{Provider: fake.New("t-good"), Fn: func(context.Context, string) (*rota.Quota, error) {
		return quotaOK(9), nil
	}})
	s, b := seed(t, `{"accounts":[
		{"id":1,"provider":"t-bad","token":{"accessToken":"a"}},
		{"id":2,"provider":"t-good","token":{"accessToken":"b"}}]}`)
	errs := s.Refresh(ctx, false)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "quota") || !strings.Contains(errs[0].Error(), "429") {
		t.Fatalf("errs = %v, want the one quota failure", errs)
	}
	if s.Find(1).Quota != nil || s.Find(2).Quota == nil || b.saves != 1 {
		t.Fatalf("the other account is still read and saved: q1=%v q2=%v saves=%d", s.Find(1).Quota, s.Find(2).Quota, b.saves)
	}
	// A failing save is one more collected error, not a panic or an abort.
	b.failOn = "save"
	errs = s.Refresh(ctx, true)
	if len(errs) != 2 || !strings.Contains(errs[1].Error(), "boom") {
		t.Fatalf("errs = %v, want the quota failure then the save failure", errs)
	}
}

// --- Maintain ----------------------------------------------------------------

func TestStoreMaintain_AdoptsThenRefreshesAndSkipsBusy(t *testing.T) {
	fake.Registry(t)
	var calls recorder
	rota.Register(ordered("t-ord", &calls))
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-ord","token":{"accessToken":"old","refreshToken":"r","expiresAt":1}}]}`)
	a := s.Find(1)
	if errs := s.Maintain(ctx); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if got := calls.all(); !slices.Equal(got, []string{"adopt", "refresh"}) {
		t.Fatalf("calls = %v, want adopt before refresh", got)
	}
	if a.Token.Access != "new" || b.saves != 1 {
		t.Fatalf("access=%q saves=%d; the rotated token must be saved once", a.Token.Access, b.saves)
	}
	// Held by a run: neither adopted nor refreshed, however overdue.
	a.Token.ExpiresAt = 1
	calls.v = nil
	release, ok := s.Hold(a)
	if !ok {
		t.Fatal("nothing else holds the account")
	}
	defer release()
	if errs := s.Maintain(ctx); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if got := calls.all(); len(got) != 0 {
		t.Fatalf("calls = %v on a busy account, want none", got)
	}
}

// --- Run ---------------------------------------------------------------------

func TestStoreRun_DeadIsReauth(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-p","dead":true,"token":{"accessToken":"a"}}]}`)
	_, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if !errors.Is(err, rota.ErrReauth) || !strings.Contains(err.Error(), "log in again") {
		t.Fatalf("err = %v, want ErrReauth", err)
	}
	if b.saves != 0 {
		t.Fatalf("saves = %d, want 0", b.saves)
	}
}

func TestStoreRun_BusyIsErrBusy(t *testing.T) {
	fake.Registry(t)
	rota.Register(owner{Provider: fake.New("t-own")})
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-own","token":{"accessToken":"a"}}]}`)
	a := s.Find(1)
	release, ok := s.Hold(a)
	if !ok {
		t.Fatal("nothing else holds the account")
	}
	defer release()
	_, err := s.Run(ctx, a, rota.Spec{Prompt: "hi"}, nil, nil)
	if !errors.Is(err, rota.ErrBusy) || !strings.Contains(err.Error(), "refresh token") {
		t.Fatalf("err = %v, want ErrBusy saying what it protects", err)
	}
}

func TestStoreRun_AdoptsBeforeRefresh(t *testing.T) {
	fake.CLI(t, "t-cli", fake.ClaudeResult(0))
	fake.Registry(t)
	var calls recorder
	rota.Register(claudeOwner{ordered("t-co", &calls)})
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-co","token":{"accessToken":"old","refreshToken":"r","expiresAt":1}}]}`)
	res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.all(); !slices.Equal(got, []string{"adopt", "refresh"}) {
		t.Fatalf("calls = %v, want adopt before refresh", got)
	}
	if !strings.HasPrefix(res.Result, "STDIN=hi ARGS=") || s.Find(1).Token.Access != "new" {
		t.Fatalf("res=%q access=%q; the run uses the refreshed token", res.Result, s.Find(1).Token.Access)
	}
	if b.saves != 2 {
		t.Fatalf("saves = %d, want 2: once after the refresh, once after staging", b.saves)
	}
}

func TestStoreRun_SavesBeforeRun(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "started")
	t.Setenv("T_MARK", mark)
	// The CLI announces itself, then lingers so a late save would be seen.
	dir := fake.CLI(t, "t-cli", "touch \"$T_MARK\"\nsleep 0.1\n"+fake.ClaudeResult(0))
	fake.Registry(t)
	p := fake.New("t-run")
	p.BaseEnv = fake.BaseEnv(dir)
	rota.Register(fake.Claude(p))
	b := &memBackend{home: t.TempDir(), blob: []byte(`{"accounts":[{"id":1,"provider":"t-run","token":{"accessToken":"tok"}}]}`)}
	afterStart := 0
	b.onSave = func() {
		if exists(mark) {
			afterStart++
		}
	}
	s := open(t, b)
	res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil || !strings.HasPrefix(res.Result, "STDIN=hi ARGS=") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !exists(mark) {
		t.Fatal("the CLI did not run")
	}
	if b.saves < 1 || afterStart != 0 {
		t.Fatalf("saves=%d, %d of them after the CLI started; every save comes first", b.saves, afterStart)
	}
}

func TestStoreRun_StageErrorStillSaves(t *testing.T) {
	fake.Registry(t)
	boom := errors.New("cannot stage")
	p := fake.New("t-run")
	p.Command = func(*rota.Account, string) (*rota.Command, error) { return nil, boom }
	rota.Register(fake.Claude(p))
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-run","token":{"accessToken":"tok"}}]}`)
	_, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the staging error", err)
	}
	if b.saves != 1 {
		t.Fatalf("saves = %d, want 1: staging may have changed the account before failing", b.saves)
	}
}

func TestStoreRun_ReleasesLockSoSaveRefuses(t *testing.T) {
	dir := fake.CLI(t, "t-cli", fake.ClaudeResult(0))
	fake.Registry(t)
	p := fake.New("t-run")
	p.BaseEnv = fake.BaseEnv(dir)
	rota.Register(fake.Claude(p))
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-run","token":{"accessToken":"tok"}}]}`)
	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	err := s.Save()
	if err == nil || !strings.Contains(err.Error(), "reopen") {
		t.Fatalf("Save after Run = %v, want a refusal: the lock went with the run", err)
	}
	lockFreeWithin(t, b, 2*time.Second)
}

func TestStoreRun_ChildEnvIsHostEnvWithoutHiddenNames(t *testing.T) {
	t.Setenv("ROTA_HOME", t.TempDir())
	store.HideFromAgents("T_SECRET")
	t.Setenv("T_SECRET", "1")
	t.Setenv("T_PLAIN", "p")
	dir := fake.CLI(t, "t-cli", `cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"ROTA_HOME=%s T_SECRET=%s T_PLAIN=%s"}\n' "$ROTA_HOME" "$T_SECRET" "$T_PLAIN"
`)
	fake.Registry(t)
	p := fake.New("t-run")
	p.BaseEnv = fake.BaseEnv(dir)
	rota.Register(fake.Claude(p))
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-run","token":{"accessToken":"tok"}}]}`)
	res, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "ROTA_HOME= T_SECRET= T_PLAIN=p" {
		t.Fatalf("child saw %q; hidden names must be empty, the rest inherited", res.Result)
	}
}

// --- Prepare -----------------------------------------------------------------

func TestStorePrepare_ReturnsBinaryEnvAndClaim(t *testing.T) {
	t.Setenv("ROTA_HOME", t.TempDir())
	dir := fake.CLI(t, "t-cli", "exit 0\n")
	fake.Registry(t)
	rota.Register(owner{Provider: fake.New("t-own")})
	s, b := seed(t, `{"accounts":[{"id":1,"provider":"t-own","token":{"accessToken":"tok"}}]}`)
	a := s.Find(1)
	path, env, release, err := s.Prepare(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "t-cli"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if !slices.Contains(env, "T_TOKEN=tok") {
		t.Fatalf("env %v lacks the credential", env)
	}
	if slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "ROTA_HOME=") }) {
		t.Fatal("env must be HostEnv: ROTA_HOME is hidden")
	}
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "PATH=") }) {
		t.Fatal("env must carry the host PATH")
	}
	if !s.Busy(a) {
		t.Fatal("the claim is handed back still held")
	}
	release()
	if s.Busy(a) {
		t.Fatal("release frees the claim")
	}
	if b.saves != 1 {
		t.Fatalf("saves = %d, want 1 before the handover", b.saves)
	}
}

func TestStorePrepare_LookPathFailureReleasesClaim(t *testing.T) {
	fake.Registry(t)
	p := fake.New("t-own")
	p.Command = func(*rota.Account, string) (*rota.Command, error) {
		return &rota.Command{Bin: "t-no-such-binary-xyz"}, nil
	}
	rota.Register(owner{Provider: p})
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-own","token":{"accessToken":"tok"}}]}`)
	a := s.Find(1)
	_, _, release, err := s.Prepare(ctx, a)
	if err == nil || !strings.Contains(err.Error(), "PATH") || release != nil {
		t.Fatalf("err=%v claim=%v, want a PATH error and no claim", err, release != nil)
	}
	if s.Busy(a) {
		t.Fatal("a failed prepare must not leave the account claimed")
	}
	r, ok := s.Hold(a)
	if !ok {
		t.Fatal("the claim is free to take again")
	}
	r()
}

// --- Hold, Busy --------------------------------------------------------------

func TestStoreHoldBusy_NoOpForNonOwners(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-p","token":{"accessToken":"a"}}]}`)
	a := s.Find(1)
	r1, ok1 := s.Hold(a)
	r2, ok2 := s.Hold(a)
	if !ok1 || !ok2 || r1 == nil || r2 == nil {
		t.Fatalf("ok1=%v ok2=%v; nothing is shared, so every hold succeeds", ok1, ok2)
	}
	if s.Busy(a) {
		t.Fatal("never busy")
	}
	if exists(s.Home(a)) {
		t.Fatal("no claim, no home created")
	}
	r1()
	r2()
}

func TestStoreHoldBusy_ClaimForOwners(t *testing.T) {
	fake.Registry(t)
	rota.Register(owner{Provider: fake.New("t-own")})
	s, _ := seed(t, `{"accounts":[{"id":1,"provider":"t-own","token":{"accessToken":"a"}}]}`)
	a := s.Find(1)
	if s.Busy(a) {
		t.Fatal("nothing is running yet")
	}
	release, ok := s.Hold(a)
	if !ok {
		t.Fatal("first hold must succeed")
	}
	if r2, ok2 := s.Hold(a); ok2 {
		r2()
		t.Fatal("a second hold must be refused while the first stands")
	}
	if !s.Busy(a) {
		t.Fatal("held is busy")
	}
	release()
	if s.Busy(a) {
		t.Fatal("released is not")
	}
	r3, ok3 := s.Hold(a)
	if !ok3 {
		t.Fatal("the claim can be taken again after release")
	}
	r3()
}

func TestStoreHoldBusy_MkdirFailureDegradesToOK(t *testing.T) {
	fake.Registry(t)
	rota.Register(owner{Provider: fake.New("t-own")})
	// A regular file where the home root should be: no home can be made.
	root := filepath.Join(t.TempDir(), "root")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &memBackend{home: root, blob: []byte(`{"accounts":[{"id":1,"provider":"t-own","token":{"accessToken":"a"}}]}`)}
	s := open(t, b)
	a := s.Find(1)
	release, ok := s.Hold(a)
	if !ok || release == nil {
		t.Fatalf("ok=%v; nowhere to lock is not a reason to refuse", ok)
	}
	release()
	if s.Busy(a) {
		t.Fatal("not claimed, so not busy")
	}
}

// --- HideFromAgents, HostEnv ---------------------------------------------------

func TestHideFromAgents_DedupesAndHostEnvRecomputes(t *testing.T) {
	store.HideFromAgents("T_DUP", "T_DUP", "")
	store.HideFromAgents("T_DUP")
	// Set after registration: HostEnv reads the environment on each call.
	t.Setenv("T_DUP", "1")
	t.Setenv("T_KEEP", "k")
	t.Setenv("ROTA_HOME", "/h")
	env := store.HostEnv()
	for _, banned := range []string{"T_DUP=", "ROTA_HOME="} {
		if slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, banned) }) {
			t.Fatalf("%s must not survive HostEnv", banned)
		}
	}
	if !slices.Contains(env, "T_KEEP=k") {
		t.Fatal("an unregistered variable passes through")
	}
	t.Setenv("T_KEEP", "k2")
	if env := store.HostEnv(); !slices.Contains(env, "T_KEEP=k2") || slices.Contains(env, "T_KEEP=k") {
		t.Fatal("HostEnv must recompute rather than cache")
	}
}

// --- ids ---------------------------------------------------------------------

func TestStore_IDsAreNeverReused(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-p"))
	b := &memBackend{home: t.TempDir()}
	s := open(t, b)
	if a, _ := login(t, s, "t-p", "c1"); a.ID != 1 {
		t.Fatalf("first id = %d, want 1", a.ID)
	}
	if err := s.Remove(1); err != nil {
		t.Fatal(err)
	}
	a, _ := login(t, s, "t-p", "c2")
	if a.ID != 2 {
		t.Fatalf("id after a removal = %d, want 2: a retired id never comes back", a.ID)
	}
	if s.Home(a) == filepath.Join(b.home, "t-p-1") {
		t.Fatal("nor does the old home")
	}
}
