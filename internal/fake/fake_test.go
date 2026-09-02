package fake_test

import (
	"context"
	"net/http"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// The fakes must themselves hold up before anything is proven with them.

func TestRegistryIsScopedToTheTest(t *testing.T) {
	before := rota.Providers()
	t.Run("inner", func(t *testing.T) {
		fake.Registry(t)
		rota.Register(fake.New("t-scoped"))
		if _, err := rota.Lookup("t-scoped"); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := rota.Lookup("t-scoped"); err == nil {
		t.Fatal("a fake registered inside a test must be gone after it")
	}
	if len(rota.Providers()) != len(before) {
		t.Fatalf("builtins changed: %v -> %v", before, rota.Providers())
	}
}

func TestBareProviderLoginRoundTrip(t *testing.T) {
	fake.Registry(t)
	p := fake.New("t-bare")
	rota.Register(p)
	l, err := rota.Begin(context.Background(), "t-bare")
	if err != nil || l.URL != "https://fake/auth" || l.Kind != "code" {
		t.Fatalf("%+v %v", l, err)
	}
	tok, err := l.Complete(context.Background(), " code1 ")
	if err != nil || tok.Access != "code1" || tok.Refresh != "r-code1" {
		t.Fatalf("%+v %v", tok, err)
	}
	if calls := p.Calls(); len(calls) != 2 || calls[1] != "complete:code1" {
		t.Fatalf("%v", calls)
	}
}

func TestServerDecodesJSONAndForms(t *testing.T) {
	s := fake.NewServer(t)
	s.Handle("/echo", func(r *http.Request, body map[string]any) (int, any) { return 200, body })
	s.Claude(t)
	if rota.ClaudeEndpoints.Token != s.URL+"/token" {
		t.Fatalf("endpoint not pointed at the server: %s", rota.ClaudeEndpoints.Token)
	}
	resp, err := rota.HTTPClient.Post(s.URL+"/echo", "application/x-www-form-urlencoded", stringsReader("grant_type=refresh_token&code=c"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp)
	}
	resp.Body.Close()
	if got := s.Requests(); len(got) != 1 || got[0].Body["grant_type"] != "refresh_token" || got[0].Body["code"] != "c" {
		t.Fatalf("%+v", got)
	}
	if s.Hits("/missing") != 0 || s.Hits("/echo") != 1 {
		t.Fatalf("hits: %d %d", s.Hits("/missing"), s.Hits("/echo"))
	}
}

func TestJWTCarriesItsClaims(t *testing.T) {
	tok := fake.JWT(fake.Reply{"sub": "s1", "exp": 4102444800})
	if len(tok) < 20 || tok[len(tok)-4:] != ".sig" {
		t.Fatalf("%q", tok)
	}
}
