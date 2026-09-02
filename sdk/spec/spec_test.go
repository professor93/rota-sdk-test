package spec_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// One provider per CLI vocabulary. A refusal quotes the provider name, so
// the tests assert on these.
const (
	claude = "t-cl"
	codex  = "t-codex"
	grok   = "t-grok"
	kimi   = "t-kimi"
)

// flavorNames is what rota.FlavorsOf answers with, in a fixed order.
var flavorNames = []string{"claude", "codex", "grok", "kimi"}

// providerOf maps a flavor name to the fake registered for it.
var providerOf = map[string]string{"claude": claude, "codex": codex, "grok": grok, "kimi": kimi}

// homeCatalog is a flavored catalog whose model list belongs to the
// account's home, the way codex's does.
type homeCatalog struct {
	*fake.FlavoredCatalog
	fn func(a *rota.Account, home string) []rota.Model
}

func (h homeCatalog) ModelsFor(a *rota.Account, home string) []rota.Model { return h.fn(a, home) }

// flavors registers the four vocabularies in a registry scoped to the
// test. codex and grok carry a small catalog of their own; kimi gets none,
// as the real one has none.
func flavors(t *testing.T) {
	t.Helper()
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New(claude)))
	cx := fake.Flavor(fake.New(codex), "codex")
	cx.ModelList = []rota.Model{{ID: "t-codex-1"}}
	cx.EffortList = []string{"low", "medium", "high"}
	cx.DefModel, cx.DefEffort = "", "medium"
	rota.Register(cx)
	gk := fake.Flavor(fake.New(grok), "grok")
	gk.ModelList = []rota.Model{{ID: "t-grok-1"}}
	gk.DefModel, gk.DefEffort = "t-grok-1", "high"
	rota.Register(gk)
	rota.Register(fake.Flavored{Provider: fake.New(kimi), Name_: "kimi"})
}

// chk checks a spec as the claude flavor, which most refusals are pinned on.
func chk(s rota.Spec, lim *rota.Limits) error { return s.Check(claude, lim) }

// msg is err's text, or "" for nil, so a table can test it without a guard.
func msg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// wantErr fails what unless err carries kind and mentions every word.
func wantErr(t *testing.T, what string, err, kind error, words ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: want %v, got nil", what, kind)
		return
	}
	if !errors.Is(err, kind) {
		t.Errorf("%s: want %v, got %v", what, kind, err)
	}
	for _, w := range words {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%s: want %q in %q", what, w, err)
		}
	}
}

// wantOK fails what unless err is nil.
func wantOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: want no error, got %v", what, err)
	}
}

// write puts content in a file under dir and returns its path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// mkdir creates one directory and returns it.
func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// asPath is a file path the way Settings and MCPConfig take one: as a JSON
// string rather than an object.
func asPath(p string) json.RawMessage {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return b
}

// setByTag sets the Spec field carrying this JSON name to a plausible
// non-zero value of its type, so a table driven off RestrictedFields can
// exercise a field it knows only by wire name.
func setByTag(t *testing.T, s *rota.Spec, name string) {
	t.Helper()
	v := reflect.ValueOf(s).Elem()
	for i := range v.NumField() {
		tag, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		if tag != name {
			continue
		}
		f := v.Field(i)
		var val any
		switch f.Type() {
		case reflect.TypeFor[string]():
			val = "x"
		case reflect.TypeFor[bool]():
			val = true
		case reflect.TypeFor[int]():
			val = 1
		case reflect.TypeFor[float64]():
			val = 1.0
		case reflect.TypeFor[[]string]():
			val = []string{"x"}
		case reflect.TypeFor[map[string]string]():
			val = map[string]string{"k": "v"}
		case reflect.TypeFor[json.RawMessage]():
			val = json.RawMessage(`{}`)
		case reflect.TypeFor[[]json.RawMessage]():
			val = []json.RawMessage{json.RawMessage(`{}`)}
		default:
			t.Fatalf("%q: no plausible value for a %s", name, f.Type())
		}
		f.Set(reflect.ValueOf(val))
		return
	}
	t.Fatalf("%q is restricted but is not a Spec field", name)
}

func TestSpecFor_FillsCwdFromAccountOnlyWhenEmpty(t *testing.T) {
	a := rota.NewAccount(1, claude, &rota.Token{Access: "a"})
	a.Cwd = "/from/account"
	empty := rota.Spec{Prompt: "hi"}
	if got := empty.For(a).Cwd; got != a.Cwd {
		t.Errorf("empty cwd: want %q, got %q", a.Cwd, got)
	}
	if empty.Cwd != "" {
		t.Errorf("For rewrote its receiver: cwd %q", empty.Cwd)
	}
	own := rota.Spec{Prompt: "hi", Cwd: "/own"}
	if got := own.For(a).Cwd; got != "/own" {
		t.Errorf("own cwd: want /own, got %q", got)
	}
}

func TestSpecCheck_WritesNoTempFiles(t *testing.T) {
	flavors(t)
	// A private temp dir, so the count sees only what these checks write.
	t.Setenv("TMPDIR", t.TempDir())
	before := entries(t, os.TempDir())
	for range 3 {
		wantOK(t, "grok prompt", rota.Spec{Prompt: "a private prompt"}.Check(grok, nil))
		wantOK(t, "codex schema", rota.Spec{Prompt: "hi", JSONSchema: json.RawMessage(`{}`)}.Check(codex, nil))
	}
	if after := entries(t, os.TempDir()); after != before {
		t.Fatalf("checking wrote temp files: %d entries became %d", before, after)
	}
}

// entries counts what a directory holds.
func entries(t *testing.T, dir string) int {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestSpecCheck_NegativeTimeoutRefusedFirst(t *testing.T) {
	flavors(t)
	err := chk(rota.Spec{TimeoutSeconds: -1}, nil)
	wantErr(t, "negative timeout, no prompt", err, rota.ErrInvalidRequest, "timeout")
	if strings.Contains(msg(err), "prompt") {
		t.Errorf("the missing prompt was reported before the timeout: %v", err)
	}
}

func TestSpecCheck_BlankPromptRequired(t *testing.T) {
	flavors(t)
	wantErr(t, "blank prompt", chk(rota.Spec{Prompt: "  \n"}, nil), rota.ErrInvalidRequest, "prompt")
}

func TestSpecCheck_ExtraNeedsAllowRawFlags(t *testing.T) {
	flavors(t)
	s := rota.Spec{Prompt: "hi", Extra: []string{"--foo"}}
	wantErr(t, "mediated", chk(s, &rota.Limits{}), rota.ErrInvalidRequest, "args")
	wantOK(t, "raw flags allowed", chk(s, &rota.Limits{AllowRawFlags: true}))
	wantOK(t, "nil limits", chk(s, nil))
}

func TestSpecCheck_ReservedFlagsAlwaysRefused(t *testing.T) {
	flavors(t)
	var cases [][]string
	for _, f := range strings.Fields("-p --print --single --prompt-file --prompt-json --output-format " +
		"--input-format --json --color -o --output-last-message --bare --betas -C --cd --cloud --bg --background") {
		cases = append(cases, []string{f})
	}
	cases = append(cases, []string{"--output-format=text"}, []string{"--output-format", "text"})
	for _, extra := range cases {
		s := rota.Spec{Prompt: "hi", Extra: extra}
		what := strings.Join(extra, " ")
		wantErr(t, what+" with raw flags", chk(s, &rota.Limits{AllowRawFlags: true}), rota.ErrInvalidRequest, "rota sets it")
		wantErr(t, what+" with nil limits", chk(s, nil), rota.ErrInvalidRequest, "rota sets it")
	}
}

func TestSpecCheck_FieldMustBelongToFlavor(t *testing.T) {
	flavors(t)
	for _, name := range rota.RestrictedFields() {
		understood := rota.FlavorsOf(name)
		for _, flavor := range flavorNames {
			s := rota.Spec{Prompt: "hi"}
			setByTag(t, &s, name)
			err := s.Check(providerOf[flavor], nil)
			what := flavor + " " + name
			if slices.Contains(understood, flavor) {
				if strings.Contains(msg(err), "does not understand") {
					t.Errorf("%s: listed as understood, yet refused: %v", what, err)
				}
				continue
			}
			wantErr(t, what, err, rota.ErrInvalidRequest, "does not understand", name)
		}
	}
}

func TestSpecCheck_EmptySliceCountsAsSet(t *testing.T) {
	flavors(t)
	err := rota.Spec{Prompt: "hi", Tools: []string{}}.Check(codex, nil)
	wantErr(t, "empty tools on codex", err, rota.ErrInvalidRequest, "does not understand", "tools")
	wantOK(t, "nil tools on codex", rota.Spec{Prompt: "hi", Tools: nil}.Check(codex, nil))
}

func TestSpecCheck_CwdMustExistAndBeADirectory(t *testing.T) {
	flavors(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	wantErr(t, "missing", chk(rota.Spec{Prompt: "hi", Cwd: missing}, nil), rota.ErrInvalidRequest, "existing directory")
	plain := write(t, dir, "plain", "x")
	wantErr(t, "file", chk(rota.Spec{Prompt: "hi", Cwd: plain}, nil), rota.ErrInvalidRequest, "existing directory")
}

func TestSpecCheck_RootsConfineDirectories(t *testing.T) {
	flavors(t)
	root := t.TempDir()
	inside := mkdir(t, filepath.Join(root, "inner"))
	// A sibling whose name has the root as a prefix, which a string
	// comparison would let through.
	sibling := mkdir(t, root+"2")
	lim := &rota.Limits{Roots: []string{root}}
	wantOK(t, "root itself", chk(rota.Spec{Prompt: "hi", Cwd: root}, lim))
	wantOK(t, "inside", chk(rota.Spec{Prompt: "hi", Cwd: inside}, lim))
	wantErr(t, "sibling", chk(rota.Spec{Prompt: "hi", Cwd: sibling}, lim), rota.ErrOutsideRoots)
	wantOK(t, "no roots", chk(rota.Spec{Prompt: "hi", Cwd: sibling}, &rota.Limits{}))
}

func TestSpecCheck_RootsResolveSymlinks(t *testing.T) {
	flavors(t)
	root := t.TempDir()
	outside := t.TempDir()
	inside := mkdir(t, filepath.Join(root, "inner"))
	escape := filepath.Join(root, "escape")
	stay := filepath.Join(root, "stay")
	if err := errors.Join(os.Symlink(outside, escape), os.Symlink(inside, stay)); err != nil {
		t.Fatal(err)
	}
	lim := &rota.Limits{Roots: []string{root}}
	wantErr(t, "link out of the root", chk(rota.Spec{Prompt: "hi", Cwd: escape}, lim), rota.ErrOutsideRoots)
	wantOK(t, "link within the root", chk(rota.Spec{Prompt: "hi", Cwd: stay}, lim))
}

func TestSpecCheck_FilesCheckedAgainstRoots(t *testing.T) {
	flavors(t)
	root := t.TempDir()
	outside := t.TempDir()
	lim := &rota.Limits{Roots: []string{root}}
	// The settings and MCP files are clean, so the only thing wrong with
	// them is where they live.
	image := write(t, outside, "pic.png", "x")
	settings := write(t, outside, "settings.json", `{"theme":"dark"}`)
	mcp := write(t, outside, "mcp.json", `{"mcpServers":{"x":{"url":"http://127.0.0.1/"}}}`)
	wantErr(t, "images", rota.Spec{Prompt: "hi", Images: []string{image}}.Check(codex, lim), rota.ErrOutsideRoots)
	wantErr(t, "settings path", rota.Spec{Prompt: "hi", Settings: asPath(settings)}.Check(claude, lim), rota.ErrOutsideRoots)
	wantErr(t, "mcp_config path", rota.Spec{Prompt: "hi", MCPConfig: []json.RawMessage{asPath(mcp)}}.Check(claude, lim), rota.ErrOutsideRoots)
	wantErr(t, "grok debug file", rota.Spec{Prompt: "hi", Debug: filepath.Join(outside, "debug.log")}.Check(grok, lim), rota.ErrOutsideRoots)
	wantOK(t, "grok debug true", rota.Spec{Prompt: "hi", Debug: "true"}.Check(grok, lim))
}

func TestSpecCheck_SettingsDenylistDefault(t *testing.T) {
	flavors(t)
	for _, key := range strings.Fields("env apiKeyHelper awsAuthRefresh awsCredentialExport hooks permissions otelHeadersHelper statusLine forceLoginMethod") {
		s := rota.Spec{Prompt: "hi", Settings: json.RawMessage(`{"` + key + `":{}}`)}
		wantErr(t, key, chk(s, &rota.Limits{}), rota.ErrInvalidRequest, key)
	}
	wantOK(t, "theme", chk(rota.Spec{Prompt: "hi", Settings: json.RawMessage(`{"theme":"dark"}`)}, &rota.Limits{}))
}

func TestSpecCheck_SettingsDenylistReplaced(t *testing.T) {
	flavors(t)
	lim := &rota.Limits{SettingsDenyKeys: []string{"theme"}}
	wantOK(t, "env with theme-only denylist", chk(rota.Spec{Prompt: "hi", Settings: json.RawMessage(`{"env":{}}`)}, lim))
	wantErr(t, "theme", chk(rota.Spec{Prompt: "hi", Settings: json.RawMessage(`{"theme":1}`)}, lim), rota.ErrInvalidRequest, "theme")
}

func TestSpecCheck_SettingsFileIsVetted(t *testing.T) {
	flavors(t)
	dir := t.TempDir()
	hooks := write(t, dir, "hooks.json", `{"hooks":{}}`)
	wantErr(t, "file with hooks", chk(rota.Spec{Prompt: "hi", Settings: asPath(hooks)}, &rota.Limits{}), rota.ErrInvalidRequest, "hooks")
	clean := write(t, dir, "clean.json", `{"theme":"dark"}`)
	wantOK(t, "clean file", chk(rota.Spec{Prompt: "hi", Settings: asPath(clean)}, &rota.Limits{}))
}

func TestSpecCheck_SettingsFileOver1MBRefused(t *testing.T) {
	flavors(t)
	big := write(t, t.TempDir(), "big.json", `{"pad":"`+strings.Repeat("x", 1<<20)+`"}`)
	wantErr(t, "over 1MB", chk(rota.Spec{Prompt: "hi", Settings: asPath(big)}, &rota.Limits{}), rota.ErrInvalidRequest, "1MB")
}

func TestSpecCheck_SettingsFileUnreadableRefused(t *testing.T) {
	flavors(t)
	wantErr(t, "directory", chk(rota.Spec{Prompt: "hi", Settings: asPath(t.TempDir())}, &rota.Limits{}), rota.ErrInvalidRequest)
}

func TestSpecCheck_MCPInlineRefused(t *testing.T) {
	flavors(t)
	inline := json.RawMessage(`{"mcpServers":{"x":{"url":"http://127.0.0.1/"}}}`)
	wantErr(t, "inline", chk(rota.Spec{Prompt: "hi", MCPConfig: []json.RawMessage{inline}}, &rota.Limits{}), rota.ErrInvalidRequest, "path")
}

func TestSpecCheck_MCPFileWithCommandRefused(t *testing.T) {
	flavors(t)
	f := write(t, t.TempDir(), "mcp.json", `{"mcpServers":{"x":{"command":"rm"}}}`)
	wantErr(t, "command", chk(rota.Spec{Prompt: "hi", MCPConfig: []json.RawMessage{asPath(f)}}, &rota.Limits{}), rota.ErrInvalidRequest, "command")
}

func TestSpecCheck_MCPFileWithURLOnlyOK(t *testing.T) {
	flavors(t)
	f := write(t, t.TempDir(), "mcp.json", `{"mcpServers":{"x":{"url":"http://127.0.0.1/"}}}`)
	wantOK(t, "url only", chk(rota.Spec{Prompt: "hi", MCPConfig: []json.RawMessage{asPath(f)}}, &rota.Limits{}))
}

func TestSpecCheck_SettingSourcesNeedRawFlags(t *testing.T) {
	flavors(t)
	s := rota.Spec{Prompt: "hi", SettingSources: []string{"user"}}
	wantErr(t, "mediated", chk(s, &rota.Limits{}), rota.ErrInvalidRequest, "setting_sources")
	wantOK(t, "raw flags allowed", chk(s, &rota.Limits{AllowRawFlags: true}))
	wantOK(t, "nil limits", chk(s, nil))
	wantOK(t, "explicit empty list", chk(rota.Spec{Prompt: "hi", SettingSources: []string{}}, &rota.Limits{}))
}

func TestSpecCheck_PluginURLsRefused(t *testing.T) {
	flavors(t)
	s := rota.Spec{Prompt: "hi", PluginURLs: []string{"https://127.0.0.1/plugin.zip"}}
	wantErr(t, "mediated", chk(s, &rota.Limits{}), rota.ErrInvalidRequest, "plugin_urls")
	wantOK(t, "raw flags allowed", chk(s, &rota.Limits{AllowRawFlags: true}))
}

func TestSpecCheck_CodexConfigRefused(t *testing.T) {
	flavors(t)
	s := rota.Spec{Prompt: "hi", Config: map[string]string{"k": "v"}}
	wantErr(t, "mediated", s.Check(codex, &rota.Limits{}), rota.ErrInvalidRequest, "config")
	wantOK(t, "raw flags allowed", s.Check(codex, &rota.Limits{AllowRawFlags: true}))
}

func TestSpecCheck_NilLimitsSkipsSuppliedConfigChecks(t *testing.T) {
	flavors(t)
	wantOK(t, "inline env settings", chk(rota.Spec{Prompt: "hi", Settings: json.RawMessage(`{"env":{}}`)}, nil))
	inline := json.RawMessage(`{"mcpServers":{"x":{"command":"rm"}}}`)
	wantOK(t, "inline mcp server", chk(rota.Spec{Prompt: "hi", MCPConfig: []json.RawMessage{inline}}, nil))
}

func TestSpecCheck_DangerousGates(t *testing.T) {
	flavors(t)
	cases := []struct {
		flavor string
		what   string
		spec   rota.Spec
	}{
		{"claude", "permission_mode bypassPermissions", rota.Spec{PermissionMode: "bypassPermissions"}},
		{"claude", "dangerously_skip_permissions", rota.Spec{DangerouslySkipPermissions: true}},
		{"claude", "allow_dangerously_skip_permissions", rota.Spec{AllowDangerouslySkipPermissions: true}},
		{"codex", "sandbox danger-full-access", rota.Spec{Sandbox: "danger-full-access"}},
		{"codex", "dangerously_bypass_approvals_and_sandbox", rota.Spec{BypassApprovalsAndSandbox: true}},
		{"codex", "dangerously_bypass_hook_trust", rota.Spec{BypassHookTrust: true}},
		{"grok", "permission_mode bypassPermissions", rota.Spec{PermissionMode: "bypassPermissions"}},
		{"grok", "sandbox danger-full-access", rota.Spec{Sandbox: "danger-full-access"}},
		{"grok", "always_approve", rota.Spec{AlwaysApprove: true}},
		{"kimi", "permission_mode auto", rota.Spec{PermissionMode: "auto"}},
		{"kimi", "permission_mode bypassPermissions", rota.Spec{PermissionMode: "bypassPermissions"}},
	}
	for _, c := range cases {
		c.spec.Prompt = "hi"
		what := c.flavor + " " + c.what
		wantErr(t, what, c.spec.Check(providerOf[c.flavor], &rota.Limits{}), rota.ErrDangerous, c.what)
		wantOK(t, what+" allowed", c.spec.Check(providerOf[c.flavor], &rota.Limits{AllowDangerous: true}))
	}
}

func TestSpecCheck_EnumRefusals(t *testing.T) {
	flavors(t)
	for _, flavor := range []string{"claude", "grok", "kimi"} {
		err := rota.Spec{Prompt: "hi", PermissionMode: "weird"}.Check(providerOf[flavor], nil)
		wantErr(t, flavor+" permission_mode", err, rota.ErrInvalidRequest, "is not one of")
	}
	wantErr(t, "codex sandbox", rota.Spec{Prompt: "hi", Sandbox: "weird"}.Check(codex, nil), rota.ErrInvalidRequest, "is not one of")
	wantOK(t, "grok sandbox", rota.Spec{Prompt: "hi", Sandbox: "weird"}.Check(grok, nil))
}

func TestSpecCheck_UnknownFlavorIsUnsupported(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.New("t-bare"))
	wantErr(t, "no flavor", rota.Spec{Prompt: "hi"}.Check("t-bare", nil), rota.ErrUnsupported)
}

func TestSpecCheck_ModelAndEffortResolved(t *testing.T) {
	flavors(t)
	wantErr(t, "unknown model", chk(rota.Spec{Prompt: "hi", Model: "nope"}, nil), rota.ErrInvalidRequest, "accepts", "t-model-1", "t-model-2")
	wantOK(t, "alias", chk(rota.Spec{Prompt: "hi", Model: "one"}, nil))
	wantErr(t, "unknown effort", chk(rota.Spec{Prompt: "hi", Effort: "medium"}, nil), rota.ErrInvalidRequest, "accepts", "low", "high")
}

func TestSpecCheckFor_UsesAccountHomeCatalog(t *testing.T) {
	fake.Registry(t)
	rota.Register(homeCatalog{FlavoredCatalog: fake.Claude(fake.New(claude)), fn: func(a *rota.Account, home string) []rota.Model {
		return []rota.Model{{ID: "only-" + home}}
	}})
	a := rota.NewAccount(1, claude, &rota.Token{Access: "a"})
	wantOK(t, "home model", rota.Spec{Prompt: "hi", Model: "only-h"}.CheckFor(a, "h", nil))
	wantErr(t, "provider model with a home", rota.Spec{Prompt: "hi", Model: "t-model-1"}.CheckFor(a, "h", nil), rota.ErrInvalidRequest, "only-h")
	wantOK(t, "provider model without a home", rota.Spec{Prompt: "hi", Model: "t-model-1"}.CheckFor(a, "", nil))
}
