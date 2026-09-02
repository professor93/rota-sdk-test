package login_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// begin registers p under its own name in a scoped registry and starts a
// login with it, so each test states only what differs.
func begin(t *testing.T, p rota.Provider) (*rota.Login, error) {
	t.Helper()
	fake.Registry(t)
	rota.Register(p)
	return rota.Begin(context.Background(), p.Name())
}

func mustBegin(t *testing.T, p rota.Provider) *rota.Login {
	t.Helper()
	l, err := begin(t, p)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestBegin_EmptyProviderUsesDefaultProvider(t *testing.T) {
	fake.Registry(t)
	fake.Restore(t, &rota.DefaultProvider, "t-d")
	rota.Register(fake.New("t-d"))
	l, err := rota.Begin(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Provider != "t-d" {
		t.Fatalf("provider = %q, want t-d", l.Provider)
	}
}

func TestBegin_UnknownProviderIsInvalidRequest(t *testing.T) {
	fake.Registry(t)
	_, err := rota.Begin(context.Background(), "t-nope")
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	// The known list is sorted, so the builtins appear in this order.
	msg := err.Error()
	last := -1
	for _, name := range []string{"claude", "codex", "grok", "kimi"} {
		i := strings.Index(msg, name)
		if i <= last {
			t.Fatalf("%q missing or out of order in %q", name, msg)
		}
		last = i
	}
}

func TestBegin_IDIsSixHexChars(t *testing.T) {
	l := mustBegin(t, fake.New("t-id"))
	if len(l.ID) != 6 {
		t.Fatalf("id %q has %d chars, want 6", l.ID, len(l.ID))
	}
	for _, r := range l.ID {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("id %q has a non-hex rune %q", l.ID, r)
		}
	}
}

func TestBegin_KindDefaultsToCode(t *testing.T) {
	l := mustBegin(t, fake.New("t-kind"))
	if l.Kind != "code" {
		t.Fatalf("kind = %q, want code", l.Kind)
	}
}

func TestBegin_KindComesFromState(t *testing.T) {
	p := fake.New("t-dev")
	p.Kind = "device"
	l := mustBegin(t, p)
	if l.Kind != "device" {
		t.Fatalf("kind = %q, want device", l.Kind)
	}
}

func TestBegin_DelegatedFollowsDelegatorNotKind(t *testing.T) {
	fake.Registry(t)
	bare := fake.New("t-key")
	bare.Kind = "apikey"
	rota.Register(bare)
	del := fake.New("t-del")
	del.Kind = "apikey"
	rota.Register(fake.Delegator{Provider: del})

	l, err := rota.Begin(context.Background(), "t-key")
	if err != nil || l.Delegated || l.Kind != "apikey" {
		t.Fatalf("bare: %+v %v", l, err)
	}
	l, err = rota.Begin(context.Background(), "t-del")
	if err != nil || !l.Delegated || l.Kind != "apikey" {
		t.Fatalf("delegator: %+v %v", l, err)
	}
}

func TestBegin_BuiltinKinds(t *testing.T) {
	// Endpoints point at a local server so no real URL is assembled; Begin
	// itself never makes a request.
	s := fake.NewServer(t)
	s.Claude(t)
	s.Codex(t)
	for _, tc := range []struct {
		provider, kind string
		delegated      bool
	}{
		{"claude", "code", false},
		{"codex", "code", false},
		{"grok", "apikey", true},
		{"kimi", "delegated", true},
	} {
		l, err := rota.Begin(context.Background(), tc.provider)
		if err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		if l.Kind != tc.kind || l.Delegated != tc.delegated || l.Provider != tc.provider {
			t.Fatalf("%s: kind=%q delegated=%v provider=%q", tc.provider, l.Kind, l.Delegated, l.Provider)
		}
		if l.URL == "" {
			t.Fatalf("%s: empty URL", tc.provider)
		}
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("Begin made %d requests", n)
	}
}

func TestBegin_CreatedAtFollowsInjectedNow(t *testing.T) {
	fake.Clock(t, time.Unix(1_700_000_000, 0))
	l := mustBegin(t, fake.New("t-now"))
	if l.CreatedAt != 1_700_000_000_000 {
		t.Fatalf("createdAt = %d, want 1700000000000", l.CreatedAt)
	}
}

func TestBegin_ProviderErrorPropagates(t *testing.T) {
	sentinel := errors.New("begin broke")
	p := fake.New("t-err")
	p.BeginErr = sentinel
	l, err := begin(t, p)
	if !errors.Is(err, sentinel) || l != nil {
		t.Fatalf("got %+v, %v; want the sentinel", l, err)
	}
}

func TestComplete_TrimsCode(t *testing.T) {
	p := fake.New("t-trim")
	l := mustBegin(t, p)
	tok, err := l.Complete(context.Background(), "  c1\n")
	if err != nil || tok.Access != "c1" {
		t.Fatalf("%+v %v", tok, err)
	}
	calls := p.Calls()
	if len(calls) == 0 || calls[len(calls)-1] != "complete:c1" {
		t.Fatalf("calls = %v, want a trailing complete:c1", calls)
	}
}

func TestComplete_UnknownProviderInLoginFails(t *testing.T) {
	l := mustBegin(t, fake.New("t-gone"))
	l.Provider = "t-nope"
	tok, err := l.Complete(context.Background(), "c1")
	if !errors.Is(err, rota.ErrInvalidRequest) || tok != nil {
		t.Fatalf("got %+v, %v; want the lookup error", tok, err)
	}
}

func TestComplete_ProviderErrorPropagatesUnchanged(t *testing.T) {
	p := fake.New("t-pending")
	p.CompleteErr = rota.ErrAuthPending
	l := mustBegin(t, p)
	_, err := l.Complete(context.Background(), "")
	if !errors.Is(err, rota.ErrAuthPending) {
		t.Fatalf("err = %v, want ErrAuthPending", err)
	}
	// Unchanged means the very value, not a wrapper around it.
	if err != rota.ErrAuthPending {
		t.Fatalf("err was wrapped: %#v", err)
	}
}

func TestComplete_NoAccessTokenIsInvalidRequest(t *testing.T) {
	p := fake.New("t-empty")
	p.Token = func(string) *rota.Token { return &rota.Token{} }
	l := mustBegin(t, p)
	tok, err := l.Complete(context.Background(), "c1")
	if !errors.Is(err, rota.ErrInvalidRequest) || tok != nil {
		t.Fatalf("got %+v, %v; want ErrInvalidRequest", tok, err)
	}
	if !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("message %q lacks 'no access token'", err)
	}
}

func TestComplete_DelegatedTokenNeedsNoAccess(t *testing.T) {
	p := fake.New("t-deleg")
	p.Token = func(string) *rota.Token { return &rota.Token{Delegated: true} }
	l := mustBegin(t, p)
	tok, err := l.Complete(context.Background(), "")
	if err != nil || tok == nil || !tok.Delegated || tok.Access != "" {
		t.Fatalf("%+v %v", tok, err)
	}
}

func TestComplete_IdentifiesWhenTokenHasNoIdentity(t *testing.T) {
	var seen string
	p := fake.Identifier{Provider: fake.New("t-ident"), Fn: func(_ context.Context, access string) (*rota.Identity, error) {
		seen = access
		return &rota.Identity{UUID: "u"}, nil
	}}
	l := mustBegin(t, p)
	tok, err := l.Complete(context.Background(), "c1")
	if err != nil || tok.Identity == nil || tok.Identity.UUID != "u" {
		t.Fatalf("%+v %v", tok, err)
	}
	if seen != "c1" {
		t.Fatalf("Identify got access %q, want the new token c1", seen)
	}
}

func TestComplete_IdentifyErrorIsDiscarded(t *testing.T) {
	p := fake.Identifier{Provider: fake.New("t-noident"), Fn: func(context.Context, string) (*rota.Identity, error) {
		return nil, errors.New("profile down")
	}}
	l := mustBegin(t, p)
	tok, err := l.Complete(context.Background(), "c1")
	if err != nil || tok == nil || tok.Access != "c1" {
		t.Fatalf("%+v %v", tok, err)
	}
	if tok.Identity != nil {
		t.Fatalf("identity = %+v, want nil", tok.Identity)
	}
}

func TestComplete_TokenIdentityWinsOverIdentifier(t *testing.T) {
	bare := fake.New("t-has-ident")
	bare.Identity = &rota.Identity{UUID: "from-token"}
	p := fake.Identifier{Provider: bare, Fn: func(context.Context, string) (*rota.Identity, error) {
		t.Fatal("Identify must not run when the token already names the account")
		return nil, nil
	}}
	l := mustBegin(t, p)
	tok, err := l.Complete(context.Background(), "c1")
	if err != nil || tok.Identity == nil || tok.Identity.UUID != "from-token" {
		t.Fatalf("%+v %v", tok, err)
	}
}
