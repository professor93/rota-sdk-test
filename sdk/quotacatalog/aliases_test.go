package quotacatalog_test

import (
	"testing"

	rota "github.com/professor93/rota/lib"
)

// Found against 1.0.0: the catalog copied its models but not their alias
// slices, so one caller's write renamed an alias for every later caller.
// Fixed in 1.0.1.
func TestModels_CopiesAliasesToo(t *testing.T) {
	for _, name := range rota.Providers() {
		first := rota.Models(name)
		for i := range first {
			if len(first[i].Aliases) > 0 {
				first[i].Aliases[0] = "tampered"
			}
		}
		for _, m := range rota.Models(name) {
			for _, alias := range m.Aliases {
				if alias == "tampered" {
					t.Fatalf("%s: an alias written by one caller reached the next", name)
				}
			}
		}
	}
}
