// Package live_test runs the suite against the accounts in ~/.rota. It is
// opt-in: without ROTA_LIVE every test skips, so a plain go test never
// touches a real token. Nothing here copies a token out of the store or
// refreshes one directly; every network step goes through the store, the
// way an application would.
package live_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/rotation"
	"github.com/professor93/rota/sessions"
	"github.com/professor93/rota/store"
	"github.com/professor93/rota/wire"
)

func skipUnlessLive(t *testing.T) {
	t.Helper()
	if os.Getenv("ROTA_LIVE") == "" {
		t.Skip("set ROTA_LIVE=1 to run against ~/.rota")
	}
}

// open takes the store lock for one test and gives it back at cleanup. A
// test that runs an agent has the lock released for it by Run; Close after
// that is a no-op.
func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open("")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLiveStore_OpensAndListsAccounts(t *testing.T) {
	skipUnlessLive(t)
	s := open(t)
	if len(s.Accounts) == 0 {
		t.Fatal("the store has no accounts; log one in first")
	}
	for _, a := range s.Accounts {
		d := wire.Describe(a)
		// wire.Account carries no label of its own; the label is the SDK's.
		if d.Email == "" && a.Label() == "" {
			t.Errorf("#%d %s: no email and no label", a.ID, a.Provider)
		}
		t.Logf("#%d %s %s status=%s order=%d percent=%.1f", d.ID, d.Provider, a.Label(), d.Status, d.Order, d.Percent)
	}
}

func TestLiveRefresh_ClaudeQuotaHasWindows(t *testing.T) {
	skipUnlessLive(t)
	s := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	before := time.Now()
	for _, err := range s.Refresh(ctx, true) {
		t.Logf("refresh: %v", err)
	}
	for _, a := range s.Accounts {
		if a.Provider != "claude" {
			continue
		}
		if a.Dead {
			t.Logf("#%d needs re-auth; the store skips it by contract", a.ID)
			continue
		}
		if a.Quota == nil {
			t.Errorf("#%d: no quota after a forced refresh", a.ID)
			continue
		}
		if !hasWindow(a.Quota, "5h") {
			t.Errorf("#%d: quota has no 5h window: %+v", a.ID, a.Quota.Windows)
		}
		at := time.UnixMilli(a.QuotaAt)
		if at.Before(before.Add(-time.Minute)) {
			t.Errorf("#%d: QuotaAt %s is older than a minute", a.ID, at)
		}
		for _, w := range a.Quota.Windows {
			t.Logf("#%d %s %.1f%% resets %s", a.ID, w.Name, w.Percent, wire.Countdown(w.ResetsAt))
		}
	}
}

func hasWindow(q *rota.Quota, name string) bool {
	for _, w := range q.Windows {
		if w.Name == name {
			return true
		}
	}
	return false
}

func TestLivePick_AgreesWithChoose(t *testing.T) {
	skipUnlessLive(t)
	s := open(t)
	for _, a := range rotation.Queue(s.Accounts) {
		if s.Busy(a) {
			t.Skipf("#%d is running; Choose steps past busy accounts and Pick does not", a.ID)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Choose first: it refreshes the readings Pick then decides from.
	chosen, cerr := rotation.Choose(ctx, s, 0)
	picked, perr := rotation.Pick(s.Accounts)
	switch {
	case cerr != nil && perr != nil:
		if !errors.Is(cerr, rotation.ErrNone) || !errors.Is(perr, rotation.ErrNone) {
			t.Fatalf("choose=%v pick=%v", cerr, perr)
		}
		t.Logf("both refuse: %v", perr)
	case cerr != nil || perr != nil:
		t.Fatalf("disagree: choose=%v/%v pick=%v/%v", chosen, cerr, picked, perr)
	case chosen.ID != picked.ID:
		t.Fatalf("choose #%d, pick #%d", chosen.ID, picked.ID)
	default:
		t.Logf("both name #%d %s", chosen.ID, chosen)
	}
}

func TestLiveRun_EachAccountAnswersASmallPrompt(t *testing.T) {
	skipUnlessLive(t)
	// Sequential on purpose: two runs would race for the same CLI homes.
	for _, id := range []int{1, 2, 3, 8} {
		t.Run(fmt.Sprintf("id%d", id), func(t *testing.T) { runOne(t, id) })
	}
}

func runOne(t *testing.T, id int) {
	s := open(t)
	a := s.Find(id)
	if a == nil {
		t.Logf("#%d: no such account, skipped", id)
		return
	}
	spec := rota.Spec{Prompt: "Reply with exactly the word OK and nothing else.", TimeoutSeconds: 180}
	// codex refuses to run outside a git repository unless told not to
	// check, and this module's directory is not one. The field is codex's
	// own, so it is set only for codex.
	if a.Provider == "codex" {
		spec.SkipGitRepoCheck = true
	}
	res, err := s.Run(context.Background(), a, spec, nil, io.Discard)
	if err != nil {
		t.Fatalf("#%d %s: %v", id, a, err)
	}
	t.Logf("#%d %s model=%s cost=%.4f duration=%dms", id, a, res.Model, res.CostUSD, res.DurationMS)
	if res.IsError || res.ExitCode != 0 {
		t.Fatalf("#%d: is_error=%v exit=%d stderr=%q", id, res.IsError, res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Result, "OK") {
		t.Fatalf("#%d: result %q lacks OK", id, res.Result)
	}
	if (a.Provider == "claude" || a.Provider == "codex") && res.SessionID == "" {
		t.Fatalf("#%d %s: no session id", id, a.Provider)
	}
}

func TestLiveSessions_ScanListsSomething(t *testing.T) {
	skipUnlessLive(t)
	s := open(t)
	// The second argument is how many recent sessions to keep per account.
	rep := sessions.Scan(s, 10)
	for _, n := range rep.Notes {
		t.Logf("note: %s", n)
	}
	if len(rep.Instances) == 0 && len(rep.Sessions) == 0 {
		t.Skip("nothing running and no sessions found; run the suite once first")
	}
	for _, in := range rep.Instances {
		t.Logf("instance: %s #%d pid=%d dir=%s", in.Kind, in.Account, in.PID, in.Dir)
	}
	for _, ss := range rep.Sessions {
		t.Logf("session: %s #%d %s at %s", ss.Provider, ss.Account, ss.ID, ss.At.Format(time.RFC3339))
	}
}
