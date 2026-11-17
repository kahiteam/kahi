//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/testutil"
)

// Shell completion (FUNC-037, ADR-052, #156), through cobra's hidden
// __complete command.

// completeNames runs kahi __complete args... and returns the offered names
// and the directive line.
func completeNames(t *testing.T, env []string, args ...string) ([]string, string) {
	t.Helper()
	stdout, stderr, code := runKahiCompletion(t, env, args...)
	if code != 0 {
		t.Fatalf("__complete %v: exit %d: %s", args, code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	directive := lines[len(lines)-1]
	var names []string
	for _, l := range lines[:len(lines)-1] {
		name, _, _ := strings.Cut(l, "\t")
		names = append(names, name)
	}
	return names, directive
}

// runKahiCompletion runs kahi with env in a temp dir, bounded by the
// completion budget's 3 s e2e promise.
func runKahiCompletion(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"__complete"}, args...)
	if env == nil {
		return runKahiTimeout(t, 3*time.Second, full...)
	}
	return runKahiEnv(t, t.TempDir(), env, full...)
}

// noSocketEnv points every default at a socket that does not exist.
func noSocketEnv(t *testing.T) []string {
	t.Helper()
	return append(searchEnv(), "KAHI_SOCKET="+filepath.Join(t.TempDir(), "none.sock"))
}

func wantNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("names %q, want %q", got, want)
	}
}

const completionPrograms = "[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n\n" +
	"[programs.worker]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\nnumprocs = 2\nprocess_name = \"worker_%(process_num)d\"\n\n"

// Steps 1, 2 and 11: the scripts parse; subcommands and flags complete.
func TestCompletion_ScriptsAndStatic(t *testing.T) {
	t.Parallel()
	for _, sh := range []string{"bash", "zsh"} {
		path, err := exec.LookPath(sh)
		if err != nil {
			t.Logf("%s not installed: syntax check skipped", sh)
			continue
		}
		script, _, code := runKahi(t, t.TempDir(), "completion", sh)
		if code != 0 {
			t.Fatalf("completion %s: exit %d", sh, code)
		}
		cmd := exec.Command(path, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v: %s", sh, err, out)
		}
	}
	names, directive := completeNames(t, noSocketEnv(t), "")
	for _, want := range []string{"completion", "ctl", "daemon", "hash-password", "help", "init", "migrate", "version"} {
		if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+want+" ") {
			t.Errorf("subcommands %q lack %s", names, want)
		}
	}
	if directive != ":4" {
		t.Errorf("directive %q", directive)
	}
	names, _ = completeNames(t, noSocketEnv(t), "ctl", "start", "--")
	if !strings.Contains(strings.Join(names, " "), "--help") {
		t.Errorf("flags %q lack --help", names)
	}
}

// Steps 3, 4, 5 and 10: names from the daemon on the socket typed with -s.
func TestCompletion_ProcessNames(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, completionPrograms)
	waitForState(t, client, "worker_1", "RUNNING", 5*time.Second)
	names, directive := completeNames(t, nil, "ctl", "-s", socket, "start", "")
	wantNames(t, names, "web", "worker_0", "worker_1", "web:*", "worker:*", "all")
	if directive != ":4" {
		t.Errorf("directive %q, want :4 (NoFileComp)", directive)
	}
	for _, sub := range []string{"attach", "tail", "pid", "send"} {
		names, _ := completeNames(t, nil, "ctl", "-s", socket, sub, "")
		wantNames(t, names, "web", "worker_0", "worker_1")
	}
	names, _ = completeNames(t, nil, "ctl", "-s", socket, "signal", "")
	wantNames(t, names, "SIGTERM", "SIGHUP", "SIGINT", "SIGQUIT", "SIGKILL", "SIGUSR1", "SIGUSR2", "SIGSTOP", "SIGCONT")
	names, _ = completeNames(t, nil, "ctl", "-s", socket, "signal", "SIGTERM", "")
	wantNames(t, names, "web", "worker_0", "worker_1", "web:*", "worker:*", "all")

	// Without -s the completion does not find this daemon.
	names, _ = completeNames(t, noSocketEnv(t), "ctl", "start", "")
	wantNames(t, names)
}

// Steps 6 and 7: groups for add, remove, update and reread.
func TestCompletion_Groups(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, completionPrograms+
		"[programs.a]\ncommand = \"/bin/sleep 300\"\nautostart = false\n\n[programs.x]\ncommand = \"/bin/sleep 300\"\nautostart = false\n\n[groups.g]\nprograms = [\"a\"]\n")
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	for _, g := range []string{"g", "x"} {
		if _, stderr, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "remove", g); code != 0 {
			t.Fatalf("remove %s: %s", g, stderr)
		}
	}
	names, _ := completeNames(t, nil, "ctl", "-s", socket, "add", "")
	wantNames(t, names, "g", "x")
	names, _ = completeNames(t, nil, "ctl", "-s", socket, "remove", "")
	wantNames(t, names, "web", "worker")

	rewriteConfig(t, socket, completionPrograms+"[programs.new]\ncommand = \"/bin/sleep 300\"\nautostart = false\n")
	for _, sub := range []string{"update", "reread"} {
		names, _ := completeNames(t, nil, "ctl", "-s", socket, sub, "")
		wantNames(t, names, "new", "web", "worker")
	}
}

// Steps 8 and 9: no daemon, or one that never answers: no names, quickly.
func TestCompletion_NoOrSlowDaemon(t *testing.T) {
	t.Parallel()
	names, _ := completeNames(t, noSocketEnv(t), "ctl", "start", "")
	wantNames(t, names)

	sock := filepath.Join(realDir(t), "stall.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered
		}
	}()
	start := time.Now()
	names, _ = completeNames(t, nil, "ctl", "-s", sock, "start", "")
	wantNames(t, names)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("completion took %v against a stalled daemon", d)
	}
}

// Step 12: -c and the TCP flags select the daemon.
func TestCompletion_ConfigAndTCPFlags(t *testing.T) {
	t.Parallel()
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	client, socket, _ := startDaemonOut(t, fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\nusername = \"admin\"\npassword = %q\n\n", addr, hashPassword(t, "pw"))+completionPrograms)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	cfg := filepath.Join(t.TempDir(), "ctl.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("[server.unix]\nfile = %q\n", socket)), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(searchEnv(), "KAHI_SOCKET=")
	names, _ := completeNames(t, env, "ctl", "-c", cfg, "pid", "")
	wantNames(t, names, "web", "worker_0", "worker_1")
	names, _ = completeNames(t, nil, "ctl", "--addr", addr, "-u", "admin", "-p", "pw", "pid", "")
	wantNames(t, names, "web", "worker_0", "worker_1")
	names, _ = completeNames(t, nil, "ctl", "--addr", addr, "-u", "admin", "-p", "wrong", "pid", "")
	wantNames(t, names)
}
