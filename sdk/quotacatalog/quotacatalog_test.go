package quotacatalog_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// The fake wrappers embed the Provider interface, so an inner wrapper's
// optional methods stay hidden from the outside. A provider that must answer
// two optional interfaces at once needs its own type.

// homeCatalog is a Catalog that is also an AccountCatalog.
type homeCatalog struct {
	fake.Catalog
	fn func(a *rota.Account, home string) []rota.Model
}

func (c homeCatalog) ModelsFor(a *rota.Account, home string) []rota.Model { return c.fn(a, home) }

// floorCatalog is a Catalog whose list is only a floor.
type floorCatalog struct{ fake.Catalog }

func (floorCatalog) CatalogIsFloor() bool { return true }

// flavoredCatalog is a Catalog that also names its flavor.
type flavoredCatalog struct {
	fake.Catalog
	flavor string
}

func (f flavoredCatalog) Flavor() string { return f.flavor }

// catalogOver is the same catalog fake.Claude carries, as a value that can
// be embedded.
func catalogOver(p rota.Provider) fake.Catalog {
	return fake.Catalog{
		Provider:   p,
		ModelList:  []rota.Model{{ID: "t-model-1", Aliases: []string{"one"}}, {ID: "t-model-2"}},
		EffortList: []string{"low", "high"},
		DefModel:   "t-model-1",
		DefEffort:  "low",
	}
}

func ids(models []rota.Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func account(provider string) *rota.Account {
	return rota.NewAccount(1, provider, &rota.Token{Access: "tok"})
}

func TestUsage_UnknownProviderFails(t *testing.T) {
	fake.Registry(t)
	q, err := rota.Usage(context.Background(), account("t-nope"))
	if q != nil || !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("quota=%v err=%v", q, err)
	}
}

func TestUsage_NonMeterIsNilNil(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	q, err := rota.Usage(context.Background(), account("t-plain"))
	if q != nil || err != nil {
		t.Fatalf("quota=%v err=%v, want nil nil", q, err)
	}
}

func TestUsage_MeterResultVerbatim(t *testing.T) {
	fake.Registry(t)
	want := &rota.Quota{Windows: []rota.Window{{Name: "5h", Percent: 12.5}}}
	sentinel := errors.New("meter says no")
	var seen string
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(_ context.Context, access string) (*rota.Quota, error) {
		seen = access
		return want, sentinel
	}})
	got, err := rota.Usage(context.Background(), account("t-m"))
	if got != want || err != sentinel {
		t.Fatalf("got %p %v, want the meter's own %p %v", got, err, want, sentinel)
	}
	if seen != "tok" {
		t.Fatalf("meter saw %q, want the account's access token", seen)
	}
}

func TestUsage_NeverCaches(t *testing.T) {
	fake.Registry(t)
	calls := 0
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(context.Context, string) (*rota.Quota, error) {
		calls++
		return &rota.Quota{}, nil
	}})
	a := account("t-m")
	for range 2 {
		if _, err := rota.Usage(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("meter called %d times, want 2", calls)
	}
}

func TestMetered_OnlyClaudeAmongBuiltins(t *testing.T) {
	for name, want := range map[string]bool{"claude": true, "codex": false, "grok": false, "kimi": false} {
		if got := rota.Metered(name); got != want {
			t.Errorf("Metered(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMetered_UnknownFalse(t *testing.T) {
	if rota.Metered("zzz") {
		t.Fatal("an unknown provider reports metered")
	}
}

func TestMetered_FakeMeterTrue(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(context.Context, string) (*rota.Quota, error) { return nil, nil }})
	rota.Register(fake.New("t-plain"))
	if !rota.Metered("t-m") || rota.Metered("t-plain") {
		t.Fatalf("t-m=%v t-plain=%v", rota.Metered("t-m"), rota.Metered("t-plain"))
	}
}

func TestModels_UnknownAndNoCatalogAreNil(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	if got := rota.Models("zzz"); got != nil {
		t.Errorf("unknown provider: %v, want nil", got)
	}
	if got := rota.Models("t-plain"); got != nil {
		t.Errorf("no catalog: %v, want nil", got)
	}
}

func TestModels_BuiltinLists(t *testing.T) {
	type entry struct{ id, alias string }
	want := map[string][]entry{
		"claude": {{"claude-opus-5", "opus"}, {"claude-fable-5", "fable"}, {"claude-sonnet-5", "sonnet"}, {"claude-haiku-4-5-20251001", "haiku"}},
		"codex":  {{"gpt-5.6-sol", ""}, {"gpt-5.6-terra", ""}, {"gpt-5.6-luna", ""}, {"gpt-5.5", ""}, {"gpt-5.2", ""}},
		"grok":   {{"grok-4.6", ""}, {"grok-4.5", ""}},
	}
	for name, list := range want {
		got := rota.Models(name)
		if len(got) != len(list) {
			t.Errorf("%s: %v, want %v", name, ids(got), list)
			continue
		}
		for i, e := range list {
			var aliases []string
			if e.alias != "" {
				aliases = []string{e.alias}
			}
			if got[i].ID != e.id || !slices.Equal(got[i].Aliases, aliases) {
				t.Errorf("%s[%d] = %q %v, want %q %v", name, i, got[i].ID, got[i].Aliases, e.id, aliases)
			}
		}
	}
	if got := rota.Models("kimi"); got != nil {
		t.Errorf("kimi: %v, want nil", got)
	}
}

func TestModels_ReturnsCopies(t *testing.T) {
	first := rota.Models("claude")
	first[0].ID = "mutated"
	if got := rota.Models("claude"); got[0].ID != "claude-opus-5" {
		t.Fatalf("the shipped list changed: %v", ids(got))
	}
}

func TestModelsFor_AccountCatalogOnlyWithNonEmptyHome(t *testing.T) {
	fake.Registry(t)
	var homes []string
	rota.Register(homeCatalog{Catalog: catalogOver(fake.New("t-ac")), fn: func(_ *rota.Account, home string) []rota.Model {
		homes = append(homes, home)
		if home == "" {
			return nil
		}
		return []rota.Model{{ID: "from-" + home}}
	}})
	rota.Register(homeCatalog{Catalog: catalogOver(fake.New("t-none")), fn: func(*rota.Account, string) []rota.Model { return nil }})
	shipped := []string{"t-model-1", "t-model-2"}

	if got := ids(rota.ModelsFor(account("t-ac"), "")); !slices.Equal(got, shipped) {
		t.Errorf("empty home: %v, want %v", got, shipped)
	}
	if len(homes) != 0 {
		t.Errorf("the account catalog was asked with an empty home: %q", homes)
	}
	if got := ids(rota.ModelsFor(account("t-ac"), "h")); !slices.Equal(got, []string{"from-h"}) {
		t.Errorf("home h: %v, want [from-h]", got)
	}
	if got := ids(rota.ModelsFor(account("t-none"), "h")); !slices.Equal(got, shipped) {
		t.Errorf("nil from the account catalog: %v, want %v", got, shipped)
	}
}

func TestEfforts_Builtins(t *testing.T) {
	want := map[string][]string{
		"claude": {"low", "medium", "high", "xhigh", "max"},
		"codex":  {"low", "medium", "high", "xhigh", "max", "ultra"},
		"grok":   {"low", "medium", "high", "xhigh"},
		"kimi":   nil,
	}
	for name, list := range want {
		if got := rota.Efforts(name); !slices.Equal(got, list) || (list == nil && got != nil) {
			t.Errorf("Efforts(%q) = %v, want %v", name, got, list)
		}
	}
}

func TestDefaults_Builtins(t *testing.T) {
	want := map[string][2]string{
		"claude": {"claude-opus-5", "high"},
		"codex":  {"", "medium"},
		"grok":   {"grok-4.6", "high"},
		"kimi":   {"", ""},
	}
	for name, pair := range want {
		if m, e := rota.Defaults(name); m != pair[0] || e != pair[1] {
			t.Errorf("Defaults(%q) = %q %q, want %q %q", name, m, e, pair[0], pair[1])
		}
	}
}

func TestResolveModel_NoCatalogPassesAnything(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	for _, want := range []string{"nonsense", ""} {
		if got, err := rota.ResolveModel("t-plain", want); got != want || err != nil {
			t.Errorf("%q: got %q %v", want, got, err)
		}
	}
}

func TestResolveModel_EmptyWantIsDefault(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	if got, err := rota.ResolveModel("t-cl", ""); got != "t-model-1" || err != nil {
		t.Errorf("fake: got %q %v", got, err)
	}
	// codex names no default model, and that empty answer is not an error
	if got, err := rota.ResolveModel("codex", ""); got != "" || err != nil {
		t.Errorf("codex: got %q %v", got, err)
	}
}

func TestResolveModel_IDCaseInsensitive(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	if got, err := rota.ResolveModel("t-cl", "T-MODEL-2"); got != "t-model-2" || err != nil {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestResolveModel_AliasToCanonical(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	for _, want := range []string{"one", "ONE"} {
		if got, err := rota.ResolveModel("t-cl", want); got != "t-model-1" || err != nil {
			t.Errorf("%q: got %q %v", want, got, err)
		}
	}
}

func TestResolveModel_UnknownListsAccepted(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	for _, name := range []string{"t-cl", "claude"} {
		got, err := rota.ResolveModel(name, "zzz")
		if got != "" || !errors.Is(err, rota.ErrInvalidRequest) {
			t.Errorf("%s: got %q %v", name, got, err)
			continue
		}
		if !strings.Contains(err.Error(), "it accepts:") {
			t.Errorf("%s: %q does not list what is accepted", name, err)
		}
		for _, id := range ids(rota.Models(name)) {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("%s: %q misses %q", name, err, id)
			}
		}
	}
}

func TestResolveModel_FloorScansOtherProviders(t *testing.T) {
	fake.Registry(t)
	rota.Register(floorCatalog{catalogOver(fake.New("t-floor"))})
	for _, want := range []string{"claude-opus-5", "opus"} {
		_, err := rota.ResolveModel("t-floor", want)
		if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "claude") {
			t.Errorf("%q: %v, want a refusal naming claude", want, err)
		}
	}
	if got, err := rota.ResolveModel("t-floor", "zzz"); got != "zzz" || err != nil {
		t.Errorf("beyond the floor: got %q %v", got, err)
	}
	if got, err := rota.ResolveModel("t-floor", "one"); got != "t-model-1" || err != nil {
		t.Errorf("own alias: got %q %v", got, err)
	}
}

func TestResolveEffort_NoLevelsWithWantIsError(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	for _, name := range []string{"t-plain", "kimi"} {
		got, err := rota.ResolveEffort(name, "high")
		if got != "" || !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "effort") {
			t.Errorf("%s: got %q %v", name, got, err)
		}
	}
}

func TestResolveEffort_NoLevelsEmptyOK(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-plain"))
	for _, name := range []string{"t-plain", "kimi"} {
		if got, err := rota.ResolveEffort(name, ""); got != "" || err != nil {
			t.Errorf("%s: got %q %v", name, got, err)
		}
	}
}

func TestResolveEffort_DefaultWhenEmpty(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	if got, err := rota.ResolveEffort("t-cl", ""); got != "low" || err != nil {
		t.Errorf("fake: got %q %v", got, err)
	}
	if got, err := rota.ResolveEffort("claude", ""); got != "high" || err != nil {
		t.Errorf("claude: got %q %v", got, err)
	}
}

func TestResolveEffort_CaseInsensitive(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	if got, err := rota.ResolveEffort("t-cl", "HIGH"); got != "high" || err != nil {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestResolveEffort_UnknownListsAccepted(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-cl")))
	got, err := rota.ResolveEffort("t-cl", "medium")
	if got != "" || !errors.Is(err, rota.ErrInvalidRequest) {
		t.Fatalf("got %q %v", got, err)
	}
	for _, want := range []string{"it accepts:", "low", "high"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q misses %q", err, want)
		}
	}
}

func TestResolved_ResolvesAgainstHomeAndBlanksEffortForKimi(t *testing.T) {
	fake.Registry(t)
	rota.Register(flavoredCatalog{
		Catalog: fake.Catalog{Provider: fake.New("t-k"), EffortList: []string{"low", "high"}, DefEffort: "high"},
		flavor:  "kimi",
	})
	rota.Register(fake.Claude(fake.New("t-cl")))
	rota.Register(homeCatalog{Catalog: catalogOver(fake.New("t-ac")), fn: func(_ *rota.Account, home string) []rota.Model {
		return []rota.Model{{ID: "only-" + home}}
	}})

	model, effort, err := rota.Resolved(account("t-k"), "", rota.Spec{Effort: "high"})
	if model != "" || effort != "" || err != nil {
		t.Errorf("kimi flavor: %q %q %v, want an empty effort", model, effort, err)
	}
	model, effort, err = rota.Resolved(account("t-cl"), "", rota.Spec{Model: "one"})
	if model != "t-model-1" || effort != "low" || err != nil {
		t.Errorf("claude flavor: %q %q %v, want t-model-1 low", model, effort, err)
	}
	if model, _, err = rota.Resolved(account("t-ac"), "h", rota.Spec{Model: "only-h"}); model != "only-h" || err != nil {
		t.Errorf("home catalog: %q %v", model, err)
	}
	if _, _, err = rota.Resolved(account("t-ac"), "h", rota.Spec{Model: "t-model-1"}); !errors.Is(err, rota.ErrInvalidRequest) {
		t.Errorf("shipped model against the home catalog: %v, want a refusal", err)
	}
}

func TestFlavor_BuiltinsFlavoredAndUnknownEmpty(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Flavored{Provider: fake.New("t-f"), Name_: "codex"})
	rota.Register(fake.New("t-bare"))
	want := map[string]string{
		"claude": "claude", "codex": "codex", "grok": "grok", "kimi": "kimi",
		"t-f": "codex", "t-bare": "", "zzz": "",
	}
	for name, flavor := range want {
		if got := rota.Flavor(name); got != flavor {
			t.Errorf("Flavor(%q) = %q, want %q", name, got, flavor)
		}
	}
}

func TestFlavorsOf_UnknownNilKnownCopy(t *testing.T) {
	if got := rota.FlavorsOf("zzz"); got != nil {
		t.Fatalf("unknown field: %v, want nil", got)
	}
	first := rota.FlavorsOf("sandbox")
	if len(first) == 0 {
		t.Fatal("sandbox is unrestricted")
	}
	first[0] = "mutated"
	if got := rota.FlavorsOf("sandbox"); slices.Contains(got, "mutated") {
		t.Fatalf("the table changed: %v", got)
	}
}

func TestRestrictedFields_NameSpecJSONTags(t *testing.T) {
	tags := map[string]bool{}
	st := reflect.TypeOf(rota.Spec{})
	for i := range st.NumField() {
		if name, _, _ := strings.Cut(st.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			tags[name] = true
		}
	}
	fields := rota.RestrictedFields()
	if len(fields) == 0 {
		t.Fatal("no restricted fields")
	}
	for _, f := range fields {
		if !tags[f] {
			t.Errorf("%q is restricted but is no Spec field", f)
		}
	}
	for _, want := range []string{"sandbox", "permission_mode"} {
		if !slices.Contains(fields, want) {
			t.Errorf("%q is missing from %v", want, fields)
		}
	}
}

func TestPermissionModes_PerFlavorCopies(t *testing.T) {
	want := map[string][]string{
		"claude": {"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"},
		"grok":   {"default", "acceptEdits", "auto", "dontAsk", "bypassPermissions", "plan"},
		"kimi":   {"plan", "acceptEdits", "dontAsk", "auto", "bypassPermissions"},
		"codex":  nil,
	}
	for name, list := range want {
		if got := rota.PermissionModes(name); !slices.Equal(got, list) || (list == nil && got != nil) {
			t.Errorf("PermissionModes(%q) = %v, want %v", name, got, list)
		}
	}
	first := rota.PermissionModes("claude")
	first[0] = "mutated"
	if got := rota.PermissionModes("claude"); got[0] != "acceptEdits" {
		t.Fatalf("the list changed: %v", got)
	}
}

func TestSandboxes_OnlyCodex(t *testing.T) {
	if got := rota.Sandboxes("codex"); !slices.Equal(got, []string{"read-only", "workspace-write", "danger-full-access"}) {
		t.Errorf("codex: %v", got)
	}
	for _, name := range []string{"claude", "grok", "kimi", "zzz"} {
		if got := rota.Sandboxes(name); got != nil {
			t.Errorf("%s: %v, want nil", name, got)
		}
	}
}

func TestTakesSandbox_CodexAndGrok(t *testing.T) {
	for name, want := range map[string]bool{"codex": true, "grok": true, "claude": false, "kimi": false, "zzz": false} {
		if got := rota.TakesSandbox(name); got != want {
			t.Errorf("TakesSandbox(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNetworkRedirecting_ListAndCopy(t *testing.T) {
	want := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
		"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE",
	}
	first := rota.NetworkRedirecting()
	if !slices.Equal(first, want) {
		t.Fatalf("%v, want %v", first, want)
	}
	first[0] = "mutated"
	if got := rota.NetworkRedirecting(); !slices.Equal(got, want) {
		t.Fatalf("the list changed: %v", got)
	}
}
