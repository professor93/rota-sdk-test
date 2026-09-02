package run_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// checked is fake.Claude plus the sign-in check Run consults before
// planning; fake.SignIn on its own would hide the catalog beneath it.
type checked struct {
	*fake.FlavoredCatalog
	signedIn func(a *rota.Account, home string) error
}

func (c checked) SignedIn(a *rota.Account, home string) error { return c.signedIn(a, home) }

// grok is the grok flavor over p with the one model the plan names.
func grok(p *fake.Provider) *fake.FlavoredCatalog {
	c := fake.Flavor(p, "grok")
	c.ModelList, c.EffortList = []rota.Model{{ID: "grok-4.6"}}, nil
	c.DefModel, c.DefEffort = "grok-4.6", ""
	return c
}

func codex(p *fake.Provider) *fake.FlavoredCatalog { return fake.Flavor(p, "codex") }

// cli writes script as the fake binary t-cli, gives the test its own
// registry, and returns a bare provider whose BaseEnv finds that binary.
func cli(t *testing.T, script string) *fake.Provider {
	t.Helper()
	dir := fake.CLI(t, "t-cli", script)
	fake.Registry(t)
	p := fake.New("t-run")
	p.BaseEnv = fake.BaseEnv(dir)
	return p
}

// register puts p in the scoped registry and returns the account to run on.
func register(p rota.Provider) *rota.Account {
	rota.Register(p)
	return rota.NewAccount(7, "t-run", &rota.Token{Access: "tok"})
}

// run is Run with no home, no supplied command and no events writer.
func run(a *rota.Account, spec rota.Spec, lim *rota.Limits) (*rota.Result, error) {
	return rota.Run(context.Background(), a, "", nil, spec, lim, nil)
}

// mark, at the top of a script, leaves a file beside the binary; ran looks
// for it, so a test can prove the CLI never started.
const mark = "printf x > \"$0.ran\"\n"

func ran(t *testing.T) bool {
	t.Helper()
	bin, err := exec.LookPath("t-cli")
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(bin + ".ran")
	return err == nil
}

// argv is the argument list the fake CLI reported back in its result.
func argv(t *testing.T, res *rota.Result, err error) []string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(res.Result, " ARGS=")
	if !ok {
		t.Fatalf("no argv in %q", res.Result)
	}
	return strings.Fields(rest)
}

func TestRun_NilCommandNeedsBaseEnv(t *testing.T) {
	p := cli(t, mark+fake.ClaudeResult(0))
	p.BaseEnv = nil
	a := register(fake.Claude(p))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "BaseEnv") {
		t.Fatalf("err = %v, want ErrInvalidRequest naming BaseEnv", err)
	}
	if res != nil || ran(t) {
		t.Fatalf("nothing may start without a base environment: res=%+v ran=%v", res, ran(t))
	}
}

func TestRun_SuppliedCommandRunsWithOnlyItsEnv(t *testing.T) {
	p := cli(t, `cat >/dev/null
printf '{"type":"result","result":"%s"}\n' "LEAK=$LEAK T=$T_TOKEN"
`)
	t.Setenv("LEAK", "1")
	a := register(fake.Claude(p))
	cmd := &rota.Command{Bin: "t-cli", Env: []string{"T_TOKEN=x"}, BaseEnv: p.BaseEnv}
	res, err := rota.Run(context.Background(), a, "", cmd, rota.Spec{Prompt: "hi"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "LEAK= T=x" {
		t.Fatalf("result = %q, want only the command's own environment", res.Result)
	}
	if slices.Contains(p.Calls(), "launch") {
		t.Fatalf("a supplied command is not staged again: %v", p.Calls())
	}
}

func TestRun_SignInCheckerConsulted(t *testing.T) {
	sentinel := errors.New("t-not-signed-in")
	p := cli(t, mark+fake.ClaudeResult(0))
	a := register(fake.SignIn{Provider: fake.Claude(p), Fn: func(*rota.Account, string) error { return sentinel }})
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if !errors.Is(err, sentinel) || res != nil || ran(t) {
		t.Fatalf("err=%v res=%+v ran=%v, want the check's own error and no run", err, res, ran(t))
	}

	// The refusal above comes before the flavor matters, so fake.SignIn
	// served; a run that goes on needs the catalog too, hence checked.
	called := false
	a = register(checked{FlavoredCatalog: fake.Claude(p), signedIn: func(*rota.Account, string) error {
		called = true
		return nil
	}})
	res, err = run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called || !strings.HasPrefix(res.Result, "STDIN=hi ARGS=") {
		t.Fatalf("called=%v result=%q", called, res.Result)
	}
}

func TestRun_MissingBinaryIsUnsupported(t *testing.T) {
	p := cli(t, fake.ClaudeResult(0))
	p.Command = func(*rota.Account, string) (*rota.Command, error) {
		return &rota.Command{Bin: "t-nope", BaseEnv: p.BaseEnv}, nil
	}
	a := register(fake.Claude(p))
	_, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if !errors.Is(err, rota.ErrUnsupported) || !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("err = %v, want ErrUnsupported naming PATH", err)
	}
}

func TestRun_PromptOnStdinArgvVisibleFieldsFilled(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.ClaudeResult(0))))
	res, err := run(a, rota.Spec{Prompt: "hi", Model: "one", Effort: "high"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Result, "STDIN=hi ARGS=") ||
		!strings.Contains(res.Result, "--model t-model-1") ||
		!strings.Contains(res.Result, "--effort high") {
		t.Fatalf("result = %q", res.Result)
	}
	if res.Model != "t-model-1" || res.Effort != "high" || res.Account != 7 || res.Provider != "t-run" || res.SessionID != "s-fake" {
		t.Fatalf("who ran: %+v", res)
	}
	if res.NumTurns != 1 || res.CostUSD != 0.5 || res.Subtype != "success" || res.IsError || res.ExitCode != 0 {
		t.Fatalf("outcome: %+v", res)
	}
	if res.Stderr != "fake-stderr" || res.DurationMS < 0 || res.Events != nil || res.Truncated {
		t.Fatalf("extras: %+v", res)
	}
}

func TestRun_ResumeLastBecomesContinue(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.ClaudeResult(0))))
	res, err := run(a, rota.Spec{Prompt: "hi", Resume: "last"}, nil)
	args := argv(t, res, err)
	// The claude builder spells continue as -c; --continue is grok's word.
	if !slices.Contains(args, "-c") || slices.Contains(args, "--resume") || slices.Contains(args, "last") {
		t.Fatalf("argv = %v, want -c and no --resume", args)
	}
}

func TestRun_NonZeroExitIsAResultNotAnError(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.ClaudeResult(3))))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatalf("a failing CLI is a result, not an error: %v", err)
	}
	if !res.IsError || res.ExitCode != 3 || !strings.HasPrefix(res.Result, "STDIN=hi ARGS=") {
		t.Fatalf("%+v", res)
	}

	// Nothing on stdout: stderr is the only explanation there is.
	a = register(fake.Claude(cli(t, "cat >/dev/null\necho fake-stderr >&2\nexit 2\n")))
	res, err = run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.ExitCode != 2 || res.Result != "fake-stderr" {
		t.Fatalf("%+v", res)
	}
}

func TestRun_TimeoutKillsAndReturnsResult(t *testing.T) {
	a := register(fake.Claude(cli(t, "exec </dev/null\nsleep 5\n")))
	start := time.Now()
	res, err := run(a, rota.Spec{Prompt: "hi", TimeoutSeconds: 1}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || res == nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("the CLI outlived its timeout: %v", el)
	}
}

func TestRun_ContextCancelKills(t *testing.T) {
	a := register(fake.Claude(cli(t, "exec </dev/null\nsleep 5\n")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	res, err := rota.Run(ctx, a, "", nil, rota.Spec{Prompt: "hi"}, nil, nil)
	if !errors.Is(err, context.Canceled) || res == nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("the CLI outlived its context: %v", el)
	}
}

func TestRun_CwdIsSymlinkResolved(t *testing.T) {
	a := register(fake.Claude(cli(t, `cat >/dev/null
printf '{"type":"result","result":"%s"}\n' "$(pwd -P)"
`)))
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	res, err := run(a, rota.Spec{Prompt: "hi", Cwd: link}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != real {
		t.Fatalf("child ran in %q, want the resolved %q", res.Result, real)
	}
}

func TestRun_HermeticSetsTempConfigDirAndRemovesIt(t *testing.T) {
	a := register(fake.Claude(cli(t, `exec </dev/null
printf '{"type":"result","result":"%s"}\n' "$CLAUDE_CONFIG_DIR"
`)))
	scratch := t.TempDir()
	resolved, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		t.Fatal(err)
	}
	res, err := run(a, rota.Spec{Prompt: "hi", Hermetic: true, ScratchDir: scratch}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Result, resolved+string(filepath.Separator)) {
		t.Fatalf("config dir %q is not under %q", res.Result, resolved)
	}
	if _, err := os.Stat(res.Result); !os.IsNotExist(err) {
		t.Fatalf("the throwaway config dir must be gone: %v", err)
	}
}

func TestRun_BufferedArrayOutputIsParsed(t *testing.T) {
	const doc = `[{"type":"system"},{"type":"result","result":"ANSWER","session_id":"s1","total_cost_usd":0.5}]`
	a := register(fake.Claude(cli(t, fake.Lines(doc))))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "ANSWER" || res.SessionID != "s1" || res.Events != nil {
		t.Fatalf("%+v", res)
	}
	res, err = run(a, rota.Spec{Prompt: "hi", IncludeEvents: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "ANSWER" || len(res.Events) != 2 {
		t.Fatalf("the array's elements are the events: %+v", res)
	}
}

func TestRun_StreamingEventsIncludedOnlyWhenAsked(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.Lines(
		`{"type":"system","session_id":"s1"}`,
		`{"type":"stream_event","event":{}}`,
		`{"type":"result","result":"ANSWER"}`))))
	res, err := run(a, rota.Spec{Prompt: "hi", Stream: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "ANSWER" || res.SessionID != "s1" || res.Events != nil {
		t.Fatalf("%+v", res)
	}
	res, err = run(a, rota.Spec{Prompt: "hi", Stream: true, IncludeEvents: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "ANSWER" || len(res.Events) != 3 {
		t.Fatalf("%+v", res)
	}
}

func TestRun_SpacedJSONStillParsed(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.Lines(`{"type" : "result","result":"ANSWER"}`))))
	// Both readers: the whole-document one and the line scanner.
	for _, stream := range []bool{false, true} {
		res, err := run(a, rota.Spec{Prompt: "hi", Stream: stream}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Result != "ANSWER" {
			t.Fatalf("stream=%v: result = %q", stream, res.Result)
		}
	}
}

func TestRun_MaxEventsTruncates(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.Lines(
		`{"type":"system","subtype":"one"}`,
		`{"type":"system","subtype":"two"}`,
		`{"type":"result","result":"ANSWER"}`))))
	res, err := run(a, rota.Spec{Prompt: "hi", Stream: true, IncludeEvents: true}, &rota.Limits{MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) > 2 || !res.Truncated {
		t.Fatalf("events=%d truncated=%v", len(res.Events), res.Truncated)
	}
}

func TestRun_MaxEventLineTruncates(t *testing.T) {
	a := register(fake.Claude(cli(t, `exec </dev/null
head -c 100000 /dev/zero | tr '\0' x
echo
printf '{"type":"result","result":"ANSWER"}\n'
`)))
	res, err := run(a, rota.Spec{Prompt: "hi", Stream: true}, &rota.Limits{MaxEventLine: 1024})
	if err != nil {
		t.Fatalf("an oversized line is a bound hit, not an error: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("%+v", res)
	}
}

func TestRun_MaxStderrKeepsTheTail(t *testing.T) {
	a := register(fake.Claude(cli(t, `exec </dev/null
head -c 10000 /dev/zero | tr '\0' a >&2
printf TAIL >&2
printf '{"type":"result","result":"ok"}\n'
`)))
	res, err := run(a, rota.Spec{Prompt: "hi"}, &rota.Limits{MaxStderr: 512})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Stderr, "[") || !strings.Contains(res.Stderr, "dropped") ||
		!strings.HasSuffix(res.Stderr, "TAIL") || !res.Truncated {
		t.Fatalf("stderr=%q truncated=%v", res.Stderr, res.Truncated)
	}
}

func TestRun_MaxBufferedOutputTruncates(t *testing.T) {
	a := register(fake.Claude(cli(t, `exec </dev/null
head -c 1048576 /dev/zero | tr '\0' y
echo
printf '{"type":"result","result":"ok"}\n'
`)))
	res, err := run(a, rota.Spec{Prompt: "hi"}, &rota.Limits{MaxBufferedOutput: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatalf("%+v", res)
	}
}

func TestRun_DefaultLimitsLeaveSmallOutputAlone(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.ClaudeResult(0))))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated || res.Stderr != "fake-stderr" {
		t.Fatalf("%+v", res)
	}
}

func TestRun_ScratchFilesAreRemoved(t *testing.T) {
	// grok takes the prompt as a file, so $2 is the scratch file; echoing
	// it back proves the file was there for the run.
	a := register(grok(cli(t, `cat >/dev/null
printf '{"text":"%s","stopReason":"end_turn","sessionId":"s"}\n' "$(cat "$2")"
`)))
	scratch := t.TempDir()
	res, err := run(a, rota.Spec{Prompt: "hi", ScratchDir: scratch}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "hi" || res.SessionID != "s" {
		t.Fatalf("%+v", res)
	}
	left, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("scratch files left behind: %v", left)
	}
}

func TestRun_CodexEventStream(t *testing.T) {
	lines := []string{
		`{"type":"thread.started","thread_id":"t-1"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"CODEX"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":5}}`,
	}
	a := register(codex(cli(t, fake.Lines(lines...))))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "t-1" || res.Result != "CODEX" || !strings.Contains(string(res.Usage), "input_tokens") || res.IsError {
		t.Fatalf("%+v", res)
	}

	a = register(codex(cli(t, fake.Lines(append(lines, `{"type":"turn.failed"}`)...))))
	res, err = run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.Result != "CODEX" {
		t.Fatalf("%+v", res)
	}
}

func TestRun_GrokBufferedShape(t *testing.T) {
	a := register(grok(cli(t, fake.Lines(
		`{"text":"SHAPE","stopReason":"error","sessionId":"01a0","usage":{},"num_turns":1,"total_cost_usd":0.03}`))))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "SHAPE" || !res.IsError || res.SessionID != "01a0" || res.NumTurns != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestRun_KimiProseBecomesResult(t *testing.T) {
	p := cli(t, fake.Lines("just words", "and more"))
	a := register(fake.Flavored{Provider: p, Name_: "kimi"})
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "just words\nand more" || res.Effort != "" {
		t.Fatalf("%+v", res)
	}
}

func TestRun_StructuredOutputCaptured(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.Lines(`{"type":"result","result":"ok","structured_output":{"k":1}}`))))
	res, err := run(a, rota.Spec{Prompt: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "ok" || !strings.Contains(string(res.Structured), `"k":1`) {
		t.Fatalf("%+v structured=%s", res, res.Structured)
	}
}
