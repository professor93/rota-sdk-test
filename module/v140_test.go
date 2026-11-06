// These pin what rota 1.4.0-beta.2 promised about its modules, as a
// program that imports rota sees them through the go command: the program
// is a module of its own, github.com/professor93/rota/cmd/rota, and the
// root — the SDK and the server — still requires nothing, so a program
// importing rota/lib never gains what the rota program comes to need.
//
// The go command is asked with the module proxy off, so nothing here
// reaches the network: it answers from this module's requirements, the
// workspace when there is one, and the module cache.

package module_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	rota "github.com/professor93/rota/lib"
)

const (
	root    = "github.com/professor93/rota"
	program = "github.com/professor93/rota/cmd/rota"
)

// TestModule_TheRootRequiresNothing: the root's go.mod has no require at
// all. It never had one; from 1.4.0-beta.2 it is a promise, because the
// program that does need other modules is no longer in it.
func TestModule_TheRootRequiresNothing(t *testing.T) {
	gomod := goList(t, "-m", "-f", "{{.GoMod}}", root)
	raw, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatalf("the root's go.mod: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "require" {
			t.Fatalf("%s requires something:\n%s", gomod, raw)
		}
	}
}

// TestModule_TheProgramIsAModuleOfItsOwn: the rota program's package is not
// in the root module. Whether it resolves at all depends on whether this
// module requires the program's module, which it need not; when it does,
// it is that module's.
func TestModule_TheProgramIsAModuleOfItsOwn(t *testing.T) {
	gateRelease(t, "1.4.0-beta.2")
	got := goList(t, "-e", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", program)
	if got == root {
		t.Fatalf("%s is still in the root module", program)
	}
	if got != "" && got != program {
		t.Fatalf("%s is in %s, want its own module", program, got)
	}
}

// goList runs go list with the given arguments, offline, and returns what
// it printed, trimmed.
func goList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// gateRelease skips unless the module under test is at least version,
// reading pre-releases as releases do: 1.4.0-beta.2 comes before
// 1.4.0-beta.3, which comes before 1.4.0.
func gateRelease(t *testing.T, version string) {
	t.Helper()
	if releaseBefore(rota.Version, version) {
		t.Skipf("rota %s predates %s, which this pins", rota.Version, version)
	}
}

// releaseBefore reports whether version a comes before b by semantic
// versioning: the numbers first, then a release after any pre-release of
// it, then the pre-release's dotted parts, numbers by value and below
// words.
func releaseBefore(a, b string) bool {
	an, ap := splitRelease(a)
	bn, bp := splitRelease(b)
	for i := range max(len(an), len(bn)) {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			return x < y
		}
	}
	switch {
	case ap == "" || ap == bp:
		return false
	case bp == "":
		return true
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := range min(len(as), len(bs)) {
		if as[i] == bs[i] {
			continue
		}
		x, xn := releaseNumber(as[i])
		y, yn := releaseNumber(bs[i])
		switch {
		case xn && yn:
			return x < y
		case xn != yn:
			return xn
		}
		return as[i] < bs[i]
	}
	return len(as) < len(bs)
}

// splitRelease is a version's numbers and its pre-release part, without a
// leading v or any build metadata.
func splitRelease(v string) ([]int, string) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, pre, _ := strings.Cut(v, "-")
	var nums []int
	for _, part := range strings.Split(core, ".") {
		n, _ := releaseNumber(part)
		nums = append(nums, n)
	}
	return nums, pre
}

// releaseNumber reads a part made of digits alone.
func releaseNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
