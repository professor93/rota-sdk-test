package spec_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// Found against 1.0.0: a caller-named settings or mcp_config file was
// opened and vetted before its path was checked against the roots, so a
// confined caller could learn whether a file outside the roots existed and
// whether it carried a denied key. Fixed in 1.0.1: the roots refuse first.
func TestSpecCheck_RootsRefuseBeforeAnyFileIsRead(t *testing.T) {
	fake.Registry(t)
	rota.Register(fake.Claude(fake.New("t-rf")))
	root := t.TempDir()
	outside := t.TempDir()
	missing := filepath.Join(outside, "missing.json")
	denied := filepath.Join(outside, "settings.json")
	if err := os.WriteFile(denied, []byte(`{"env":{"X":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lim := &rota.Limits{Roots: []string{root}}
	quote := func(p string) json.RawMessage { return json.RawMessage(strconv.Quote(p)) }
	for _, tc := range []struct {
		name string
		spec rota.Spec
	}{
		{"missing settings", rota.Spec{Prompt: "p", Settings: quote(missing)}},
		{"denied settings", rota.Spec{Prompt: "p", Settings: quote(denied)}},
		{"missing mcp", rota.Spec{Prompt: "p", MCPConfig: []json.RawMessage{quote(missing)}}},
		{"denied mcp", rota.Spec{Prompt: "p", MCPConfig: []json.RawMessage{quote(denied)}}},
	} {
		if err := tc.spec.Check("t-rf", lim); !errors.Is(err, rota.ErrOutsideRoots) {
			t.Fatalf("%s: want only the roots' refusal, got %v", tc.name, err)
		}
	}
}
