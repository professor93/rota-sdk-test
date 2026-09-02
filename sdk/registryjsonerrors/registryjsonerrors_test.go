package registryjsonerrors_test

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

func TestNewRegistry_EmptyNoDefault(t *testing.T) {
	fake.Registry(t)
	r := rota.NewRegistry()
	if got := r.Providers(); len(got) != 0 {
		t.Fatalf("a new registry carries %v", got)
	}
	if _, err := r.Lookup(""); !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("empty name with no default: %v", err)
	}
	r.Default = "t-a"
	r.Register(fake.New("t-a"))
	if p, err := r.Lookup(""); err != nil || p.Name() != "t-a" {
		t.Fatalf("empty name after a default: %v %v", p, err)
	}
}

func TestRegistryRegister_ReplacesByNameOnly(t *testing.T) {
	fake.Registry(t)
	n := len(rota.Providers())
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(context.Context, string) (*rota.Quota, error) { return &rota.Quota{}, nil }})
	if !rota.Metered("t-m") {
		t.Fatal("the meter did not register")
	}
	rota.Register(fake.New("t-m"))
	if rota.Metered("t-m") {
		t.Fatal("the replacement kept the old provider's abilities")
	}
	if got := len(rota.Providers()); got != n+1 {
		t.Fatalf("%d providers, want %d: one entry per name", got, n+1)
	}
}

func TestRegistryProviders_Sorted(t *testing.T) {
	fake.Registry(t)
	r := rota.NewRegistry()
	for _, name := range []string{"t-c", "t-a", "t-b"} {
		r.Register(fake.New(name))
	}
	if got := r.Providers(); !slices.Equal(got, []string{"t-a", "t-b", "t-c"}) {
		t.Fatalf("%v", got)
	}
}

func TestLookup_EmptyUsesDefaultProviderNotRegistryDefault(t *testing.T) {
	reg := fake.Registry(t)
	fake.Restore(t, &rota.DefaultProvider, "t-x")
	rota.Register(fake.New("t-x"))
	if p, err := rota.Lookup(""); err != nil || p.Name() != "t-x" {
		t.Fatalf("package Lookup(\"\"): %v %v, want t-x", p, err)
	}
	if reg.Default != "claude" {
		t.Fatalf("the registry's own default moved to %q", reg.Default)
	}
	if p, err := reg.Lookup(""); err != nil || p.Name() != "claude" {
		t.Fatalf("registry Lookup(\"\"): %v %v, want claude", p, err)
	}
}

func TestLookup_UnknownListsKnown(t *testing.T) {
	_, err := rota.Lookup("zzz")
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{`"zzz"`, "known:", strings.Join(rota.Providers(), ", ")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q misses %q", err, want)
		}
	}
}

func TestProviders_BuiltinsPresent(t *testing.T) {
	got := rota.Providers()
	for _, want := range []string{"claude", "codex", "grok", "kimi"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q missing from %v", want, got)
		}
	}
}

func TestEncode_NilSliceIsNull(t *testing.T) {
	raw, err := rota.Encode(struct {
		A []int
		M map[string]int
	}{})
	if err != nil || string(raw) != `{"A":null,"M":null}` {
		t.Fatalf("%s %v", raw, err)
	}
	// an empty but non-nil slice still writes []: that difference is what
	// tells a set field from an unset one
	raw, err = rota.Encode(struct{ A []int }{A: []int{}})
	if err != nil || string(raw) != `{"A":[]}` {
		t.Fatalf("%s %v", raw, err)
	}
}

func TestEncodeIndent_TwoSpaces(t *testing.T) {
	raw, err := rota.EncodeIndent(map[string]int{"a": 1})
	if err != nil || string(raw) != "{\n  \"a\": 1\n}" {
		t.Fatalf("%q %v", raw, err)
	}
}

func TestEncodeTo_AppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	if err := rota.EncodeTo(&buf, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "{\"a\":1}\n" {
		t.Fatalf("%q", buf.String())
	}
}

// lenientDoc repeats a name in two spellings and carries one invalid byte.
var lenientDoc = "{\"Access_Token\":\"a\",\"access_token\":\"b\",\"bad\":\"\xff\"}"

type lenientTarget struct {
	Access string `json:"access_token"`
	Bad    string `json:"bad"`
}

func TestUnmarshalLenient_CaseInsensitiveDuplicatesAndBadUTF8(t *testing.T) {
	var v lenientTarget
	if err := rota.UnmarshalLenient([]byte(lenientDoc), &v); err != nil {
		t.Fatal(err)
	}
	if v.Access != "b" {
		t.Fatalf("access %q, want the last spelling to win", v.Access)
	}
	if !utf8.ValidString(v.Bad) {
		t.Fatalf("bad %q was not repaired", v.Bad)
	}
}

func TestDecodeLenient_Reader(t *testing.T) {
	var v lenientTarget
	if err := rota.DecodeLenient(strings.NewReader(lenientDoc), &v); err != nil {
		t.Fatal(err)
	}
	if v.Access != "b" || !utf8.ValidString(v.Bad) {
		t.Fatalf("%+v", v)
	}
}

func TestLenientOptions_DecodesWithJSONv2(t *testing.T) {
	data := []byte(`{"ACCESS_TOKEN":"a"}`)
	var strict, lenient lenientTarget
	if err := jsonv2.Unmarshal(data, &strict); err != nil || strict.Access != "" {
		t.Fatalf("strict: %+v %v, want the name unmatched", strict, err)
	}
	if err := jsonv2.Unmarshal(data, &lenient, rota.LenientOptions()); err != nil || lenient.Access != "a" {
		t.Fatalf("lenient: %+v %v", lenient, err)
	}
}

func TestAccount_WireShapeIsExact(t *testing.T) {
	a := rota.Account{ID: 2, Provider: "claude", Email: "a@b.c", Order: 1, Token: rota.Token{Access: "tok", ExpiresAt: 1700000000000}}
	raw, err := rota.Encode(a)
	want := `{"id":2,"provider":"claude","email":"a@b.c","token":{"accessToken":"tok","expiresAt":1700000000000},"order":1}`
	if err != nil || string(raw) != want {
		t.Fatalf("\n got %s\nwant %s\n%v", raw, want, err)
	}
}

func TestResult_WireShapeIsExact(t *testing.T) {
	raw, err := rota.Encode(rota.Result{Account: 1, Provider: "claude", Result: "hi"})
	want := `{"account":1,"provider":"claude","model":"","effort":"","result":"hi","is_error":false,"exit_code":0}`
	if err != nil || string(raw) != want {
		t.Fatalf("\n got %s\nwant %s\n%v", raw, want, err)
	}
}

func TestSentinels_AreDistinct(t *testing.T) {
	all := []error{
		rota.ErrInvalidRequest, rota.ErrDangerous, rota.ErrOutsideRoots, rota.ErrUnsupported, rota.ErrReauth,
		rota.ErrDeadToken, rota.ErrAuthPending, rota.ErrNoLogin, rota.ErrNoAccount, rota.ErrBusy,
	}
	for i, a := range all {
		for j, b := range all {
			if errors.Is(a, b) != (i == j) {
				t.Errorf("errors.Is(%v, %v) = %v", a, b, i != j)
			}
		}
	}
}

func TestInvalid_IsInvalidRequestWithoutPrefix(t *testing.T) {
	err := rota.Invalid("model %q is unknown", "x")
	if !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("%v", err)
	}
	if err.Error() != `model "x" is unknown` {
		t.Fatalf("%q", err)
	}
}

func TestWrapNoAccount_IsNoAccountNamingID(t *testing.T) {
	err := rota.WrapNoAccount(7)
	if !errors.Is(err, rota.ErrNoAccount) || !strings.Contains(err.Error(), "7") {
		t.Fatalf("%v", err)
	}
}

func TestWrapNoLogin_IsNoLoginNamingID(t *testing.T) {
	err := rota.WrapNoLogin("ab12cd")
	if !errors.Is(err, rota.ErrNoLogin) || !strings.Contains(err.Error(), "ab12cd") {
		t.Fatalf("%v", err)
	}
}

func TestWrapReauth_IsReauthNamingAccount(t *testing.T) {
	a := &rota.Account{ID: 3, Provider: "claude", Email: "a@b.c"}
	err := rota.WrapReauth(a)
	if !errors.Is(err, rota.ErrReauth) {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"log in again", a.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q misses %q", err, want)
		}
	}
}

func TestWrapNoBinary_IsUnsupportedNotInPath(t *testing.T) {
	err := rota.WrapNoBinary("t-cli", errors.New("boom"))
	if !errors.Is(err, rota.ErrUnsupported) {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"t-cli", "not found in PATH", "boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q misses %q", err, want)
		}
	}
}

func TestHTTPError_MessageTrimsAndTruncates(t *testing.T) {
	he := &rota.HTTPError{Status: 500, Body: "  " + strings.Repeat("x", 400) + "  "}
	// the cut keeps 300 characters and marks the cut with an ellipsis
	if got, want := he.Error(), "http 500: "+strings.Repeat("x", 300)+"..."; got != want {
		t.Fatalf("len %d: %q", len(got), got)
	}
	if got := (&rota.HTTPError{Status: 429, Body: " boom "}).Error(); got != "http 429: boom" {
		t.Fatalf("%q", got)
	}
}

func TestOAuthError_KeepsCode(t *testing.T) {
	s := fake.NewServer(t)
	s.Claude(t)
	s.Handle("/token", func(*http.Request, map[string]any) (int, any) {
		return 400, fake.OAuthReject("access_denied", "")
	})
	l, err := rota.Begin(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Complete(context.Background(), "C1")
	var oe *rota.OAuthError
	if !errors.As(err, &oe) {
		t.Fatalf("%v is no OAuthError", err)
	}
	if oe.Code != "access_denied" || oe.Description != "" || oe.Error() == "" {
		t.Fatalf("%+v %q", oe, oe.Error())
	}
	if errors.Is(err, rota.ErrAuthPending) || errors.Is(err, rota.ErrDeadToken) {
		t.Fatalf("a denied code grant became a sentinel: %v", err)
	}
	if s.Hits("/token") != 1 {
		t.Fatalf("%d token requests", s.Hits("/token"))
	}
}
