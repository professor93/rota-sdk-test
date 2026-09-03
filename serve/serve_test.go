// Package serve_test drives the published rota binary over HTTP, the way a
// client that never imports the module would: rota serve on a loopback
// port, a seeded store, fake vendor CLIs on its PATH, and nothing on the
// network.
package serve_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/* ------------------------------------------------------------- GET / -- */

func TestRoot_UnauthenticatedSaysVersion(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("GET", "/", "", "Authorization", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	doc := object(t, raw)
	// Alive and which version, nothing else; the version is the binary's own.
	if doc["success"] != true || doc["version"] != binVersion || len(doc) != 2 {
		t.Fatalf("want {success:true, version:%q}, got %s", binVersion, raw)
	}
}

/* ------------------------------------------------------------ bearer -- */

// Every guarded route, with the id 1 where one is needed.
var guarded = [][2]string{
	{"GET", "/v1/schema"}, {"GET", "/v1/accounts"}, {"GET", "/v1/accounts/1/schema"},
	{"POST", "/v1/run"}, {"POST", "/v1/accounts/1/run"},
	{"PATCH", "/v1/accounts/1"}, {"DELETE", "/v1/accounts/1"},
	{"POST", "/v1/login"}, {"POST", "/v1/login/abcdef"},
	{"POST", "/v1/auth"}, {"POST", "/v1/auth/abcdef"},
}

func TestV1_NeedsBearer(t *testing.T) {
	var s *server
	for i, r := range guarded {
		// Ten bad tokens block an address (api/server.go failMax), and a
		// missing one counts, so the routes are spread over two servers.
		if i%10 == 0 {
			s = start(t, opts{})
		}
		resp, raw := s.do(r[0], r[1], `{"prompt":"p"}`, "Authorization", "")
		if resp.StatusCode != 401 || !strings.Contains(string(raw), `"error"`) {
			t.Fatalf("%s %s without a token: %d %s", r[0], r[1], resp.StatusCode, raw)
		}
	}
}

func TestV1_WrongTokenIs401(t *testing.T) {
	s := start(t, opts{})
	for _, h := range []string{"Bearer nope", "Bearer " + token + "x", "Basic " + token, token} {
		resp, raw := s.do("GET", "/v1/accounts", "", "Authorization", h)
		if resp.StatusCode != 401 {
			t.Fatalf("%q: %d %s", h, resp.StatusCode, raw)
		}
	}
	if resp, raw := s.do("GET", "/v1/accounts", ""); resp.StatusCode != 200 {
		t.Fatalf("the right token after wrong ones: %d %s", resp.StatusCode, raw)
	}
}

func TestV1_TenBadTokensBlockTheAddress(t *testing.T) {
	s := start(t, opts{})
	bad := func() int {
		resp, _ := s.do("GET", "/v1/accounts", "", "Authorization", "Bearer nope")
		return resp.StatusCode
	}
	// failMax is 10 within failWindow (api/server.go): the first ten are
	// refused one by one, the eleventh is refused for being the eleventh.
	for i := range 10 {
		if code := bad(); code != 401 {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
	}
	if code := bad(); code != 429 {
		t.Fatalf("eleventh bad token: %d, want 429", code)
	}
	// Since 1.0.6 the block is for guesses only: the right token is admitted
	// from a blocked address, so a proxy or a web page on the loopback cannot
	// lock the operator out; wrong tokens from it stay refused.
	if resp, raw := s.do("GET", "/v1/accounts", ""); resp.StatusCode != 200 {
		t.Fatalf("a blocked address with the right token: %d %s", resp.StatusCode, raw)
	}
	if code := bad(); code != 429 {
		t.Fatalf("wrong tokens stay blocked: %d", code)
	}
	if resp, _ := s.do("GET", "/", "", "Authorization", ""); resp.StatusCode != 200 {
		t.Fatalf("the root is outside the limiter: %d", resp.StatusCode)
	}
}

/* ------------------------------------------------------- /v1/schema -- */

func TestSchema_DescribesEveryProvider(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("GET", "/v1/schema", "")
	var doc struct {
		Version   string `json:"version"`
		Providers map[string]struct {
			Flavor   string            `json:"flavor"`
			Models   []json.RawMessage `json:"models"`
			Efforts  []string          `json:"efforts"`
			Defaults struct {
				Model  string `json:"model"`
				Effort string `json:"effort"`
			} `json:"defaults"`
			Metered bool              `json:"metered"`
			Fields  []json.RawMessage `json:"fields"`
		} `json:"providers"`
		Fields         []json.RawMessage `json:"fields"`
		AllowDangerous bool              `json:"allow_dangerous"`
		AllowRawFlags  bool              `json:"allow_raw_flags"`
		Roots          []string          `json:"roots"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v %s", resp.StatusCode, err, raw)
	}
	if doc.Version != binVersion {
		t.Fatalf("version %q, want %q", doc.Version, binVersion)
	}
	for _, name := range []string{"claude", "codex", "grok", "kimi"} {
		p, ok := doc.Providers[name]
		if !ok || p.Flavor != name || len(p.Fields) == 0 {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	// A catalog is the provider's to publish. kimi has none, so its model
	// field is free text; codex has models but leaves the default to its CLI.
	for _, name := range []string{"claude", "codex", "grok"} {
		if p := doc.Providers[name]; len(p.Models) == 0 || len(p.Efforts) == 0 || p.Defaults.Effort == "" {
			t.Fatalf("%s catalog: %+v", name, p)
		}
	}
	for _, name := range []string{"claude", "grok"} {
		if doc.Providers[name].Defaults.Model == "" {
			t.Fatalf("%s names no default model", name)
		}
	}
	if kimi := doc.Providers["kimi"]; len(kimi.Models) != 0 || !strings.Contains(string(raw), `"name":"model","kind":"string"`) {
		t.Fatalf("kimi: %+v", kimi)
	}
	// Metered is a fact about the provider: claude publishes usage, grok
	// runs on an API key and does not.
	if !doc.Providers["claude"].Metered || doc.Providers["grok"].Metered {
		t.Fatalf("metered: claude %v grok %v", doc.Providers["claude"].Metered, doc.Providers["grok"].Metered)
	}
	if len(doc.Fields) == 0 || doc.AllowDangerous || doc.AllowRawFlags || len(doc.Roots) != 0 {
		t.Fatalf("server-wide part: fields=%d dangerous=%v raw=%v roots=%v",
			len(doc.Fields), doc.AllowDangerous, doc.AllowRawFlags, doc.Roots)
	}
}

/* ----------------------------------------------------- /v1/accounts -- */

func TestAccounts_ListedInRotationOrderWithDefault(t *testing.T) {
	s := start(t, opts{})
	l := s.list()
	if got := l.ids(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("order: %v, want [1 2]", got)
	}
	if l.Default == nil || *l.Default != 1 {
		t.Fatalf("default: %v, want 1 (first in the queue)", l.Default)
	}
	grok, claude := l.Accounts[0], l.Accounts[1]
	if grok.Provider != "grok" || grok.Email != "g@x" || grok.Order != 1 || grok.Metered || grok.Percent != 0 {
		t.Fatalf("grok row: %+v", grok)
	}
	if claude.Provider != "claude" || claude.Order != 2 || !claude.Metered || claude.Percent != 10 ||
		len(claude.Windows) != 1 || claude.Windows[0].Name != "five_hour" {
		t.Fatalf("claude row: %+v", claude)
	}
	for _, a := range l.Accounts {
		if a.Status == "" {
			t.Fatalf("status missing: %+v", a)
		}
	}
}

func TestAccounts_ThresholdReadsTheCutoff(t *testing.T) {
	s := start(t, opts{})
	// Neither seeded account names a threshold, and the listing does not
	// echo the zero back: it says what the rotation will actually use.
	for _, a := range s.list().Accounts {
		if a.Threshold != 100 {
			t.Fatalf("account %d threshold %d, want 100", a.ID, a.Threshold)
		}
	}
}

func TestAccounts_DefaultSkipsAnAccountOutOfTheQueue(t *testing.T) {
	// grok at order 0 is listed last and never picked; claude is the default.
	seeded := strings.Replace(seed(), `"order":1,`, `"order":0,`, 1)
	s := start(t, opts{accounts: seeded})
	l := s.list()
	if got := l.ids(); len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("order: %v, want [2 1]", got)
	}
	if l.Default == nil || *l.Default != 2 {
		t.Fatalf("default: %v, want 2", l.Default)
	}
}

/* ------------------------------------------- /v1/accounts/{id}/schema -- */

func TestAccountSchema_DescribesOneAccount(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("GET", "/v1/accounts/1/schema", "")
	var doc struct {
		Account  int               `json:"account"`
		Provider string            `json:"provider"`
		Flavor   string            `json:"flavor"`
		Models   []json.RawMessage `json:"models"`
		Efforts  []string          `json:"efforts"`
		Defaults struct {
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"defaults"`
		Fields []json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v %s", resp.StatusCode, err, raw)
	}
	if doc.Account != 1 || doc.Provider != "grok" || doc.Flavor != "grok" || len(doc.Models) == 0 ||
		strings.Join(doc.Efforts, ",") != "low,medium,high,xhigh" ||
		doc.Defaults.Model != "grok-4.6" || doc.Defaults.Effort != "high" || len(doc.Fields) == 0 {
		t.Fatalf("%s", raw)
	}
}

func TestAccountSchema_UnknownIdIs404(t *testing.T) {
	s := start(t, opts{})
	if resp, raw := s.do("GET", "/v1/accounts/99/schema", ""); resp.StatusCode != 404 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	if resp, raw := s.do("GET", "/v1/accounts/abc/schema", ""); resp.StatusCode != 400 {
		t.Fatalf("a non-numeric id: %d %s", resp.StatusCode, raw)
	}
}

/* ----------------------------------------------- PATCH /v1/accounts -- */

// patch sends one PATCH and returns its status and decoded body.
func patch(s *server, id string, body string) (int, map[string]any, []byte) {
	s.t.Helper()
	resp, raw := s.do("PATCH", "/v1/accounts/"+id, body)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	return resp.StatusCode, doc, raw
}

// claudeFirst moves claude (2) ahead of grok (1) by the given order value
// and checks both the reply and the renumbered listing that follows.
func claudeFirst(t *testing.T, order string) {
	t.Helper()
	s := start(t, opts{})
	code, doc, raw := patch(s, "2", `{"order":`+order+`}`)
	if code != 200 || doc["id"] != 2.0 || doc["order"] != 1.0 || doc["threshold"] != 100.0 {
		t.Fatalf("order %s: %d %s", order, code, raw)
	}
	l := s.list()
	if got := l.ids(); len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("order %s: listing %v, want [2 1]", order, got)
	}
	// The other account shifted rather than sharing the number.
	if l.Accounts[0].Order != 1 || l.Accounts[1].Order != 2 {
		t.Fatalf("order %s: numbers %d,%d want 1,2", order, l.Accounts[0].Order, l.Accounts[1].Order)
	}
	if l.Default == nil || *l.Default != 2 {
		t.Fatalf("order %s: default %v, want 2", order, l.Default)
	}
}

func TestPatchAccount_OrderNumberTakesThatPlace(t *testing.T) { claudeFirst(t, `1`) }

func TestPatchAccount_OrderNumberAsStringIsTheSame(t *testing.T) { claudeFirst(t, `"1"`) }

func TestPatchAccount_OrderFirstShiftsQueue(t *testing.T) { claudeFirst(t, `"first"`) }

func TestPatchAccount_OrderUpMovesOnePlace(t *testing.T) { claudeFirst(t, `"up"`) }

func TestPatchAccount_OrderBeforeIdPlacesRelative(t *testing.T) { claudeFirst(t, `"before:1"`) }

func TestPatchAccount_OrderZeroLeavesTheQueue(t *testing.T) {
	s := start(t, opts{})
	code, doc, raw := patch(s, "1", `{"order":0}`)
	if code != 200 || doc["order"] != 0.0 {
		t.Fatalf("%d %s", code, raw)
	}
	l := s.list()
	// Out of the queue means listed last, renumbered around, never picked.
	if got := l.ids(); len(got) != 2 || got[0] != 2 || got[1] != 1 || l.Accounts[0].Order != 1 || l.Accounts[1].Order != 0 {
		t.Fatalf("%+v", l.Accounts)
	}
	if l.Default == nil || *l.Default != 2 {
		t.Fatalf("default %v, want 2", l.Default)
	}
}

func TestPatchAccount_OrderPastTheEndIsLast(t *testing.T) {
	s := start(t, opts{})
	if code, _, raw := patch(s, "1", `{"order":50}`); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	l := s.list()
	if got := l.ids(); got[0] != 2 || got[1] != 1 || l.Accounts[1].Order != 2 {
		t.Fatalf("%+v", l.Accounts)
	}
}

func TestPatchAccount_BadOrderIs400(t *testing.T) {
	s := start(t, opts{})
	for _, order := range []string{`"sideways"`, `-1`, `1.5`, `true`, `"before:x"`, `"before:2"`, `[1]`} {
		code, _, raw := patch(s, "2", `{"order":`+order+`}`)
		if code != 400 || !strings.Contains(string(raw), `"error"`) {
			t.Fatalf("order %s: %d %s", order, code, raw)
		}
	}
	// None of the refusals moved anything.
	if got := s.list().ids(); got[0] != 1 || got[1] != 2 {
		t.Fatalf("listing changed: %v", got)
	}
}

func TestPatchAccount_NothingToChangeIs400(t *testing.T) {
	s := start(t, opts{})
	for _, body := range []string{`{}`, `{"unrelated":1}`} {
		if code, _, raw := patch(s, "1", body); code != 400 || !strings.Contains(string(raw), "nothing to change") {
			t.Fatalf("body %q: %d %s", body, code, raw)
		}
	}
	// No body at all is a bad request too, refused for being no document
	// rather than for changing nothing.
	if code, _, raw := patch(s, "1", ``); code != 400 || !strings.Contains(string(raw), `"error"`) {
		t.Fatalf("empty body: %d %s", code, raw)
	}
}

func TestPatchAccount_UnknownIdIs404(t *testing.T) {
	s := start(t, opts{})
	if code, _, raw := patch(s, "99", `{"order":1}`); code != 404 {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestPatchAccount_ThresholdOutOfRangeIs400(t *testing.T) {
	s := start(t, opts{})
	for _, th := range []string{`0`, `101`, `-5`} {
		if code, _, raw := patch(s, "2", `{"threshold":`+th+`}`); code != 400 || !strings.Contains(string(raw), "1 to 100") {
			t.Fatalf("threshold %s: %d %s", th, code, raw)
		}
	}
	if s.list().Accounts[1].Threshold != 100 {
		t.Fatal("a refused threshold must leave the account as it was")
	}
}

func TestPatchAccount_ThresholdIsStored(t *testing.T) {
	s := start(t, opts{})
	code, doc, raw := patch(s, "2", `{"threshold":50}`)
	if code != 200 || doc["threshold"] != 50.0 {
		t.Fatalf("%d %s", code, raw)
	}
	if got := s.list().Accounts[1]; got.ID != 2 || got.Threshold != 50 {
		t.Fatalf("%+v", got)
	}
}

func TestPatchAccount_CwdEqualToConfigDirIs400(t *testing.T) {
	s := start(t, opts{})
	dir := t.TempDir()
	body := `{"cwd":` + quote(dir) + `,"config_dir":` + quote(dir+"/") + `}`
	if code, _, raw := patch(s, "2", body); code != 400 || !strings.Contains(string(raw), "config_dir") {
		t.Fatalf("%d %s", code, raw)
	}
	if a := s.list().Accounts[1]; a.Cwd != "" || a.ConfigDir != "" {
		t.Fatalf("a refused project must not be written: %+v", a)
	}
}

func TestPatchAccount_RelativeProjectPathIs400(t *testing.T) {
	s := start(t, opts{})
	for _, body := range []string{`{"cwd":"src/api"}`, `{"config_dir":"./homes/api"}`} {
		if code, _, raw := patch(s, "2", body); code != 400 || !strings.Contains(string(raw), "absolute") {
			t.Fatalf("%s: %d %s", body, code, raw)
		}
	}
}

func TestPatchAccount_ProjectDirsAreStored(t *testing.T) {
	s := start(t, opts{})
	cwd, cfg := t.TempDir(), t.TempDir()
	code, doc, raw := patch(s, "2", `{"cwd":`+quote(cwd)+`,"config_dir":`+quote(cfg)+`}`)
	if code != 200 || doc["cwd"] != cwd || doc["config_dir"] != cfg {
		t.Fatalf("%d %s", code, raw)
	}
	if a := s.list().Accounts[1]; a.Cwd != cwd || a.ConfigDir != cfg {
		t.Fatalf("%+v", a)
	}
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

/* ---------------------------------------------- DELETE /v1/accounts -- */

func TestDeleteAccount_RemovesAndThen404(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("DELETE", "/v1/accounts/1", "")
	var doc struct {
		Removed struct {
			ID       int    `json:"id"`
			Provider string `json:"provider"`
			Email    string `json:"email"`
		} `json:"removed"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || resp.StatusCode != 200 ||
		doc.Removed.ID != 1 || doc.Removed.Provider != "grok" || doc.Removed.Email != "g@x" {
		t.Fatalf("%d %v %s", resp.StatusCode, err, raw)
	}
	l := s.list()
	if got := l.ids(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("after removal: %v", got)
	}
	if l.Default == nil || *l.Default != 2 {
		t.Fatalf("default after removal: %v", l.Default)
	}
	if resp, raw := s.do("DELETE", "/v1/accounts/1", ""); resp.StatusCode != 404 {
		t.Fatalf("second delete: %d %s", resp.StatusCode, raw)
	}
}

func TestDeleteAccount_BadIdIs400(t *testing.T) {
	s := start(t, opts{})
	for _, id := range []string{"abc", "0", "-1"} {
		if resp, raw := s.do("DELETE", "/v1/accounts/"+id, ""); resp.StatusCode != 400 {
			t.Fatalf("%s: %d %s", id, resp.StatusCode, raw)
		}
	}
}

/* ------------------------------------------------------------- runs -- */

func TestRun_ByIdReturnsTheResultFields(t *testing.T) {
	s := start(t, opts{})
	code, out, raw := s.run(1, `{"prompt":"hello grok"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if out.Account != 1 || out.Provider != "grok" || out.IsError || out.ExitCode != 0 ||
		!strings.HasPrefix(out.SessionID, "01a00000-") || !strings.Contains(out.Result, "PROMPT=hello grok") ||
		!strings.Contains(out.Result, "--output-format json") {
		t.Fatalf("%+v", out)
	}
	// What actually ran is always reported, defaults included.
	if out.Model != "grok-4.6" || out.Effort != "high" {
		t.Fatalf("model %q effort %q", out.Model, out.Effort)
	}
	// The two booleans are present even when zero: a client must not guess.
	doc := object(t, raw)
	for _, key := range []string{"account", "provider", "result", "is_error", "exit_code", "session_id", "model", "effort"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("reply lacks %q: %s", key, raw)
		}
	}
}

func TestRun_RotationPicksTheDefault(t *testing.T) {
	s := start(t, opts{})
	code, out, raw := s.run(0, `{"prompt":"whoever"}`)
	if code != 200 || out.Account != 1 || out.Provider != "grok" || !strings.Contains(out.Result, "PROMPT=whoever") {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestRun_ClaudeReadsTheResultEvent(t *testing.T) {
	s := start(t, opts{})
	code, out, raw := s.run(2, `{"prompt":"hello claude","model":"sonnet","effort":"low"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if out.Account != 2 || out.Provider != "claude" || out.SessionID != "s-fake" || out.Stderr != "fake-stderr" ||
		!strings.Contains(out.Result, "PROMPT=hello claude") || !strings.Contains(out.Result, "--effort low") ||
		out.Effort != "low" || !strings.HasPrefix(out.Model, "claude-sonnet") {
		t.Fatalf("%+v", out)
	}
	// The prompt travels on stdin, never in the argv.
	if strings.Contains(strings.SplitN(out.Result, "ARGS=", 2)[1], "hello claude") {
		t.Fatalf("prompt on the command line: %q", out.Result)
	}
}

func TestRun_MissingPromptIs400(t *testing.T) {
	s := start(t, opts{})
	for _, body := range []string{`{}`, `{"prompt":""}`, `{"prompt":"   "}`, `{"stream":true}`} {
		if code, out, raw := s.run(1, body); code != 400 || !strings.Contains(out.Error, "prompt") {
			t.Fatalf("%s: %d %s", body, code, raw)
		}
	}
}

func TestRun_UnknownFieldIs400(t *testing.T) {
	s := start(t, opts{})
	if code, _, raw := s.run(1, `{"prompt":"p","bogus":1}`); code != 400 || !strings.Contains(string(raw), "bogus") {
		t.Fatalf("%d %s", code, raw)
	}
	if code, _, raw := s.run(1, `not json`); code != 400 {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestRun_UnknownAccountIs404(t *testing.T) {
	s := start(t, opts{})
	if code, _, raw := s.run(99, `{"prompt":"p"}`); code != 404 {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestRun_DeadAccountIs409(t *testing.T) {
	seeded := strings.Replace(seed(), `"provider":"claude",`, `"provider":"claude","dead":true,`, 1)
	s := start(t, opts{accounts: seeded})
	if code, _, raw := s.run(2, `{"prompt":"p"}`); code != 409 || !strings.Contains(string(raw), "re-auth") {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestRun_DangerousOptionIs403WithoutFlag(t *testing.T) {
	s := start(t, opts{})
	for id, body := range map[int]string{
		2: `{"prompt":"p","permission_mode":"bypassPermissions"}`,
		1: `{"prompt":"p","always_approve":true}`,
	} {
		if code, _, raw := s.run(id, body); code != 403 || !strings.Contains(string(raw), `"error"`) {
			t.Fatalf("account %d %s: %d %s", id, body, code, raw)
		}
	}
	// A refused request must not have run anything.
	if code, out, _ := s.run(2, `{"prompt":"p","dangerously_skip_permissions":true}`); code != 403 || out.Result != "" {
		t.Fatalf("%d %+v", code, out)
	}
}

func TestRun_DangerousOptionRunsWithFlag(t *testing.T) {
	s := start(t, opts{allowDangerous: true})
	code, out, raw := s.run(2, `{"prompt":"p","permission_mode":"bypassPermissions"}`)
	if code != 200 || !strings.Contains(out.Result, "--permission-mode bypassPermissions") {
		t.Fatalf("%d %s", code, raw)
	}
	if resp, raw := s.do("GET", "/v1/schema", ""); !strings.Contains(string(raw), `"allow_dangerous":true`) {
		t.Fatalf("the schema must say so: %d %s", resp.StatusCode, raw)
	}
}

func TestRun_CwdOutsideRootIs400(t *testing.T) {
	s := start(t, opts{root: true})
	outside := t.TempDir()
	for _, body := range []string{
		`{"prompt":"p","cwd":` + quote(outside) + `}`,
		`{"prompt":"p","cwd":` + quote(filepath.Join(s.root, "..")) + `}`,
		`{"prompt":"p","add_dirs":[` + quote(outside) + `]}`,
		`{"prompt":"p","cwd":` + quote(filepath.Join(s.root, "missing")) + `}`,
	} {
		if code, _, raw := s.run(2, body); code != 400 || !strings.Contains(string(raw), `"error"`) {
			t.Fatalf("%s: %d %s", body, code, raw)
		}
	}
}

func TestRun_CwdInsideRootRuns(t *testing.T) {
	s := start(t, opts{root: true})
	real, _ := filepath.EvalSymlinks(s.root)
	// Nothing named: the run starts in the first root.
	if code, out, raw := s.run(2, `{"prompt":"p"}`); code != 200 || !strings.Contains(out.Result, "CWD="+real) {
		t.Fatalf("default cwd: %d %s", code, raw)
	}
	sub := filepath.Join(s.root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, raw := s.run(2, `{"prompt":"p","cwd":`+quote(sub)+`}`); code != 200 || !strings.Contains(out.Result, "CWD="+filepath.Join(real, "sub")) {
		t.Fatalf("cwd inside the root: %d %s", code, raw)
	}
}

func TestRun_NonZeroExitIs502(t *testing.T) {
	s := start(t, opts{claude: claudeScript("", 3)})
	code, out, raw := s.run(2, `{"prompt":"p"}`)
	if code != 502 || out.ExitCode != 3 || out.Stderr != "fake-stderr" || !strings.Contains(out.Result, "PROMPT=p") {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestRun_TimeoutIs504(t *testing.T) {
	s := start(t, opts{claude: claudeScript("3", 0), timeout: time.Second})
	started := time.Now()
	code, _, raw := s.run(2, `{"prompt":"p"}`)
	if code != 504 || time.Since(started) > 2500*time.Millisecond {
		t.Fatalf("%d after %v: %s", code, time.Since(started), raw)
	}
}

func TestRun_StreamIsServerSentEvents(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/accounts/2/run", `{"prompt":"p","stream":true}`)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	frames := sseFrames(t, string(raw))
	if len(frames) < 3 {
		t.Fatalf("too few frames:\n%s", raw)
	}
	// rota speaks first, naming the account and the resolved model, and
	// last, saying how it ended. Every event names its own type.
	first, last := frames[0], frames[len(frames)-1]
	if first.event != "init" || first.data["type"] != "init" || first.data["account"] != 2.0 ||
		first.data["provider"] != "claude" || first.data["model"] == "" {
		t.Fatalf("first frame: %+v", first)
	}
	if last.event != "done" || last.data["type"] != "done" || last.data["exit_code"] != 0.0 ||
		last.data["is_error"] != false || last.data["account"] != 2.0 || last.data["session_id"] != "s-fake" {
		t.Fatalf("last frame: %+v", last)
	}
	var text string
	for _, f := range frames[1 : len(frames)-1] {
		if f.event != f.data["type"] {
			t.Fatalf("event name %q disagrees with type %v", f.event, f.data["type"])
		}
		if f.event == "text" {
			text, _ = f.data["text"].(string)
		}
	}
	// The text event carries what the agent said, which the fake made the
	// argv: the CLI was asked for a stream. The provider's own event stays
	// out unless include_events asks for it.
	if !strings.Contains(text, "--output-format stream-json") {
		t.Fatalf("text event %q lacks the streaming argv:\n%s", text, raw)
	}
	if strings.Contains(string(raw), `"raw":`) {
		t.Fatalf("raw events were not asked for:\n%s", raw)
	}
}

func TestRun_StreamAcceptsNDJSON(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/accounts/2/run", `{"prompt":"p","stream":true}`, "Accept", "application/x-ndjson")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("%d %s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 3 {
		t.Fatalf("too few lines:\n%s", raw)
	}
	var types []string
	for _, line := range lines {
		var ev struct {
			Type string `json:"type"`
			Seq  int    `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
			t.Fatalf("line %q: %v", line, err)
		}
		types = append(types, ev.Type)
	}
	if types[0] != "init" || types[len(types)-1] != "done" {
		t.Fatalf("types %v: want init first and done last", types)
	}
	if strings.Contains(string(raw), "event:") || strings.Contains(string(raw), "data:") {
		t.Fatalf("NDJSON must not carry SSE framing:\n%s", raw)
	}
}

func TestRun_MaxConcurrentSerializesRuns(t *testing.T) {
	// The semaphore in api/server.go withSlot blocks rather than refuses, so
	// two runs against a one-slot server both succeed, one after the other.
	s := start(t, opts{maxConcurrent: 1, claude: claudeScript("0.7", 0)})
	started := time.Now()
	done := make(chan int, 2)
	for range 2 {
		go func() { code, _, _ := s.run(2, `{"prompt":"p"}`); done <- code }()
	}
	a, b := <-done, <-done
	if a != 200 || b != 200 {
		t.Fatalf("codes %d %d", a, b)
	}
	if elapsed := time.Since(started); elapsed < 1400*time.Millisecond {
		t.Fatalf("two 700ms runs finished in %v: they overlapped", elapsed)
	}
}

/* ------------------------------------------------------------ login -- */

func TestLogin_UnknownProviderIs400(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/login", `{"provider":"nope"}`)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "unknown provider") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
}

func TestLogin_GrokReturnsIdUrlKind(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/login", `{"provider":"grok"}`)
	var login struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		URL      string `json:"url"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &login); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v %s", resp.StatusCode, err, raw)
	}
	// An API-key login has no authorization round trip: the URL is where a
	// person gets a key, and the kind says a key is what to paste.
	if len(login.ID) != 6 || login.Provider != "grok" || login.Kind != "apikey" || !strings.HasPrefix(login.URL, "https://console.x.ai") {
		t.Fatalf("%s", raw)
	}
}

func TestLoginFinish_BadIdIs404(t *testing.T) {
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/login/zzzzzz", `{"code":"xai-k"}`)
	if resp.StatusCode != 404 || !strings.Contains(string(raw), `"error"`) {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
}

func TestLoginFinish_ApiKeyAddsAccount(t *testing.T) {
	s := start(t, opts{})
	_, raw := s.do("POST", "/v1/login", `{"provider":"grok"}`)
	id := object(t, raw)["id"].(string)
	resp, raw := s.do("POST", "/v1/login/"+id, `{"code":"xai-second"}`)
	doc := object(t, raw)
	// Ids are never reused, so the third account is 3 whatever came before.
	if resp.StatusCode != 200 || doc["id"] != 3.0 || doc["provider"] != "grok" || doc["status"] != "added" {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	l := s.list()
	if got := l.ids(); len(got) != 3 || got[2] != 3 || l.Accounts[2].Order != 3 {
		t.Fatalf("a new account joins the end of the queue: %+v", l.Accounts)
	}
	// Finishing consumed the login: the same id is unknown afterwards.
	if resp, raw := s.do("POST", "/v1/login/"+id, `{"code":"xai-again"}`); resp.StatusCode != 404 {
		t.Fatalf("a used login id: %d %s", resp.StatusCode, raw)
	}
}

func TestLoginFinish_WrongKeyKeepsThePendingLogin(t *testing.T) {
	s := start(t, opts{})
	_, raw := s.do("POST", "/v1/login", `{"provider":"grok"}`)
	id := object(t, raw)["id"].(string)
	if resp, raw := s.do("POST", "/v1/login/"+id, `{"code":"sk-not-xai"}`); resp.StatusCode != 400 || !strings.Contains(string(raw), "xai-") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	// A typo costs one retry, not a new login.
	if resp, raw := s.do("POST", "/v1/login/"+id, `{"code":"xai-ok"}`); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
}

func TestAuth_OldPathsStillWork(t *testing.T) {
	s := start(t, opts{})
	_, raw := s.do("POST", "/v1/auth", `{"provider":"grok"}`)
	doc := object(t, raw)
	if doc["kind"] != "apikey" {
		t.Fatalf("%s", raw)
	}
	resp, raw := s.do("POST", "/v1/auth/"+doc["id"].(string), `{"code":"xai-old-path"}`)
	if resp.StatusCode != 200 || object(t, raw)["status"] != "added" {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
}
