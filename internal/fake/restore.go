// Package fake is what the suite stands in for the world with: providers,
// the OAuth and usage endpoints, the vendor CLIs, and the clock. Everything
// here is reachable through rota's own injection points, so the SDK under
// test is the published one, unmodified.
package fake

import (
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
)

// Restore sets *p to v for the rest of the test and puts the old value back
// at cleanup. Every package-level knob in rota goes through this so a test
// never leaks its settings into the next.
func Restore[T any](t testing.TB, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// Clock pins rota.Now to at and returns a function that moves it forward.
func Clock(t testing.TB, at time.Time) (advance func(d time.Duration)) {
	t.Helper()
	now := at
	Restore(t, &rota.Now, func() time.Time { return now })
	return func(d time.Duration) { now = now.Add(d) }
}

// Registry gives the test its own copy of the provider registry: every
// builtin is carried over, and anything the test registers disappears with
// it. rota has no Unregister, so this is how a fake stays scoped.
func Registry(t testing.TB) *rota.Registry {
	t.Helper()
	old := rota.DefaultRegistry
	fresh := rota.NewRegistry()
	fresh.Default = old.Default
	for _, name := range old.Providers() {
		p, err := old.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		fresh.Register(p)
	}
	Restore(t, &rota.DefaultRegistry, fresh)
	return fresh
}
