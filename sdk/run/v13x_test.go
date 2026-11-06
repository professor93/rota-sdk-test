package run_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// Since 1.3.1 a CLI is asked to stop before it is killed: on Linux and macOS
// a cancelled run, a timeout or a run over a bound sends SIGTERM to the CLI's
// process group and SIGKILL three seconds later to whatever has not gone.
// Claude Code rotates its refresh token, and a process killed between the
// provider accepting a refresh and the store saving it loses the login.

// stopsAsked skips where the gentle stop is not promised: before 1.3.1, and
// on Windows, which is unchanged.
func stopsAsked(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows is unchanged: the CLI is killed")
	}
	if releasedBefore(rota.Version, "1.3.1") {
		t.Skipf("rota %s predates 1.3.1, which this pins", rota.Version)
	}
}

// A CLI that handles SIGTERM finishes and exits with its own status, and the
// run returns as soon as its process group has gone rather than after the
// grace. A timed-out run still returns the timeout.
func TestRun_TimeoutAsksTheCLIToStopAndKeepsItsExitCode(t *testing.T) {
	stopsAsked(t)
	a := register(fake.Claude(cli(t, `trap 'exit 7' TERM
cat >/dev/null
while :; do sleep 0.1; done
`)))
	start := time.Now()
	res, err := run(a, rota.Spec{Prompt: "hi", TimeoutSeconds: 2}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || res == nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if res.ExitCode != 7 {
		t.Fatalf("the exit code of a CLI that handles SIGTERM is its own: %d", res.ExitCode)
	}
	if el := time.Since(start); el >= 4500*time.Millisecond {
		t.Fatalf("a CLI that stops when asked costs no grace: %v", el)
	}
}

// One that does not stop when asked holds its run for the grace, and is then
// killed with everything in its group.
func TestRun_ACLIThatIgnoresTheAskIsKilledAfterTheGrace(t *testing.T) {
	stopsAsked(t)
	ready := filepath.Join(t.TempDir(), "ready")
	a := register(fake.Claude(cli(t, `trap '' TERM
cat >/dev/null
: > `+ready+`
sleep 30
`)))
	ctx := cancelWhen(t, ready)
	res, err := rota.Run(ctx, a, "", nil, rota.Spec{Prompt: "hi"}, nil, nil)
	asked := time.Since(cancelledAt(ctx))
	if !errors.Is(err, context.Canceled) || res == nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if asked < 2800*time.Millisecond {
		t.Fatalf("killed before the grace was over: %v after the cancel", asked)
	}
	if asked > 15*time.Second {
		t.Fatalf("never killed: %v", asked)
	}
}

// The ask goes to the whole process group: a helper the CLI started — the
// one in the middle of a refresh, say — is asked too, and has its chance to
// finish.
func TestRun_HelpersInTheCLIsGroupAreAskedToo(t *testing.T) {
	stopsAsked(t)
	dir := t.TempDir()
	mark, ready := filepath.Join(dir, "helper-stopped"), filepath.Join(dir, "ready")
	a := register(fake.Claude(cli(t, `trap 'wait; exit 3' TERM
cat >/dev/null
sh -c 'trap "echo asked > `+mark+`; exit 0" TERM; : > `+ready+`; while :; do sleep 0.1; done' &
wait
`)))
	res, err := rota.Run(cancelWhen(t, ready), a, "", nil, rota.Spec{Prompt: "hi"}, nil, nil)
	if !errors.Is(err, context.Canceled) || res == nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if body, err := os.ReadFile(mark); err != nil || strings.TrimSpace(string(body)) != "asked" {
		t.Fatalf("the helper was asked to stop and finished: %q %v", body, err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("the CLI's own status: %d", res.ExitCode)
	}
}

// A run rota stopped for a bound returns what 1.3.0 returned — no error,
// exit code -1 and is_error — whatever the CLI exits with once asked.
// Claude Code handles SIGTERM itself, so exiting 0 is the common case.
func TestRun_ABoundHitIsMinusOneWhateverTheCLIExitsWith(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the CLI is a shell script")
	}
	a := register(fake.Claude(cli(t, `cat >/dev/null
trap 'exit 0' TERM
head -c 1048576 /dev/zero | tr '\0' y
echo
while :; do sleep 0.1; done
`)))
	start := time.Now()
	res, err := run(a, rota.Spec{Prompt: "hi"}, &rota.Limits{MaxBufferedOutput: 4096})
	if err != nil {
		t.Fatalf("a bound hit is not an error: %v", err)
	}
	if !res.Truncated || !res.IsError || res.ExitCode != -1 {
		t.Fatalf("truncated=%v is_error=%v exit=%d", res.Truncated, res.IsError, res.ExitCode)
	}
	if el := time.Since(start); el > 15*time.Second {
		t.Fatalf("the CLI was never stopped: %v", el)
	}
}

// cancelWhen is a context cancelled once path exists, which a script makes
// after its traps are in place, so the ask never reaches a shell still
// starting on a loaded machine. It gives up waiting after twenty seconds.
func cancelWhen(t *testing.T, path string) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	go func() {
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(path); err == nil {
				break
			}
		}
		cancel(cancelled(time.Now()))
	}()
	return ctx
}

// cancelled is the cause cancelWhen gives: when it cancelled.
type cancelled time.Time

func (c cancelled) Error() string { return "cancelled at " + time.Time(c).String() }

// cancelledAt is when cancelWhen cancelled ctx.
func cancelledAt(ctx context.Context) time.Time {
	var at cancelled
	if errors.As(context.Cause(ctx), &at) {
		return time.Time(at)
	}
	return time.Now()
}

// releasedBefore reports whether a dotted version reads before another,
// number by number, with a missing or non-numeric part as 0.
func releasedBefore(a, b string) bool {
	as, bs := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// failingWriter refuses every event, as a client that went away does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the reader went away") }

// An events writer that fails while the CLI runs stops the run the way a
// bound does: no error, exit code -1 and is_error, whatever the CLI exits
// with once asked. A CLI that stops when asked costs no grace.
func TestRun_AFailedEventsWriterIsMinusOneWhateverTheCLIExitsWith(t *testing.T) {
	stopsAsked(t)
	a := register(fake.Claude(cli(t, `trap 'exit 0' TERM
cat >/dev/null
printf '{"type":"system","subtype":"init","session_id":"s"}\n'
while :; do sleep 0.1; done
`)))
	start := time.Now()
	res, err := rota.Run(context.Background(), a, "", nil, rota.Spec{Prompt: "hi", Stream: true}, nil, failingWriter{})
	if err != nil {
		t.Fatalf("not an error: %v", err)
	}
	if !res.IsError || res.ExitCode != -1 {
		t.Fatalf("is_error=%v exit=%d", res.IsError, res.ExitCode)
	}
	if el := time.Since(start); el >= 2800*time.Millisecond {
		t.Fatalf("a CLI that stops when asked costs no grace: %v", el)
	}
}
