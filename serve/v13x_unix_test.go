//go:build unix

package serve_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Since 1.3.2 an interactive `rota run` on a claude account's own login
// leaves a watcher behind, `rota serve watch <id>`, which makes the look
// `rota list` starts with every five minutes for as long as anything runs in
// the account's home. It keeps to one watcher a home through
// .rota-watch.lock there, says what it did in watch.log in the store, and
// goes on SIGTERM or SIGHUP. Windows starts none.

// keptClaude is a store whose one claude account keeps its login in the home
// rota made for it, and whose last launch there took the stored route: what
// a watcher has something to keep for. The home is made, as a launch would
// have made it.
func keptClaude(t *testing.T) (store, home string) {
	t.Helper()
	now := time.Now()
	store = storeOf(t, fmt.Sprintf(`{"ordered":true,"accounts":[
 {"id":1,"provider":"claude","email":"c@x","order":1,"quotaAt":%d,
  "quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
  "extra":{"route":"stored"},
  "token":{"accessToken":"A","refreshToken":"R","expiresAt":%d}}]}`, now.UnixMilli(), now.Add(time.Hour).UnixMilli()))
	home = filepath.Join(store, "homes", "claude-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return store, home
}

// watched is a watcher process the test started.
type watched struct {
	cmd   *exec.Cmd
	ended chan struct{} // closed once it has been waited for
	err   error         // how it ended, once ended is closed
}

// watcher starts `rota serve watch <id>` in a session of its own, which is
// where a watcher runs rather than starting itself again, so the test holds
// the very process. It is killed at cleanup if it is still there.
func watcher(t *testing.T, store string, id int) *watched {
	t.Helper()
	if buildErr != nil {
		t.Skip(buildErr)
	}
	w := &watched{cmd: exec.Command(rotaBin, "serve", "watch", strconv.Itoa(id)), ended: make(chan struct{})}
	w.cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "ROTA_HOME=" + store}, offline()...)
	w.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		w.err = w.cmd.Wait()
		close(w.ended)
	}()
	t.Cleanup(func() {
		select {
		case <-w.ended:
		default:
			_ = w.cmd.Process.Kill()
			<-w.ended
		}
	})
	return w
}

// exited waits for the watcher to end, and returns how.
func (w *watched) exited(t *testing.T, what string) error {
	t.Helper()
	select {
	case <-w.ended:
		return w.err
	case <-time.After(15 * time.Second):
		t.Fatalf("%s never ended", what)
		return nil
	}
}

// running reports whether the watcher has not ended yet.
func (w *watched) running() bool {
	select {
	case <-w.ended:
		return false
	default:
		return true
	}
}

// watchSaid waits until the store's watch.log holds a line containing want,
// and returns the whole log.
func watchSaid(t *testing.T, store, want string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(store, "watch.log"))
		if strings.Contains(string(raw), want) {
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch.log never said %q:\n%s", want, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// One watcher a home: the first takes .rota-watch.lock there and names
// itself in it, a second goes at once and says nothing, and the first goes
// on SIGTERM, saying so before it lets go of the lock.
func TestServeWatch_OneWatcherAHomeUntilASignal(t *testing.T) {
	since(t, "1.3.2")
	store, home := keptClaude(t)
	first := watcher(t, store, 1)
	watchSaid(t, store, "watching "+home)
	if raw, err := os.ReadFile(filepath.Join(home, ".rota-watch.lock")); err != nil || strings.TrimSpace(string(raw)) != strconv.Itoa(first.cmd.Process.Pid) {
		t.Fatalf("the lock names the watcher: %q %v", raw, err)
	}

	if err := watcher(t, store, 1).exited(t, "a second watcher of one home"); err != nil {
		t.Fatalf("a second watcher goes at once, quietly: %v", err)
	}
	if log := watchSaid(t, store, "watching"); strings.Count(log, "watching") != 1 {
		t.Fatalf("and says nothing:\n%s", log)
	}
	if !first.running() {
		t.Fatalf("the first watcher keeps watching: %v", first.err)
	}

	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := first.exited(t, "the watcher after SIGTERM"); err != nil {
		t.Fatalf("it goes cleanly: %v", err)
	}
	watchSaid(t, store, "stopped by a signal")
}

// A watcher with nothing to keep goes at its first look and says why: an
// account whose provider keeps no login in a home, and an account that is
// not there. Started from anywhere but a session of its own, `rota serve
// watch` starts itself again in one and returns at once.
func TestServeWatch_NothingToKeepEndsAtTheFirstLook(t *testing.T) {
	since(t, "1.3.2")
	store := storeOf(t, `{"ordered":true,"accounts":[
 {"id":1,"provider":"grok","email":"g@x","order":1,"token":{"accessToken":"xai-fake"}}]}`)
	if err := watcher(t, store, 1).exited(t, "a watcher of a grok account"); err != nil {
		t.Fatal(err)
	}
	watchSaid(t, store, "no longer keeps its login in its home")

	// At one MiB the log is moved aside to watch.log.1.
	full := strings.Repeat("x", 1<<20)
	if err := os.WriteFile(filepath.Join(store, "watch.log"), []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := watcher(t, store, 1).exited(t, "a watcher of a grok account"); err != nil {
		t.Fatal(err)
	}
	if aside, err := os.ReadFile(filepath.Join(store, "watch.log.1")); err != nil || string(aside) != full {
		t.Fatalf("the full log is moved aside: %d bytes, %v", len(aside), err)
	}
	if log := watchSaid(t, store, "no longer keeps"); strings.Contains(log, "xxx") {
		t.Fatal("and a new one begun")
	}

	start := time.Now()
	out, code := rotaIn(t, store, nil, "serve", "watch", "9")
	if code != 0 || time.Since(start) > 10*time.Second {
		t.Fatalf("returns at once: %d after %v\n%s", code, time.Since(start), out)
	}
	watchSaid(t, store, "account 9 is gone")
}

// An interactive `rota run` on a claude account's own login starts the
// watcher before it hands over; the CLI's exit status is what it was. A
// question starts none.
func TestRotaRun_AnInteractiveRunLeavesAWatcherBehind(t *testing.T) {
	since(t, "1.3.2")
	if runtime.GOOS != "linux" {
		t.Skip("elsewhere a launch reads the home's login from a keychain, which no test may touch")
	}
	now := time.Now()
	store := storeOf(t, fmt.Sprintf(`{"ordered":true,"accounts":[
 {"id":1,"provider":"claude","email":"c@x","order":1,"quotaAt":%d,
  "quota":{"windows":[{"name":"five_hour","percent":10,"primary":true}]},
  "token":{"accessToken":"A","refreshToken":"R","expiresAt":%d}}]}`, now.UnixMilli(), now.Add(time.Hour).UnixMilli()))
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+claudeScript("", 0)), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin + ":/usr/bin:/bin", "ROTA_CLAUDE_HOME=" + t.TempDir()}

	out, code := rotaIn(t, store, env, "run", "1", "hi")
	if code != 0 {
		t.Fatalf("a question: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(store, "watch.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a question starts no watcher: %v", err)
	}

	out, code = rotaIn(t, store, env, "run", "1")
	if code != 0 {
		t.Fatalf("the CLI's own status: %d\n%s", code, out)
	}
	home := filepath.Join(store, "homes", "claude-1")
	t.Cleanup(func() { // a watcher a failed test left behind
		if raw, err := os.ReadFile(filepath.Join(home, ".rota-watch.lock")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	watchSaid(t, store, "watching "+home)
	raw, err := os.ReadFile(filepath.Join(home, ".rota-watch.lock"))
	pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || perr != nil {
		t.Fatalf("the watcher names itself in the home: %q %v", raw, err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("the watcher is running: %v", err)
	}
	watchSaid(t, store, "stopped by a signal")
}

// Started from anywhere but a session of its own, as a rota about to become
// the CLI starts it, the watcher starts itself again in a session of its
// own, at /, with nothing open but /dev/null, and returns at once; a store
// named by a relative ROTA_HOME is found from / as it was from where it was
// started.
func TestServeWatch_StartsItselfAgainInASessionOfItsOwn(t *testing.T) {
	since(t, "1.3.2")
	if buildErr != nil {
		t.Skip(buildErr)
	}
	store, home := keptClaude(t)
	cmd := exec.Command(rotaBin, "serve", "watch", "1")
	cmd.Dir = filepath.Dir(store)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "ROTA_HOME=" + filepath.Base(store)}, offline()...)
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil || time.Since(start) > 10*time.Second {
		t.Fatalf("returns at once: %v after %v\n%s", err, time.Since(start), out)
	}
	// The relative store, made absolute from where it was named, may read as
	// the same directory by another path, /private/var for /var on macOS.
	watchSaid(t, store, filepath.Join("homes", "claude-1"))
	raw, err := os.ReadFile(filepath.Join(home, ".rota-watch.lock"))
	pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || perr != nil || pid == cmd.Process.Pid {
		t.Fatalf("another process keeps the home: %q %v", raw, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("and outlives the one that started it: %v", err)
	}
	if runtime.GOOS == "linux" {
		proc := "/proc/" + strconv.Itoa(pid)
		if cwd, err := os.Readlink(proc + "/cwd"); err != nil || cwd != "/" {
			t.Errorf("it runs at /: %q %v", cwd, err)
		}
		for _, fd := range []string{"0", "1", "2"} {
			if target, err := os.Readlink(proc + "/fd/" + fd); err != nil || target != "/dev/null" {
				t.Errorf("fd %s is /dev/null: %q %v", fd, target, err)
			}
		}
		stat, err := os.ReadFile(proc + "/stat")
		if err != nil {
			t.Fatal(err)
		}
		f := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:])) // state ppid pgrp session
		if len(f) < 4 || f[3] != strconv.Itoa(pid) {
			t.Errorf("it leads a session of its own: %q", stat)
		}
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	watchSaid(t, store, "stopped by a signal")
}

// Only a claude account whose login lives in its home leaves a watcher: an
// interactive run on an account in a directory the person chose, or on
// another provider, starts none.
func TestRotaRun_AnInteractiveRunWithNothingToKeepStartsNoWatcher(t *testing.T) {
	since(t, "1.3.2")
	store := storeOf(t, `{"ordered":true,"accounts":[`+claudeIn(1, t.TempDir(), time.Now().Add(30*24*time.Hour))+`,
 {"id":2,"provider":"grok","email":"g@x","order":2,"token":{"accessToken":"xai-fake"}}]}`)
	bin := t.TempDir()
	for name, body := range map[string]string{"claude": claudeScript("", 0), "grok": grokScript("")} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + bin + ":/usr/bin:/bin", "ROTA_CLAUDE_HOME=" + t.TempDir()}
	for _, id := range []string{"1", "2"} {
		if out, code := rotaIn(t, store, env, "run", id); code != 0 {
			t.Fatalf("rota run %s: %d\n%s", id, code, out)
		}
	}
	time.Sleep(time.Second) // a watcher says something at its first look
	if raw, err := os.ReadFile(filepath.Join(store, "watch.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no watcher: %q %v", raw, err)
	}
}
