//go:build local

// store.Watch and store.WatchEvery are new in 1.3.2, and naming them would
// not build against a module that predates them, so these cases are behind
// the `local` tag, as longtoken/ is: `make local`, or
//
//	GOWORK=/path/go.work go test -tags local ./store
package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/store"
	"rotatest/internal/fake"
)

// watching runs store.Watch over a store in dir for account 1, every few
// milliseconds, and returns what it says and when it is done.
func watching(t *testing.T, ctx context.Context, dir string) (said <-chan string, done <-chan struct{}) {
	t.Helper()
	fake.Restore(t, &store.WatchEvery, 30*time.Millisecond)
	lines, over := make(chan string, 1000), make(chan struct{})
	go func() {
		defer close(over)
		store.Watch(ctx, func() (*store.Store, error) { return store.Open(dir) }, 1, func(m string) { lines <- m })
	}()
	t.Cleanup(func() {
		select {
		case <-over:
		case <-time.After(5 * time.Second):
			t.Error("the watcher never ended")
		}
	})
	return lines, over
}

// until waits for a line containing want.
func until(t *testing.T, said <-chan string, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-said:
			if strings.Contains(m, want) {
				return
			}
		case <-deadline:
			t.Fatalf("the watcher never said %q", want)
		}
	}
}

// launchedStore is a store in a directory whose one claude account has been
// launched on its stored login once, which is what a watcher keeps.
func launchedStore(t *testing.T) (dir, home string) {
	t.Helper()
	personsWorld(t)
	echoClaude(t)
	dir = t.TempDir()
	writeIn(t, filepath.Join(dir, "accounts.json"), fmt.Sprintf(`{"accounts":[{"id":1,"provider":"claude","email":"c@x",
 "token":{"accessToken":"A0","refreshToken":"R0","expiresAt":%d}}]}`, hoursFromNow(1).UnixMilli()))
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Run(ctx, s.Find(1), rota.Spec{Prompt: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return dir, s.Home(s.Find(1))
}

// A watcher keeps looking for as long as anything runs in the home, and
// goes by itself, saying so, once nothing does.
func TestWatch_GoesOnceNothingRunsInTheHome(t *testing.T) {
	keptHome(t, "1.3.2")
	dir, home := launchedStore(t)
	closeWindow := liveWindow(t, home)
	said, done := watching(t, ctx, dir)
	until(t, said, "watching "+home)
	time.Sleep(200 * time.Millisecond) // several looks
	select {
	case <-done:
		t.Fatal("it keeps watching while a window is open")
	default:
	}
	closeWindow()
	until(t, said, "nothing runs in its home any more")
	<-done
}

// It goes when the account dies, and on its context, saying which.
func TestWatch_GoesWhenTheAccountDiesOrItIsStopped(t *testing.T) {
	keptHome(t, "1.3.2")
	dir, home := launchedStore(t)
	liveWindow(t, home)
	said, done := watching(t, ctx, dir)
	until(t, said, "watching")
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Find(1).Dead = true
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	until(t, said, "no longer keeps its login in its home")
	<-done

	stop, cancel := context.WithCancel(ctx)
	dir, home = launchedStore(t)
	liveWindow(t, home)
	said, done = watching(t, stop, dir)
	until(t, said, "watching")
	cancel()
	until(t, said, "stopped by a signal")
	<-done
	if _, err := os.Stat(filepath.Join(home, ".rota-watch.lock")); err != nil {
		t.Fatalf("the lock file stays, for the next watcher to take: %v", err)
	}
}
