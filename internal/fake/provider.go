package fake

import (
	"context"
	"io/fs"
	"strings"
	"sync"

	rota "github.com/professor93/rota/lib"
)

// Provider implements exactly the four required methods, nothing optional.
// Every optional ability is a wrapper below, so a test composes precisely
// the provider it needs: fake.Meter{Provider: fake.Refresher{Provider: p}}.
//
// Defaults: Begin hands out URL with state {"verifier": "v"} and the Kind
// when set; Complete refuses the code "bad" and a wrong verifier, otherwise
// returns Token{Access: code, Refresh: "r-" + code, Identity: Identity};
// Launch returns Command{Bin: "t-cli", BaseEnv: BaseEnv} with the network
// drops every real provider has.
type Provider struct {
	Name_       string
	Kind        string
	URL         string
	BeginErr    error
	CompleteErr error
	Identity    *rota.Identity
	Token       func(code string) *rota.Token
	Command     func(a *rota.Account, home string) (*rota.Command, error)
	BaseEnv     []string

	mu    sync.Mutex
	calls []string
}

// New is a bare provider named name. Names should start with "t-" so they
// never collide with a builtin.
func New(name string) *Provider {
	return &Provider{Name_: name, URL: "https://fake/auth", BaseEnv: []string{"PATH=/usr/bin:/bin"}}
}

// Calls is every method invoked so far, in order, for tests that prove a
// call did or did not happen.
func (p *Provider) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func (p *Provider) record(s string) {
	p.mu.Lock()
	p.calls = append(p.calls, s)
	p.mu.Unlock()
}

func (p *Provider) Name() string { return p.Name_ }

func (p *Provider) Begin(ctx context.Context) (string, map[string]string, error) {
	p.record("begin")
	if p.BeginErr != nil {
		return "", nil, p.BeginErr
	}
	state := map[string]string{"verifier": "v"}
	if p.Kind != "" {
		state["kind"] = p.Kind
	}
	return p.URL, state, nil
}

func (p *Provider) Complete(ctx context.Context, code string, state map[string]string) (*rota.Token, error) {
	p.record("complete:" + code)
	if p.CompleteErr != nil {
		return nil, p.CompleteErr
	}
	if code == "bad" || state["verifier"] != "v" {
		return nil, rota.Invalid("code rejected")
	}
	if p.Token != nil {
		return p.Token(code), nil
	}
	return &rota.Token{Access: code, Refresh: "r-" + code, Identity: p.Identity, Extra: map[string]string{"seen": code}}, nil
}

func (p *Provider) Launch(a *rota.Account, home string) (*rota.Command, error) {
	p.record("launch")
	if p.Command != nil {
		return p.Command(a, home)
	}
	return &rota.Command{
		Bin:     "t-cli",
		Env:     []string{"T_TOKEN=" + a.Token.Access},
		Drop:    append([]string{"T_OTHER"}, rota.NetworkRedirecting()...),
		BaseEnv: p.BaseEnv,
	}, nil
}

// The optional abilities. Each embeds rota.Provider, so they stack in any
// order and the four required methods pass straight through.

type Refresher struct {
	rota.Provider
	Fn func(ctx context.Context, a *rota.Account) (*rota.Token, error)
}

func (r Refresher) Refresh(ctx context.Context, a *rota.Account) (*rota.Token, error) {
	return r.Fn(ctx, a)
}

type Identifier struct {
	rota.Provider
	Fn func(ctx context.Context, access string) (*rota.Identity, error)
}

func (i Identifier) Identify(ctx context.Context, access string) (*rota.Identity, error) {
	return i.Fn(ctx, access)
}

type Meter struct {
	rota.Provider
	Fn func(ctx context.Context, access string) (*rota.Quota, error)
}

func (m Meter) Quota(ctx context.Context, access string) (*rota.Quota, error) {
	return m.Fn(ctx, access)
}

type Catalog struct {
	rota.Provider
	ModelList  []rota.Model
	EffortList []string
	DefModel   string
	DefEffort  string
}

func (c Catalog) Models() []rota.Model             { return c.ModelList }
func (c Catalog) Efforts() []string                { return c.EffortList }
func (c Catalog) Defaults() (model, effort string) { return c.DefModel, c.DefEffort }

type AccountCatalog struct {
	rota.Provider
	Fn func(a *rota.Account, home string) []rota.Model
}

func (c AccountCatalog) ModelsFor(a *rota.Account, home string) []rota.Model { return c.Fn(a, home) }

type Adopter struct {
	rota.Provider
	Fn func(a *rota.Account, home string) error
}

func (a Adopter) Adopt(acct *rota.Account, home string) error { return a.Fn(acct, home) }

type FSAdopter struct {
	rota.Provider
	Fn func(a *rota.Account, fsys fs.FS) error
}

func (a FSAdopter) AdoptFS(acct *rota.Account, fsys fs.FS) error { return a.Fn(acct, fsys) }

type Delegator struct {
	rota.Provider
	Plan rota.LoginPlan
}

func (d Delegator) LoginPlan(a *rota.Account, home string) rota.LoginPlan { return d.Plan }

type Planner struct {
	rota.Provider
	Fn func(ctx context.Context, a *rota.Account, home string) (*rota.Command, []rota.StagedFile, error)
}

func (p Planner) Plan(ctx context.Context, a *rota.Account, home string) (*rota.Command, []rota.StagedFile, error) {
	return p.Fn(ctx, a, home)
}

type Flavored struct {
	rota.Provider
	Name_ string
}

func (f Flavored) Flavor() string { return f.Name_ }

type Floor struct {
	rota.Provider
	Is bool
}

func (f Floor) CatalogIsFloor() bool { return f.Is }

type SignIn struct {
	rota.Provider
	Fn func(a *rota.Account, home string) error
}

func (s SignIn) SignedIn(a *rota.Account, home string) error { return s.Fn(a, home) }

// A wrapper embeds the rota.Provider interface, so Go promotes only the four
// required methods through it: stacking two wrappers keeps the outer ability
// and loses the inner one. The types below are the combinations tests need
// as one concrete value; for any other mix, define a local struct embedding
// *Provider and add the methods.

// FlavoredCatalog is a provider rota treats as one of its flavors, with a
// model and effort catalog: what Spec.Check and Run need to plan an argv.
type FlavoredCatalog struct {
	*Provider
	FlavorName string
	ModelList  []rota.Model
	EffortList []string
	DefModel   string
	DefEffort  string
}

func (c *FlavoredCatalog) Flavor() string                   { return c.FlavorName }
func (c *FlavoredCatalog) Models() []rota.Model             { return c.ModelList }
func (c *FlavoredCatalog) Efforts() []string                { return c.EffortList }
func (c *FlavoredCatalog) Defaults() (model, effort string) { return c.DefModel, c.DefEffort }

// Owning is a provider that owns its credential file, the way codex and
// kimi do: Adopter plus Refresher, each recorded in Calls. A nil AdoptFn
// adopts nothing; a nil RefreshFn hands the same token back.
type Owning struct {
	*Provider
	AdoptFn   func(a *rota.Account, home string) error
	RefreshFn func(ctx context.Context, a *rota.Account) (*rota.Token, error)
}

func (o *Owning) Adopt(a *rota.Account, home string) error {
	o.record("adopt")
	if o.AdoptFn == nil {
		return nil
	}
	return o.AdoptFn(a, home)
}

func (o *Owning) Refresh(ctx context.Context, a *rota.Account) (*rota.Token, error) {
	o.record("refresh")
	if o.RefreshFn == nil {
		t := a.Token
		return &t, nil
	}
	return o.RefreshFn(ctx, a)
}

// Flavor makes p one of rota's flavors with a small catalog: two models,
// t-model-1 (alias one) and t-model-2, efforts low and high, defaults
// t-model-1 and low. Pass "" for a catalog-less flavor like kimi's.
func Flavor(p *Provider, flavor string) *FlavoredCatalog {
	return &FlavoredCatalog{
		Provider:   p,
		FlavorName: flavor,
		ModelList:  []rota.Model{{ID: "t-model-1", Aliases: []string{"one"}}, {ID: "t-model-2"}},
		EffortList: []string{"low", "high"},
		DefModel:   "t-model-1",
		DefEffort:  "low",
	}
}

// Claude is Flavor(p, "claude"): the usual base for Run tests, because the
// claude argv is the simplest to assert on.
func Claude(p *Provider) *FlavoredCatalog { return Flavor(p, "claude") }

// Join is how a fake CLI reports its argv back: one string, so a test can
// assert on "--model t-model-1" without parsing.
func Join(args []string) string { return strings.Join(args, " ") }
