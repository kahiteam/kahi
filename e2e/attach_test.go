//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/testutil"
)

// kahi ctl attach (FUNC-034, ADR-044, #157), through the binary with input
// from a pipe (the non-terminal path).

type syncWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// attachProc is a running kahi ctl attach.
type attachProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *syncWriter
	stderr *syncWriter
	done   chan error
}

func startAttach(t *testing.T, socket, name string) *attachProc {
	t.Helper()
	cmd := exec.Command(kahiBinary, "ctl", "-s", socket, "attach", name)
	a := &attachProc{cmd: cmd, stdout: &syncWriter{}, stderr: &syncWriter{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = a.stdout, a.stderr
	var err error
	if a.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { a.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = a.stdin.Close()
		_ = cmd.Process.Kill()
		<-a.done
	})
	return a
}

// wait returns the exit code within limit.
func (a *attachProc) wait(t *testing.T, limit time.Duration) int {
	t.Helper()
	select {
	case err := <-a.done:
		a.done <- err
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	case <-time.After(limit):
		t.Fatalf("attach did not end within %v (stdout %q, stderr %q)", limit, a.stdout.String(), a.stderr.String())
	}
	return -1
}

func waitText(t *testing.T, w *syncWriter, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(w.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q in %q", want, w.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitAttached waits until the daemon holds the session (ctl send is
// refused with "attached").
func waitAttached(t *testing.T, socket, name string) {
	t.Helper()
	// The probe writes zero bytes through the API: kahi ctl send adds a
	// newline, which reaches the process when the probe wins the race with
	// the session and satisfies a script's read before the test's input.
	client := unixHTTPClient(socket)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var code int
		var body string
		resp, err := client.Post("http://unix/api/v1/processes/"+name+"/stdin", "application/json", strings.NewReader(`{"data":""}`))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			code, body = resp.StatusCode, string(b)
			if code == http.StatusConflict && strings.Contains(body, "process "+name+": attached") {
				return
			}
		} else {
			body = err.Error()
		}
		if time.Now().After(deadline) {
			t.Fatalf("session not held: %d %q", code, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const attachCat = "[programs.cat]\ncommand = \"/bin/cat\"\nstartsecs = 0\nstopwaitsecs = 2\n\n"

// Steps 1, 12 and 9: input forwarded as it arrives (0x1d too, no banner),
// exit after EOF, the process runs on, the hold is released.
func TestAttach_PipeSession(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, attachCat)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)
	before, _ := getProcessInfo(client, "cat")

	cmd := exec.Command(kahiBinary, "ctl", "-s", socket, "attach", "cat")
	cmd.Stdin = strings.NewReader("hi\na\x1db\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("attach: %v (%q)", err, errb.String())
	}
	if time.Since(start) > 6*time.Second {
		t.Errorf("attach took %v after EOF", time.Since(start))
	}
	if !strings.Contains(out.String(), "hi\n") || !strings.Contains(out.String(), "a\x1db\n") {
		t.Errorf("stdout %q", out.String())
	}
	if strings.Contains(errb.String(), "attached to") || strings.Contains(errb.String(), "detached") {
		t.Errorf("banner without a terminal: %q", errb.String())
	}
	if now, _ := getProcessInfo(client, "cat"); now.State != "RUNNING" || now.PID != before.PID {
		t.Errorf("cat after attach: %+v", now)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, stderr, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "send", "cat", "hello")
		if code == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("send after attach: %d %q", code, stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitTail(t, client, "cat", "hello")
}

// Steps 2, 3 and 6: one session at a time; send is refused during it; a
// killed client releases the hold.
func TestAttach_OneSessionAndRelease(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, attachCat)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)
	a := startAttach(t, socket, "cat")
	waitAttached(t, socket, "cat")

	_, stderr, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "attach", "cat")
	if code != 1 || !strings.Contains(stderr, "process cat: already attached") {
		t.Errorf("second attach: %d %q", code, stderr)
	}
	_, stderr, code = runKahi(t, t.TempDir(), "ctl", "-s", socket, "send", "cat", "x")
	if code != 1 || !strings.Contains(stderr, "process cat: attached") {
		t.Errorf("send during attach: %d %q", code, stderr)
	}

	_ = a.cmd.Process.Signal(syscall.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for {
		cmd := exec.Command(kahiBinary, "ctl", "-s", socket, "attach", "cat")
		cmd.Stdin = strings.NewReader("x\n")
		out, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(out), "x") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("attach after the client was killed: %v %q", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Step 4 and 14: not running, unknown, a listener process.
func TestAttach_Refusals(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, "[programs.p]\ncommand = \"/bin/cat\"\nautostart = false\n\n[eventlisteners.l]\ncommand = \"/bin/cat\"\nevents = [\"PROCESS_STATE_EXITED\"]\nstartsecs = 0\nstopwaitsecs = 2\n")
	waitForState(t, client, "l", "RUNNING", 5*time.Second)
	for name, want := range map[string]string{
		"p":      "process not running: p",
		"nosuch": "no such process: nosuch",
		"l":      "process l: stdin carries the event listener protocol",
	} {
		_, stderr, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "attach", name)
		if code != 1 || !strings.Contains(stderr, want) {
			t.Errorf("%s: %d %q", name, code, stderr)
		}
	}
}

// Steps 5, 7 and 8: stdout and stderr kept apart (merged with
// redirect_stderr); the process exiting ends the session.
func TestAttach_StreamsAndExit(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	both := writeScript(t, dir, "both.sh", "read l\necho OUT\necho ERR >&2\nexec sleep 30\n")
	quit := writeScript(t, dir, "quit.sh", "read l\nexit 3\n")
	client, socket, _ := startDaemonOut(t, fmt.Sprintf(
		"[programs.both]\ncommand = %q\nstartsecs = 0\nstopwaitsecs = 2\n\n[programs.merged]\ncommand = %q\nstartsecs = 0\nstopwaitsecs = 2\nredirect_stderr = true\n\n[programs.quit]\ncommand = %q\nstartsecs = 0\nautorestart = false\n",
		both, both, quit))
	for _, n := range []string{"both", "merged", "quit"} {
		waitForState(t, client, n, "RUNNING", 5*time.Second)
	}

	a := startAttach(t, socket, "both")
	waitAttached(t, socket, "both")
	_, _ = io.WriteString(a.stdin, "go\n")
	waitText(t, a.stdout, "OUT")
	waitText(t, a.stderr, "ERR")
	if strings.Contains(a.stdout.String(), "ERR") {
		t.Errorf("stderr on stdout: %q", a.stdout.String())
	}

	m := startAttach(t, socket, "merged")
	waitAttached(t, socket, "merged")
	_, _ = io.WriteString(m.stdin, "go\n")
	waitText(t, m.stdout, "OUT")
	waitText(t, m.stdout, "ERR")

	q := startAttach(t, socket, "quit")
	waitAttached(t, socket, "quit")
	_, _ = io.WriteString(q.stdin, "bye\n")
	if code := q.wait(t, 5*time.Second); code != 0 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(q.stderr.String(), "process quit exited") {
		t.Errorf("stderr %q", q.stderr.String())
	}
}

// Step 10: the daemon goes away during a session.
func TestAttach_DaemonLost(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	socket := dir + "/kahi.sock"
	cfgPath := dir + "/kahi.toml"
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n%s", socket, attachCat)), 0o644); err != nil {
		t.Fatal(err)
	}
	client, daemon, _ := rawDaemon(t, dir, socket, "", "-c", cfgPath)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)
	info, _ := getProcessInfo(client, "cat")
	a := startAttach(t, socket, "cat")
	waitAttached(t, socket, "cat")
	_ = daemon.Process.Signal(syscall.SIGKILL)
	// The orphaned cat would trip the suite's leak check.
	t.Cleanup(func() { _ = syscall.Kill(info.PID, syscall.SIGKILL) })
	if code := a.wait(t, 5*time.Second); code != 1 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(a.stderr.String(), "attach to cat lost:") {
		t.Errorf("stderr %q", a.stderr.String())
	}
}

// Step 11: on a terminal the banner is printed (keystrokes: unit tests).
func TestAttach_TerminalBanner(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	short := writeScript(t, dir, "short.sh", "sleep 2\n")
	client, socket, _ := startDaemonOut(t, fmt.Sprintf("[programs.short]\ncommand = %q\nstartsecs = 0\nautorestart = false\n", short))
	waitForState(t, client, "short", "RUNNING", 5*time.Second)
	out := runKahiTTY(t, "ctl", "-s", socket, "attach", "short")
	if !strings.Contains(out, "attached to short (Ctrl+] to detach)") || !strings.Contains(out, "process short exited") {
		t.Errorf("terminal output %q", out)
	}
}

// Step 13: the endpoint: 101 over the socket, 401 over TCP without
// credentials, 400 without the Upgrade header.
func TestAttach_Endpoint(t *testing.T) {
	t.Parallel()
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	client, socket, _ := startDaemonOut(t, fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\nusername = \"admin\"\npassword = %q\n\n", addr, hashPassword(t, "pw"))+attachCat)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)
	raw := func(network, address, headers string) string {
		conn, err := net.DialTimeout(network, address, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprintf(conn, "POST /api/v1/processes/cat/attach HTTP/1.1\r\nHost: kahi\r\nContent-Length: 0\r\n%s\r\n", headers)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Sprintf("%d %s", resp.StatusCode, b)
	}
	up := "Connection: Upgrade\r\nUpgrade: kahi-attach\r\n"
	if got := raw("unix", socket, up); !strings.HasPrefix(got, "101") {
		t.Errorf("unix: %s", got)
	}
	if got := raw("tcp", addr, up); !strings.HasPrefix(got, "401") {
		t.Errorf("tcp without credentials: %s", got)
	}
	if got := raw("unix", socket, ""); !strings.HasPrefix(got, "400") || !strings.Contains(got, "attach requires Upgrade: kahi-attach") {
		t.Errorf("no Upgrade: %s", got)
	}
}
