package serve_test

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A terminal somebody is sitting at, offered to the server running beside
// them. `rota run <id> --share` keeps rota above the CLI instead of being
// replaced by it and hands the same bytes to a rota serving this machine,
// over a socket in the store.
//
// These are about that from the outside: the socket the published binary
// actually opens, and a terminal shared into the published listing.

// shareSock is where a server listens for a local terminal being offered.
func shareSock(home string) string {
	return filepath.Join(home, "terminals", "share.sock")
}

// shortHome is a store somewhere a unix socket address fits.
//
// A socket address is about a hundred bytes on both kernels, and a test's own
// temporary directory is named after the test inside a path macOS already
// makes long — so the socket in it would not fit, and these cases would skip
// on the machine most of them are written on. The directory is this test's
// all the same, and goes with it.
func shortHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if len(shareSock(home)) > 100 {
		t.Skipf("%s is too long for a unix socket address", shareSock(home))
	}
	probe := filepath.Join(home, "probe.sock")
	ln, err := net.Listen("unix", probe)
	if err != nil {
		t.Skipf("no unix socket here: %v", err)
	}
	ln.Close()
	os.Remove(probe)
	return home
}

// waitForFile waits for something to appear, because a server answers GET /
// a moment before every last thing it opens is open.
func waitForFile(t *testing.T, path string) os.FileInfo {
	t.Helper()
	for range 500 {
		if fi, err := os.Lstat(path); err == nil {
			return fi
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return nil
}

// The door a local terminal is offered through is the owner's alone, and it
// is there exactly when the file said it should be.
func TestTheShareSocketIsTheOwnersAlone(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig(), home: shortHome(t)})

	fi := waitForFile(t, shareSock(s.home))
	if fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%s is not a socket: %v", shareSock(s.home), fi.Mode())
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("the socket is %o, want 600: who may share a terminal is decided by the filesystem", perm)
	}
	dir, err := os.Stat(filepath.Dir(shareSock(s.home)))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Fatalf("the directory it sits in is %o, want 700", perm)
	}
}

// And it is not there when the file turned it off, while the terminals the
// server starts itself go on working.
func TestNoShareSocketWhenTheFileSaysNo(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig() + "\n[terminal]\nshare = false\n", home: shortHome(t)})

	// The server is up — this answered — so anything it was going to open
	// is open.
	if resp, raw := s.do("GET", "/v1/terminals", ""); resp.StatusCode != 200 {
		t.Fatalf("the terminal group is still on: %d %s", resp.StatusCode, raw)
	}
	if _, err := os.Lstat(shareSock(s.home)); err == nil {
		t.Fatalf("there is a socket at %s and the file said share = false", shareSock(s.home))
	}
}

// A terminal offered from this machine is listed like any other, as a shared
// one: the same id, the same description, the same page.
func TestATerminalSharedFromThisMachineIsListed(t *testing.T) {
	since(t, "1.2.0")
	onlyUnix(t)
	s := start(t, opts{config: terminalConfig(), claude: ttyClaude(), home: shortHome(t)})
	waitForFile(t, shareSock(s.home))

	// --share needs a terminal to share, and `go test` has none. script is
	// the portable way to give a command one; where there is no script,
	// there is nothing here to run.
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("no script(1) to put a command at a terminal with")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+ttyClaude()), 0o700); err != nil {
		t.Fatal(err)
	}

	// The two script(1)s take their arguments differently: util-linux wants
	// the command as one -c string and the file last, the BSDs want the file
	// first and the command after it.
	args := []string{"-q", "/dev/null", rotaBin, "run", "2", "--share", "--label", "shared-here"}
	if runtime.GOOS == "linux" {
		args = []string{"-q", "-c", rotaBin + " run 2 --share --label shared-here", "/dev/null"}
	}
	cmd := exec.Command("script", args...)
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		"ROTA_HOME=" + s.home,
		"TERM=xterm",
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
	}
	// A process group of its own, so the whole of it can be ended: script
	// holds the terminal and rota holds the CLI under it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Left open on purpose. script copies its own standard input into the
	// terminal it made, and gives up when that input ends.
	typing, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("script(1) would not run here: %v", err)
	}
	t.Cleanup(func() {
		typing.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	shared := waitForShared(t, s)
	if shared["label"] != "shared-here" {
		t.Fatalf("a shared terminal is called what --label called it: %v", shared)
	}
	if shared["mode"] != "control" {
		t.Fatalf("and says whether the page may type into it: %v", shared)
	}
	if pid, _ := shared["pid"].(float64); pid == 0 {
		t.Fatalf("and which process is answering for it: %v", shared)
	}
	if acct, _ := shared["account"].(map[string]any); acct == nil || acct["id"] != float64(2) {
		t.Fatalf("and whose account is paying for it: %v", shared)
	}
}

// waitForShared waits for a terminal of kind "shared" in the published
// listing and hands it back.
func waitForShared(t *testing.T, s *server) map[string]any {
	t.Helper()
	var last string
	for range 500 {
		resp, raw := s.do("GET", "/v1/terminals", "")
		if resp.StatusCode == 200 {
			last = string(raw)
			var doc struct {
				Terminals []map[string]any `json:"terminals"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				for _, one := range doc.Terminals {
					if one["kind"] == "shared" {
						return one
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no terminal was ever shared to this server; it holds %s\nrota said:\n%s",
		last, strings.TrimSpace(s.stderr.String()))
	return nil
}
