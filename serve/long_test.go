// These pin the long-lived token from outside the binary: the login that
// asks for one, and what a launch does once an account holds one.
//
// Nothing here names a field the pinned module does not have — the token is
// seeded as raw JSON and read back through the CLI's environment — so the
// package still compiles against the published version and each test simply
// skips there.

package serve_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// tailScript is a claude CLI that answers with the last four characters of
// the token it was given, and never the token. Four characters are enough to
// say which credential arrived and not enough to be one.
const tailScript = `cat >/dev/null
tok="$CLAUDE_CODE_OAUTH_TOKEN"
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"s-fake","result":"TAIL=%s","num_turns":1,"total_cost_usd":0.01}\n' "${tok#"${tok%????}"}"
`

// longSeed is a store whose only account is claude, holding a long-lived
// token that expires at the given moment (zero for none).
func longSeed(until time.Time, extra string) string {
	long := ""
	if !until.IsZero() {
		long = fmt.Sprintf(`"long":{"accessToken":"long-token-WXYZ","expiresAt":%d},`, until.UnixMilli())
	}
	return fmt.Sprintf(`{"ordered":true,"accounts":[
	 {"id":1,"provider":"claude","email":"c@x","uuid":"u1","order":1,"quotaAt":%d,
	  "quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},%s%s
	  "token":{"accessToken":"short-token-ABCD","refreshToken":"r","expiresAt":0}}]}`,
		time.Now().UnixMilli(), long, extra)
}

func TestLoginCanAskForALongLivedToken(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{})
	resp, raw := s.do("POST", "/v1/login", `{"provider":"claude","long":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	doc := object(t, raw)
	if doc["long"] != true {
		t.Fatalf("the reply must say which login this is: %s", raw)
	}
	url, _ := doc["url"].(string)
	if !strings.Contains(url, "scope=user%3Ainference&") {
		t.Fatalf("a long login asks for inference only: %s", url)
	}
	// The ordinary login is unchanged.
	_, raw = s.do("POST", "/v1/login", `{"provider":"claude"}`)
	if url, _ := object(t, raw)["url"].(string); !strings.Contains(url, "user%3Aprofile") {
		t.Fatalf("ordinary login: %s", url)
	}
}

func TestARunUsesTheLongTokenWhileItLasts(t *testing.T) {
	since(t, "1.2.0")
	for _, c := range []struct {
		what  string
		until time.Time
		want  string
	}{
		{"a year left", time.Now().Add(300 * 24 * time.Hour), "TAIL=WXYZ"},
		{"an hour left", time.Now().Add(time.Hour), "TAIL=ABCD"},
		{"expired", time.Now().Add(-time.Hour), "TAIL=ABCD"},
		{"none at all", time.Time{}, "TAIL=ABCD"},
	} {
		s := start(t, opts{accounts: longSeed(c.until, ""), claude: tailScript})
		code, out, raw := s.run(1, `{"prompt":"hi"}`)
		if code != 200 {
			t.Fatalf("%s: %d %s", c.what, code, raw)
		}
		if !strings.Contains(out.Result, c.want) {
			t.Fatalf("%s: answered %q, want %q", c.what, out.Result, c.want)
		}
		if strings.Contains(string(raw), "long-token-WXYZ") {
			t.Fatalf("%s: the token itself travelled back: %s", c.what, raw)
		}
	}
}

func TestADeadAccountRunsWhenNamedIfItHasALongToken(t *testing.T) {
	since(t, "1.2.0")
	s := start(t, opts{
		accounts: longSeed(time.Now().Add(300*24*time.Hour), `"dead":true,"deadReason":"invalid_grant: reused",`),
		claude:   tailScript,
	})
	code, out, raw := s.run(1, `{"prompt":"hi"}`)
	if code != 200 {
		t.Fatalf("named by id: %d %s", code, raw)
	}
	if !strings.Contains(out.Result, "TAIL=WXYZ") {
		t.Fatalf("result = %q", out.Result)
	}
	// The rotation still passes it by: a dead account is not one to be
	// handed work without being asked for.
	if code, _, raw := s.run(0, `{"prompt":"hi"}`); code == 200 {
		t.Fatalf("the rotation must not choose it: %d %s", code, raw)
	}
	// And the listing keeps saying the login is dead, whatever runs.
	_, raw = s.do("GET", "/v1/accounts", "")
	if !strings.Contains(string(raw), `"status":"reauth"`) {
		t.Fatalf("status: %s", raw)
	}
}

func TestTheListingCarriesTheDateAndNeverTheToken(t *testing.T) {
	since(t, "1.2.0")
	until := time.Now().Add(300 * 24 * time.Hour)
	s := start(t, opts{accounts: longSeed(until, ""), claude: tailScript})
	resp, raw := s.do("GET", "/v1/accounts", "")
	if resp.StatusCode != 200 || strings.Contains(string(raw), "long-token-WXYZ") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"long_until":"`+until.UTC().Format(time.RFC3339)) {
		t.Fatalf("long_until missing or wrong: %s", raw)
	}

	// Forgetting it is a setting like any other, and only "forget" is one.
	if resp, raw := s.do("PATCH", "/v1/accounts/1", `{"long":"drop"}`); resp.StatusCode != 400 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	if resp, raw := s.do("PATCH", "/v1/accounts/1", `{"long":"forget"}`); resp.StatusCode != 200 ||
		strings.Contains(string(raw), "long_until") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	// And the run is back on the ordinary token.
	if _, out, raw := s.run(1, `{"prompt":"hi"}`); !strings.Contains(out.Result, "TAIL=ABCD") {
		t.Fatalf("result = %q %s", out.Result, raw)
	}
}
