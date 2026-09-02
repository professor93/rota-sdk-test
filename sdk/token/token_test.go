package token_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// at is the instant every clock-driven test pins.
var at = time.Unix(1_700_000_000, 0)

// expired is an account whose token is already past its expiry at `at`.
func expired(provider, refresh string) *rota.Account {
	return rota.NewAccount(1, provider, &rota.Token{Access: "A1", Refresh: refresh, ExpiresAt: 1})
}

// refresher registers a Refresher fake named name whose Refresh is fn.
func refresher(t *testing.T, name string, fn func(context.Context, *rota.Account) (*rota.Token, error)) {
	t.Helper()
	fake.Registry(t)
	rota.Register(fake.Refresher{Provider: fake.New(name), Fn: fn})
}

func TestExpired_ZeroExpiryNever(t *testing.T) {
	fake.Clock(t, at)
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A1", ExpiresAt: 0})
	if a.Expired() {
		t.Fatal("a token with no expiry must never expire")
	}
}

func TestExpired_DelegatedNever(t *testing.T) {
	fake.Clock(t, at)
	a := rota.NewAccount(1, "t-x", &rota.Token{Delegated: true, ExpiresAt: 1})
	if !a.Delegated || a.Expired() {
		t.Fatalf("delegated=%v expired=%v", a.Delegated, a.Expired())
	}
}

func TestExpired_UsesBufferAndNow(t *testing.T) {
	advance := fake.Clock(t, at)
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A1"})

	a.Token.ExpiresAt = at.Add(4 * time.Minute).UnixMilli()
	if !a.Expired() {
		t.Fatal("4 min left is inside the 5 min default buffer")
	}
	a.Token.ExpiresAt = at.Add(6 * time.Minute).UnixMilli()
	if a.Expired() {
		t.Fatal("6 min left is outside the 5 min default buffer")
	}

	fake.Restore(t, &rota.ExpiryBuffer, time.Minute)
	a.Token.ExpiresAt = at.Add(4 * time.Minute).UnixMilli()
	if a.Expired() {
		t.Fatal("4 min left is outside a 1 min buffer")
	}
	// now + buffer == expiry is already expired: the comparison is >=.
	advance(3 * time.Minute)
	if !a.Expired() {
		t.Fatal("reaching the buffer boundary counts as expired")
	}
}

func TestNowMS_FollowsInjectedNow(t *testing.T) {
	advance := fake.Clock(t, at)
	if got := rota.NowMS(); got != at.UnixMilli() {
		t.Fatalf("NowMS = %d, want %d", got, at.UnixMilli())
	}
	advance(1500 * time.Millisecond)
	if got := rota.NowMS(); got != at.UnixMilli()+1500 {
		t.Fatalf("NowMS after advance = %d, want %d", got, at.UnixMilli()+1500)
	}
}

func TestRefresh_FreshTokenAsksNobody(t *testing.T) {
	fake.Clock(t, at)
	refresher(t, "t-fresh", func(context.Context, *rota.Account) (*rota.Token, error) {
		t.Fatal("a fresh token must not be refreshed")
		return nil, nil
	})
	a := rota.NewAccount(1, "t-fresh", &rota.Token{Access: "A1", Refresh: "R1", ExpiresAt: at.Add(24 * time.Hour).UnixMilli()})
	changed, err := rota.Refresh(context.Background(), a)
	if changed || err != nil {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestRefresh_UnknownProviderFails(t *testing.T) {
	fake.Clock(t, at)
	fake.Registry(t)
	a := expired("t-nope", "R1")
	changed, err := rota.Refresh(context.Background(), a)
	if changed || !errors.Is(err, rota.ErrInvalidRequest) || a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestRefresh_NonRefresherIsReauthAndDead(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	fake.Clock(t, at)
	a := expired("t-plain", "R1")
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestRefresh_NoRefreshTokenIsReauthAndDead(t *testing.T) {
	fake.Clock(t, at)
	refresher(t, "t-noref", func(context.Context, *rota.Account) (*rota.Token, error) {
		t.Fatal("nothing to refresh with, so the provider must not be asked")
		return nil, nil
	})
	a := expired("t-noref", "")
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestRefresh_DeadTokenBecomesReauth(t *testing.T) {
	fake.Clock(t, at)
	refresher(t, "t-dead", func(context.Context, *rota.Account) (*rota.Token, error) {
		return nil, rota.ErrDeadToken
	})
	a := expired("t-dead", "R1")
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || !errors.Is(err, rota.ErrReauth) || !a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
}

func TestRefresh_TransientErrorLeavesAccount(t *testing.T) {
	fake.Clock(t, at)
	sentinel := errors.New("network blip")
	refresher(t, "t-blip", func(context.Context, *rota.Account) (*rota.Token, error) {
		return nil, sentinel
	})
	a := expired("t-blip", "R1")
	before := a.Token
	changed, err := rota.Refresh(context.Background(), a)
	if changed || !errors.Is(err, sentinel) || errors.Is(err, rota.ErrReauth) || a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
	if !reflect.DeepEqual(a.Token, before) {
		t.Fatalf("token changed: %+v -> %+v", before, a.Token)
	}
}

func TestRefresh_EmptyAccessIsPlainError(t *testing.T) {
	fake.Clock(t, at)
	refresher(t, "t-blank", func(context.Context, *rota.Account) (*rota.Token, error) {
		return &rota.Token{}, nil
	})
	a := expired("t-blank", "R1")
	changed, err := rota.Refresh(context.Background(), a)
	if changed || err == nil || a.Dead {
		t.Fatalf("changed=%v err=%v dead=%v", changed, err, a.Dead)
	}
	if errors.Is(err, rota.ErrReauth) || errors.Is(err, rota.ErrDeadToken) {
		t.Fatalf("err %v carries a sentinel it should not", err)
	}
	if a.Token.Access != "A1" {
		t.Fatalf("access = %q, want the old A1", a.Token.Access)
	}
}

func TestRefresh_SuccessAppliesAndClearsDead(t *testing.T) {
	fake.Clock(t, at)
	refresher(t, "t-ok", func(context.Context, *rota.Account) (*rota.Token, error) {
		return &rota.Token{Access: "A2"}, nil
	})
	a := expired("t-ok", "R1")
	a.Dead = true
	changed, err := rota.Refresh(context.Background(), a)
	if !changed || err != nil {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if a.Token.Access != "A2" || a.Token.Refresh != "R1" || a.Dead {
		t.Fatalf("%+v dead=%v", a.Token, a.Dead)
	}
}

func TestApply_KeepsRefreshExpiryScopesWhenAbsent(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A1", Refresh: "R1", ExpiresAt: 9, Scopes: []string{"a"}})
	a.Apply(&rota.Token{Access: "A2"})
	want := rota.Token{Access: "A2", Refresh: "R1", ExpiresAt: 9, Scopes: []string{"a"}}
	if !reflect.DeepEqual(a.Token, want) {
		t.Fatalf("got %+v, want %+v", a.Token, want)
	}
}

func TestApply_ReplacesWhenPresent(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A1", Refresh: "R1", ExpiresAt: 9, Scopes: []string{"a"}})
	a.Apply(&rota.Token{Access: "A2", Refresh: "R2", ExpiresAt: 10, Scopes: []string{"b"}})
	want := rota.Token{Access: "A2", Refresh: "R2", ExpiresAt: 10, Scopes: []string{"b"}}
	if !reflect.DeepEqual(a.Token, want) {
		t.Fatalf("got %+v, want %+v", a.Token, want)
	}
}

func TestApply_FoldsIdentityAndMergesExtraAndSetsDelegated(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A1", Extra: map[string]string{"keep": "1", "over": "old"}})
	a.Apply(&rota.Token{
		Access:    "A2",
		Delegated: true,
		Identity:  &rota.Identity{UUID: "u1", Email: "e@x", Org: "o1"},
		Extra:     map[string]string{"over": "new", "add": "2"},
	})
	if a.UUID != "u1" || a.Email != "e@x" || a.Org != "o1" {
		t.Fatalf("identity not folded: uuid=%q email=%q org=%q", a.UUID, a.Email, a.Org)
	}
	wantExtra := map[string]string{"keep": "1", "over": "new", "add": "2"}
	if !reflect.DeepEqual(a.Extra, wantExtra) {
		t.Fatalf("extra = %v, want %v", a.Extra, wantExtra)
	}
	if !a.Delegated {
		t.Fatal("delegated should follow the token to true")
	}
	// A partial identity only overwrites what it names.
	a.Apply(&rota.Token{Access: "A3", Identity: &rota.Identity{UUID: "u2"}})
	if a.UUID != "u2" || a.Email != "e@x" || a.Org != "o1" {
		t.Fatalf("partial identity: uuid=%q email=%q org=%q", a.UUID, a.Email, a.Org)
	}
	if a.Delegated {
		t.Fatal("delegated should follow the token back to false")
	}
}

func TestApply_ClearsDead(t *testing.T) {
	a := rota.NewAccount(1, "t-x", &rota.Token{Access: "A1"})
	a.Dead = true
	a.Apply(&rota.Token{Access: "A2"})
	if a.Dead {
		t.Fatal("apply must clear Dead")
	}
}

func TestToken_RoundTripsThroughEncode(t *testing.T) {
	in := rota.Token{
		Access:    "A1",
		Refresh:   "R1",
		ExpiresAt: 1_700_000_000_000,
		Scopes:    []string{"a", "b"},
		Delegated: true,
		Identity:  &rota.Identity{UUID: "u1", Email: "e@x", Org: "o1"},
		Extra:     map[string]string{"id_token": "jwt", "device": "d1"},
	}
	raw, err := rota.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	var out rota.Token
	if err := rota.UnmarshalLenient(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip lost something:\n in: %+v\nout: %+v\nraw: %s", in, out, raw)
	}
}

func TestWhen_NeverFailsToDecode(t *testing.T) {
	for _, in := range []string{`""`, `"yesterday"`, `null`, `42`} {
		doc := []byte(`{"name":"5h","percent":1,"resetsAt":` + in + `}`)
		var w rota.Window
		if err := rota.UnmarshalLenient(doc, &w); err != nil {
			t.Fatalf("%s: lenient decode failed: %v", in, err)
		}
		if !w.ResetsAt.IsZero() || w.Name != "5h" || w.Percent != 1 {
			t.Fatalf("%s: got %+v", in, w)
		}
		// The v1 decoder a consumer may still use must agree.
		var v1 rota.Window
		if err := json.Unmarshal(doc, &v1); err != nil {
			t.Fatalf("%s: encoding/json decode failed: %v", in, err)
		}
		if !v1.ResetsAt.IsZero() {
			t.Fatalf("%s: encoding/json got %v", in, v1.ResetsAt)
		}
	}
}

func TestWhen_ParsesRFC3339WithOffsetToUTC(t *testing.T) {
	var w rota.Window
	if err := rota.UnmarshalLenient([]byte(`{"name":"5h","resetsAt":"2099-01-02T03:04:05.5+02:00"}`), &w); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2099, 1, 2, 1, 4, 5, 500_000_000, time.UTC)
	if !w.ResetsAt.Equal(want) {
		t.Fatalf("got %v, want %v", w.ResetsAt.Time, want)
	}
}

func TestWindow_MarshalsMinimalShape(t *testing.T) {
	raw, err := rota.Encode(rota.Window{Name: "5h"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"name":"5h","percent":0}` {
		t.Fatalf("got %s", raw)
	}
	if strings.Contains(string(raw), "resetsAt") {
		t.Fatalf("zero resetsAt must be omitted: %s", raw)
	}
}
