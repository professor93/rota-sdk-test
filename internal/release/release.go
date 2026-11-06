// Package release orders rota's versions the way semantic versioning does,
// so a case can name the release that brought what it pins and skip on one
// older: 1.4.0-beta.2 comes before 1.4.0-beta.3, which comes before 1.4.0.
package release

import (
	"strings"
	"testing"
)

// Since skips t unless have, the rota under test, is at least want.
func Since(t testing.TB, have, want string) {
	t.Helper()
	if Older(have, want) {
		t.Skipf("rota %s predates %s, which this pins", have, want)
	}
}

// Older reports whether version a comes before b. A leading v and any build
// metadata are ignored, and a missing number reads as 0. With the numbers
// equal, a pre-release comes before the release itself, and two pre-releases
// compare part by dotted part: numbers by value, a number before a word,
// words as text, and a shorter list first when one begins the other.
func Older(a, b string) bool {
	an, ap := split(a)
	bn, bp := split(b)
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
	case ap == bp || ap == "":
		return false
	case bp == "":
		return true
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := range min(len(as), len(bs)) {
		x, xn := number(as[i])
		y, yn := number(bs[i])
		switch {
		case xn && yn:
			if x != y {
				return x < y
			}
		case xn != yn:
			return xn
		case as[i] != bs[i]:
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

// split is a version's numbers and its pre-release part.
func split(v string) ([]int, string) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, pre, _ := strings.Cut(v, "-")
	var nums []int
	for _, part := range strings.Split(core, ".") {
		n, _ := number(part)
		nums = append(nums, n)
	}
	return nums, pre
}

// number reads a part made of digits alone.
func number(s string) (int, bool) {
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
