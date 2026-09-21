// These pin rota.Start and the Session it returns: a run that stays open
// for more messages.

package session_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	rota "github.com/professor93/rota/lib"
	"rotatest/internal/fake"
)

// The few helpers a session test needs, as sdk/run keeps them: a constrained
// package cannot reach another package's test files, and these are shorter
// to repeat than to share.

// cli writes script as the fake binary t-cli, gives the test its own
// registry, and returns a bare provider whose BaseEnv finds that binary.
func cli(t *testing.T, script string) *fake.Provider {
	t.Helper()
	dir := fake.CLI(t, "t-cli", script)
	fake.Registry(t)
	p := fake.New("t-session")
	p.BaseEnv = fake.BaseEnv(dir)
	return p
}

// register puts p in the scoped registry and returns the account to run on.
func register(p rota.Provider) *rota.Account {
	rota.Register(p)
	return rota.NewAccount(7, "t-session", &rota.Token{Access: "tok"})
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

// Start keeps the CLI open: the prompt is the first message and Send is every
// one after it, in the same conversation. What became of each arrives on
// Notices — accepted when it reached the CLI, answered when the CLI had its
// turn, and idle when nothing is left waiting.
func TestStart_TakesMoreMessagesAndReportsThem(t *testing.T) {
	a := register(fake.Claude(cli(t, fake.Echo())))
	var events bytes.Buffer
	s, err := rota.Start(context.Background(), a, "", nil,
		rota.Spec{Prompt: "one", Input: true, Stream: true}, nil, &events)
	if err != nil {
		t.Fatal(err)
	}

	// A consumer must drain the notices or they are dropped, so they are
	// taken from the moment there is a session to take them from.
	var mu sync.Mutex
	var notices []rota.Notice
	read := make(chan struct{})
	go func() {
		defer close(read)
		for n := range s.Notices() {
			mu.Lock()
			notices = append(notices, n)
			mu.Unlock()
		}
	}()

	id, err := s.Send("two")
	if err != nil || id == "" {
		t.Fatalf("a running session takes another message: %q %v", id, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close says there is nothing more: %v", err)
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.IsError {
		t.Fatalf("the CLI finished its turn and exited: %+v", res)
	}
	<-read

	if out := events.String(); !strings.Contains(out, "echo: one") || !strings.Contains(out, "echo: two") {
		t.Fatalf("both messages are answered in the same run, and every line is copied out:\n%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	accepted, answered := false, false
	for _, n := range notices {
		if n.ID == id {
			accepted = accepted || n.Kind == "accepted"
			answered = answered || n.Kind == "answered"
		}
		if n.Kind == "failed" {
			t.Fatalf("nothing failed: %+v", notices)
		}
	}
	if !accepted || !answered {
		t.Fatalf("the message is accepted and then answered, by id: %+v", notices)
	}
	if last := notices[len(notices)-1]; last.Kind != "idle" {
		t.Fatalf("a session with nothing left waiting says so last: %+v", notices)
	}
}

// The two shapes are told apart before anything is spent: a session is a
// stream, and a one-shot run is not a session.
func TestStart_RefusesInputWithoutStreamAndRunRefusesInput(t *testing.T) {
	a := register(fake.Claude(cli(t, mark+fake.Echo())))

	s, err := rota.Start(context.Background(), a, "", nil,
		rota.Spec{Prompt: "one", Input: true}, nil, nil)
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "stream") {
		t.Fatalf("err = %v, want ErrInvalidRequest naming stream", err)
	}
	if s != nil || ran(t) {
		t.Fatalf("and nothing started: session=%v ran=%v", s != nil, ran(t))
	}

	res, err := rota.Run(context.Background(), a, "", nil,
		rota.Spec{Prompt: "one", Input: true}, nil, nil)
	if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "Start") {
		t.Fatalf("err = %v, want ErrInvalidRequest naming Start", err)
	}
	if res != nil || ran(t) {
		t.Fatalf("and nothing started: res=%+v ran=%v", res, ran(t))
	}
}
