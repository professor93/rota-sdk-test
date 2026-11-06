package release_test

import (
	"testing"

	"rotatest/internal/release"
)

// TestOlder_ReadsPreReleasesAsSemanticVersioningDoes: each pair, and the
// pair the other way round.
func TestOlder_ReadsPreReleasesAsSemanticVersioningDoes(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.3.0", "1.3.0", false},
		{"1.3.0", "1.3.2", true},
		{"1.3.2", "1.4.0-beta.1", true},
		{"1.4.0-beta.1", "1.4.0-beta.2", true},
		{"1.4.0-beta.2", "1.4.0-beta.3", true},
		{"1.4.0-beta.3", "1.4.0", true},
		{"1.4.0-beta.9", "1.4.0-beta.10", true},
		{"1.4.0-beta.10", "1.4.0-rc.1", true},
		{"1.4.0-alpha", "1.4.0-alpha.1", true},
		{"1.4.0-alpha.1", "1.4.0-alpha.beta", true},
		{"1.4.0-alpha.beta", "1.4.0-beta", true},
		{"1.4.0-beta.01.9", "1.4.0-beta.1.10", true},
		{"1.4.0", "1.4.1", true},
		{"1.9.0", "1.10.0", true},
		{"1.10.0", "2.0.0-beta.1", true},
		{"1.4.0-beta.3", "1.4.0-beta.3", false},
		{"v1.4.0-beta.3", "1.4.0-beta.3", false},
		{"1.4.0-beta.3+abc", "1.4.0-beta.3", false},
		{"1.4.0+abc", "1.4.0", false},
		{"1.4", "1.4.0", false},
	} {
		if got := release.Older(c.a, c.b); got != c.want {
			t.Errorf("Older(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if c.want {
			if release.Older(c.b, c.a) {
				t.Errorf("Older(%q, %q) = true, and the other way round as well", c.b, c.a)
			}
		}
	}
}

// TestSince_SkipsOnlyARotaOlderThanTheRelease.
func TestSince_SkipsOnlyARotaOlderThanTheRelease(t *testing.T) {
	for _, c := range []struct {
		have, want string
		skip       bool
	}{
		{"1.3.0", "1.4.0-beta.1", true},
		{"1.4.0-beta.2", "1.4.0-beta.3", true},
		{"1.4.0-beta.3", "1.4.0-beta.3", false},
		{"1.4.0", "1.4.0-beta.3", false},
		{"1.5.0", "1.4.0-beta.3", false},
	} {
		ran := false
		t.Run(c.have+"_since_"+c.want, func(t *testing.T) {
			release.Since(t, c.have, c.want)
			ran = true
		})
		if ran == c.skip {
			t.Errorf("Since(%q, %q) skipped=%v, want %v", c.have, c.want, !ran, c.skip)
		}
	}
}
