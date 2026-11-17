//go:build e2e

package e2e

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Syslog forwarding of process output (FUNC-020, ADR-050, #152).

// syslogDaemon starts a daemon whose [supervisor] table carries extra (for
// syslog_address) and returns its client, socket, output, directory and
// command.
func syslogDaemon(t *testing.T, extra, body string) (*ctl.Client, string, *os.File, string, *exec.Cmd) {
	t.Helper()
	dir := realDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	cfg := fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\n%s\n\n[server.unix]\nfile = %q\n\n%s", extra, socket, body)
	if err := os.WriteFile(filepath.Join(dir, "kahi.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	client, cmd, out := rawDaemon(t, dir, socket, "", "-c", filepath.Join(dir, "kahi.toml"))
	return client, socket, out, dir, cmd
}

// gramListener receives datagrams at path, with log/syslog's one trailing
// newline removed.
func gramListener(t *testing.T, path string) <-chan string {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch := make(chan string, 1000)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			ch <- strings.TrimSuffix(string(buf[:n]), "\n")
		}
	}()
	return ch
}

// streamListener accepts connections on ln and sends each line, raw (with
// its newline). stop closes ln and every accepted connection.
func streamListener(t *testing.T, ln net.Listener) (ch <-chan string, stop func()) {
	t.Helper()
	out := make(chan string, 1000)
	var mu sync.Mutex
	var conns []net.Conn
	stop = func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	}
	t.Cleanup(stop)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func() {
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					out <- line
				}
			}()
		}
	}()
	return out, stop
}

// body is a message's text after "tag[pid]: ".
func body(m string) string {
	if i := strings.Index(m, "]: "); i >= 0 {
		return m[i+3:]
	}
	return m
}

// await returns the first message whose body satisfies ok, within limit.
func await(t *testing.T, ch <-chan string, limit time.Duration, ok func(string) bool) string {
	t.Helper()
	deadline := time.After(limit)
	for {
		select {
		case m := <-ch:
			if ok(body(m)) {
				return m
			}
		case <-deadline:
			t.Fatalf("no matching syslog message within %v", limit)
		}
	}
}

func suffix(s string) func(string) bool {
	return func(b string) bool { return strings.HasSuffix(strings.TrimSuffix(b, "\n"), s) }
}

func syslogProg(script, extra string) string {
	return fmt.Sprintf("[programs.p]\ncommand = %q\nstartsecs = 0\nstopwaitsecs = 2\nstopasgroup = true\nkillasgroup = true\n%s\n", script, extra)
}

// Steps 1, 2 and 4: one message per line, tagged with the process name,
// LOCAL0.INFO by default, no newline in a body.
func TestSyslog_LinesTagAndDefaultPriority(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	ch := gramListener(t, sock)
	script := writeScript(t, dir, "p.sh", "printf 'hello\\na\\nb\\nc\\n'\nexec sleep 300\n")
	syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), syslogProg(script, "stdout_syslog = true"))
	m := await(t, ch, 5*time.Second, suffix("hello"))
	if !strings.HasPrefix(m, "<134>") || !strings.Contains(m, " p[") {
		t.Errorf("message %q: want <134> and tag p", m)
	}
	for _, want := range []string{"a", "b", "c"} {
		got := body(await(t, ch, 5*time.Second, func(string) bool { return true }))
		if got != want {
			t.Errorf("body %q, want %q", got, want)
		}
	}
}

// Steps 3 and 5: facility and priority; numprocs tags.
func TestSyslog_FacilityAndNumprocsTags(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	ch := gramListener(t, sock)
	script := writeScript(t, dir, "p.sh", "echo hi\nexec sleep 300\n")
	body := fmt.Sprintf("[programs.web]\ncommand = %q\nnumprocs = 2\nprocess_name = \"web_%%(process_num)d\"\nstartsecs = 0\nstopwaitsecs = 2\nstdout_syslog = true\nsyslog_facility = \"LOCAL3\"\nsyslog_priority = \"notice\"\n", script)
	syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), body)
	tags := map[string]bool{}
	for len(tags) < 2 {
		m := await(t, ch, 5*time.Second, suffix("hi"))
		if !strings.HasPrefix(m, "<157>") {
			t.Fatalf("message %q: want <157>", m)
		}
		for _, tag := range []string{"web_0", "web_1"} {
			if strings.Contains(m, " "+tag+"[") {
				tags[tag] = true
			}
		}
	}
}

// Steps 6, 9, 10 and 11: stderr alone, splitting, ANSI stripping, a partial
// last line.
func TestSyslog_StreamsSplitStripTail(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	ch := gramListener(t, sock)
	script := writeScript(t, dir, "p.sh", "echo out\nsleep 0.2\necho err >&2\nprintf '%5000s\\n' '' | tr ' ' x >&2\nprintf '\\033[31mred\\033[0m\\n' >&2\nsleep 0.2\necho end >&2\nprintf 'tail' >&2\n")
	syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), syslogProg(script, "stderr_syslog = true\nstrip_ansi = true\nautorestart = false"))
	var bodies []string
	for {
		b := body(await(t, ch, 10*time.Second, func(string) bool { return true }))
		bodies = append(bodies, b)
		if b == "tail" {
			break
		}
	}
	joined := strings.Join(bodies, "\n")
	if strings.Contains(joined, "out") {
		t.Errorf("stdout forwarded with stderr_syslog alone: %q", bodies)
	}
	var sizes []int
	x := ""
	for _, b := range bodies {
		if strings.Trim(b, "x") == "" && b != "" {
			sizes = append(sizes, len(b))
			x += b
		}
	}
	if len(sizes) != 3 || sizes[0] != 2048 || sizes[1] != 2048 || sizes[2] != 904 || x != strings.Repeat("x", 5000) {
		t.Errorf("split sizes %v", sizes)
	}
	if !strings.Contains(joined, "\nred\n") || strings.Contains(joined, "\x1b") {
		t.Errorf("ANSI not stripped: %q", bodies)
	}
}

// Step 12: redirect_stderr routes stderr through stdout_syslog and warns
// about stderr_syslog.
func TestSyslog_RedirectStderr(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	ch := gramListener(t, sock)
	script := writeScript(t, dir, "p.sh", "echo viaerr >&2\nexec sleep 300\n")
	_, _, out, _, _ := syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), syslogProg(script, "redirect_stderr = true\nstdout_syslog = true\nstderr_syslog = true"))
	await(t, ch, 5*time.Second, suffix("viaerr"))
	waitOutputFile(t, out, "programs.p.stderr_syslog has no effect with redirect_stderr", 5*time.Second)
}

// Step 13: config errors.
func TestSyslog_ConfigErrors(t *testing.T) {
	t.Parallel()
	expectRefused(t, "[programs.p]\ncommand = \"/bin/sleep 1\"\nsyslog_facility = \"BOGUS\"\n", "programs.p.syslog_facility: unknown facility BOGUS")
	expectRefused(t, "[programs.p]\ncommand = \"/bin/sleep 1\"\nsyslog_priority = \"BOGUS\"\n", "programs.p.syslog_priority: unknown priority BOGUS")
	dir := realDir(t)
	cfg := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(cfg, []byte("[supervisor]\nsyslog_address = \"ftp://x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
	if code != 1 || !strings.Contains(stderr, "supervisor.syslog_address: invalid address ftp://x") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// Step 14: udp, tcp (newline-framed) and a unix stream socket (fallback).
func TestSyslog_NetworkTransports(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	script := writeScript(t, dir, "p.sh", "echo hello\nexec sleep 300\n")

	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	uch := make(chan string, 10)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, _, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			uch <- string(buf[:n])
		}
	}()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tcp.Close() })
	tch, _ := streamListener(t, tcp)
	us, err := net.Listen("unix", filepath.Join(dir, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = us.Close() })
	sch, _ := streamListener(t, us)

	for _, c := range []struct {
		addr string
		ch   <-chan string
	}{
		{"udp://" + udp.LocalAddr().String(), uch},
		{"tcp://" + tcp.Addr().String(), tch},
		{"unix://" + filepath.Join(dir, "s.sock"), sch},
	} {
		syslogDaemon(t, fmt.Sprintf("syslog_address = %q", c.addr), syslogProg(script, "stdout_syslog = true"))
		m := await(t, c.ch, 5*time.Second, suffix("hello"))
		if !strings.HasPrefix(m, "<134>") || !strings.HasSuffix(m, "hello\n") {
			t.Errorf("%s: message %q", c.addr, m)
		}
	}
}

// Steps 7 and 8: no listener: one WARN per outage, the process runs on;
// the listener appears: recovery with one INFO.
func TestSyslog_OutageAndRecovery(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	logf := filepath.Join(dir, "p.log")
	script := writeScript(t, dir, "p.sh", "i=0\nwhile :; do i=$((i+1)); echo n=$i; sleep 0.1; done\n")
	_, _, out, _, _ := syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), syslogProg(script, fmt.Sprintf("stdout_syslog = true\nstdout_logfile = %q", logf)))
	waitOutputFile(t, out, "syslog connection failed for p stdout", 5*time.Second)
	b, _ := os.ReadFile(logf)
	start := strings.Count(string(b), "\n")
	for {
		b, _ = os.ReadFile(logf)
		if strings.Count(string(b), "\n") >= start+10 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := strings.Count(readOut(out), "syslog connection failed for p stdout"); n != 1 {
		t.Errorf("%d failure warnings, want 1 per outage", n)
	}
	ch := gramListener(t, sock)
	await(t, ch, 65*time.Second, func(b string) bool { return strings.HasPrefix(b, "n=") })
	waitOutputFile(t, out, "syslog connection restored for p stdout", 5*time.Second)
}

// Step 18: a tcp listener that goes away and comes back.
func TestSyslog_TCPReconnect(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	logf := filepath.Join(dir, "p.log")
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := tcp.Addr().String()
	tch, stopTCP := streamListener(t, tcp)
	script := writeScript(t, dir, "p.sh", "i=0\nwhile :; do i=$((i+1)); echo n=$i; sleep 0.1; done\n")
	_, _, out, _, _ := syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "tcp://"+addr), syslogProg(script, fmt.Sprintf("stdout_syslog = true\nstdout_logfile = %q", logf)))
	await(t, tch, 5*time.Second, func(b string) bool { return strings.HasPrefix(b, "n=") })
	stopTCP()
	waitOutputFile(t, out, "syslog connection failed for p stdout", 10*time.Second)
	b1, _ := os.ReadFile(logf)
	time.Sleep(time.Second)
	b2, _ := os.ReadFile(logf)
	if len(b2) <= len(b1) {
		t.Error("the stdout log stopped growing during the outage")
	}
	if n := strings.Count(readOut(out), "syslog connection failed for p stdout"); n != 1 {
		t.Errorf("%d failure warnings, want 1", n)
	}
	tcp2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot rebind %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = tcp2.Close() })
	t2, _ := streamListener(t, tcp2)
	await(t, t2, 65*time.Second, func(b string) bool { return strings.HasPrefix(b, "n=") })
	waitOutputFile(t, out, "syslog connection restored for p stdout", 5*time.Second)
}

// Step 15: SIGUSR2 reopens log files but leaves syslog connections alone.
func TestSyslog_SIGUSR2KeepsConnection(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	ch := gramListener(t, sock)
	script := writeScript(t, dir, "p.sh", "i=0\nwhile :; do i=$((i+1)); echo n=$i; sleep 0.1; done\n")
	_, _, out, _, cmd := syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), syslogProg(script, "stdout_syslog = true"))
	last := 0
	for last < 5 {
		fmt.Sscanf(body(await(t, ch, 5*time.Second, func(b string) bool { return strings.HasPrefix(b, "n=") })), "n=%d", &last)
	}
	if err := cmd.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	before := last
	await(t, ch, 5*time.Second, func(b string) bool {
		var n int
		fmt.Sscanf(b, "n=%d", &n)
		return n > before+5
	})
	if strings.Contains(readOut(out), "syslog connection failed") {
		t.Error("SIGUSR2 broke the syslog connection")
	}
}

// Steps 16, 17 and 19: syslog_address is restart-only; no ignored warning;
// a facility change restarts the program.
func TestSyslog_ReloadRules(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	sock := filepath.Join(dir, "log.sock")
	gramListener(t, sock)
	script := writeScript(t, dir, "p.sh", "exec sleep 300\n")
	client, socket, out, cdir, _ := syslogDaemon(t, fmt.Sprintf("syslog_address = %q", "unix://"+sock), syslogProg(script, "stdout_syslog = true\nsyslog_facility = \"LOCAL1\""))
	waitForState(t, client, "p", "RUNNING", 5*time.Second)
	if strings.Contains(readOut(out), "stdout_syslog is not implemented yet") {
		t.Error("stdout_syslog still flagged as not implemented")
	}
	info, _ := getProcessInfo(client, "p")
	write := func(addr, facility string) {
		cfg := fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\nsyslog_address = %q\n\n[server.unix]\nfile = %q\n\n%s", addr, socket, syslogProg(script, "stdout_syslog = true\nsyslog_facility = \""+facility+"\""))
		if err := os.WriteFile(filepath.Join(cdir, "kahi.toml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("unix://"+sock, "LOCAL2")
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "p", "RUNNING", 10*time.Second)
	if now, _ := getProcessInfo(client, "p"); now.PID == info.PID {
		t.Error("a syslog_facility change did not restart the program")
	}
	write("udp://127.0.0.1:9", "LOCAL2")
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	waitOutputFile(t, out, "supervisor.syslog_address change takes effect after restart", 5*time.Second)
}
