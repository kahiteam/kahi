//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Event listener pools run by the daemon (FUNC-055, SEC-020, ADR-042, step
// 7b): a member speaks the supervisord READY/RESULT protocol on its stdin
// and stdout; its stderr is captured like a program's.

// listenerScript is a supervisord-style listener: it copies each header line
// to stderr and answers by mode. Marker modes act on the first event only,
// recorded in the file named by $2, so the restarted member behaves.
const listenerScript = `mode=$1
n=0
while :; do
  printf 'READY\n'
  read -r hdr || exit 0
  echo "$hdr" >&2
  len=${hdr##*len:}
  n=$((n+1))
  case $mode in
    hex)
      printf 'body=%s\n' "$(head -c "$len" | od -An -tx1 | tr -d ' \n')" >&2
      printf 'RESULT 2\nOK' ;;
    *)
      head -c "$len" >/dev/null ;;
  esac
  case $mode in
    ok|hex) printf 'RESULT 2\nOK' ;;
    fail) printf 'RESULT 4\nFAIL' ;;
    garbage)
      if [ ! -f "$2" ]; then : > "$2"; printf 'garbage\n'; else printf 'RESULT 2\nOK'; fi ;;
    exit)
      if [ ! -f "$2" ]; then : > "$2"; exit 3; fi
      printf 'RESULT 2\nOK' ;;
    hang)
      if [ ! -f "$2" ]; then : > "$2"; exec sleep 300; fi
      printf 'RESULT 2\nOK' ;;
  esac
done
`

// poolEnv is a test directory with the listener script and an exiting
// program "p" whose starts produce PROCESS_STATE_EXITED events.
type poolEnv struct {
	dir      string
	listener string // path of the listener script
	prog     string // program section for p
}

func newPoolEnv(t *testing.T) poolEnv {
	t.Helper()
	dir := realDir(t)
	l := writeScript(t, dir, "listener.sh", listenerScript)
	exit0 := writeScript(t, dir, "exit0.sh", "exit 0\n")
	prog := fmt.Sprintf("[programs.p]\ncommand = %q\nautostart = false\nautorestart = false\nstartsecs = 0\n", exit0)
	return poolEnv{dir: dir, listener: l, prog: prog}
}

// pool returns an [eventlisteners.name] section running the script in mode.
func (e poolEnv) pool(name, mode, extra string) string {
	return fmt.Sprintf("[eventlisteners.%s]\ncommand = \"%s %s %s\"\nevents = [\"PROCESS_STATE_EXITED\"]\nstartsecs = 0\nstopwaitsecs = 2\nstopasgroup = true\nkillasgroup = true\nstderr_logfile = %q\n%s\n",
		name, e.listener, mode, filepath.Join(e.dir, name+".marker"), e.stderr(name), extra)
}

func (e poolEnv) stderr(name string) string { return filepath.Join(e.dir, name+".err") }

// emit makes p exit once, publishing PROCESS_STATE_EXITED.
func emit(t *testing.T, client *ctl.Client) {
	t.Helper()
	if err := client.Start("p"); err != nil {
		t.Fatalf("start p: %v", err)
	}
	// Start returns once p is spawned; wait for its exit, so the next
	// start is a new run.
	waitForState(t, client, "p", "EXITED", 5*time.Second)
}

func waitFile(t *testing.T, path, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), want) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never contained %q:\n%s", path, want, b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

var serialRe = regexp.MustCompile(`serial:(\d+) pool:`)

// serials returns the serials in a listener's stderr, in order.
func serials(t *testing.T, path string) []string {
	t.Helper()
	b, _ := os.ReadFile(path)
	var out []string
	for _, m := range serialRe.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	return out
}

func waitSerials(t *testing.T, path string, n int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := serials(t, path)
		if len(s) >= n {
			return s
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(path)
			t.Fatalf("%s: %d serials, want %d:\n%s", path, len(s), n, b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitReady(t *testing.T, client *ctl.Client, name string) int {
	t.Helper()
	waitForState(t, client, name, "RUNNING", 5*time.Second)
	info, err := getProcessInfo(client, name)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the member writes READY
	return info.PID
}

// Steps 1-3: delivery, the header, OK acknowledges and the next event comes.
func TestListenerPool_DeliversAndAcknowledges(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, _, _ := startDaemonOut(t, e.pool("l", "ok", "")+e.prog)
	waitReady(t, client, "l")
	emit(t, client)
	emit(t, client)
	s := waitSerials(t, e.stderr("l"), 2, 5*time.Second)
	out := waitFile(t, e.stderr("l"), "eventname:PROCESS_STATE_EXITED", 5*time.Second)
	if !regexp.MustCompile(`ver:3\.0 server:kahi serial:\d+ pool:l poolserial:1 eventname:PROCESS_STATE_EXITED len:\d+`).MatchString(out) {
		t.Errorf("header:\n%s", out)
	}
	var a, b int
	fmt.Sscan(s[0], &a)
	fmt.Sscan(s[1], &b)
	if b != a+1 || len(serials(t, e.stderr("l"))) != 2 {
		t.Errorf("serials %v, want N then N+1 once each", serials(t, e.stderr("l")))
	}
}

// Step 4: FAIL redelivers the same serial; the third failure drops it.
func TestListenerPool_FailDropsAfterThree(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, _, out := startDaemonOut(t, e.pool("l", "fail", "")+e.prog)
	waitReady(t, client, "l")
	emit(t, client)
	s := waitSerials(t, e.stderr("l"), 3, 10*time.Second)
	if s[0] != s[1] || s[1] != s[2] {
		t.Fatalf("serials %v, want one serial three times", s)
	}
	waitOutputFile(t, out, fmt.Sprintf("event %s for l dropped after 3 failures", s[0]), 5*time.Second)
	emit(t, client)
	s = waitSerials(t, e.stderr("l"), 4, 5*time.Second)
	if s[3] == s[0] {
		t.Errorf("after the drop: %v", s)
	}
}

// Step 5: a member that exits while holding an event; its restart gets it.
func TestListenerPool_ExitRedelivers(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, _, out := startDaemonOut(t, e.pool("l", "exit", "autorestart = true")+e.prog)
	pid := waitReady(t, client, "l")
	emit(t, client)
	waitOutputFile(t, out, "event listener l exited unexpectedly", 5*time.Second)
	s := waitSerials(t, e.stderr("l"), 2, 10*time.Second)
	if s[0] != s[1] {
		t.Fatalf("serials %v, want the same serial redelivered", s)
	}
	if info, _ := getProcessInfo(client, "l"); info.PID == pid {
		t.Errorf("pid unchanged (%d)", pid)
	}
}

// Step 6: events beyond buffer_size while no member is READY drop the
// oldest with one warning.
func TestListenerPool_BufferOverflow(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	slow := writeScript(t, e.dir, "slow.sh", "sleep 30\n")
	cfg := fmt.Sprintf("[eventlisteners.l]\ncommand = %q\nevents = [\"PROCESS_STATE_EXITED\"]\nbuffer_size = 2\nstartsecs = 0\nstopwaitsecs = 2\nstopasgroup = true\nkillasgroup = true\n\n", slow) + e.prog
	client, _, out := startDaemonOut(t, cfg)
	waitForState(t, client, "l", "RUNNING", 5*time.Second)
	for range 3 {
		emit(t, client)
	}
	waitOutputFile(t, out, "event buffer overflow for l, dropped", 15*time.Second)
}

// Step 7: an invalid result is logged and the event redelivered.
func TestListenerPool_InvalidResult(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, _, out := startDaemonOut(t, e.pool("l", "garbage", "")+e.prog)
	waitReady(t, client, "l")
	emit(t, client)
	waitOutputFile(t, out, "invalid result from listener l: garbage", 5*time.Second)
	s := waitSerials(t, e.stderr("l"), 2, 5*time.Second)
	if s[0] != s[1] {
		t.Fatalf("serials %v", s)
	}
}

// Step 8: no RESULT within result_timeout: the member is stopped and
// restarted, and the new member gets the event.
func TestListenerPool_ResultTimeout(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, _, out := startDaemonOut(t, e.pool("l", "hang", "result_timeout = 1\nautorestart = true")+e.prog)
	pid := waitReady(t, client, "l")
	emit(t, client)
	s := waitSerials(t, e.stderr("l"), 1, 5*time.Second)
	waitOutputFile(t, out, "listener l timed out on event "+s[0], 5*time.Second)
	s = waitSerials(t, e.stderr("l"), 2, 10*time.Second)
	if s[0] != s[1] {
		t.Fatalf("serials %v", s)
	}
	if info, _ := getProcessInfo(client, "l"); info.PID == pid {
		t.Errorf("pid unchanged (%d)", pid)
	}
}

// Step 9: numprocs = 3, round-robin over READY members.
func TestListenerPool_RoundRobin(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	cfg := strings.Replace(e.pool("l", "ok", "numprocs = 3\nprocess_name = \"l_%(process_num)d\""), fmt.Sprintf("stderr_logfile = %q", e.stderr("l")),
		fmt.Sprintf("stderr_logfile = %q", filepath.Join(e.dir, "l_%(process_num)d.err")), 1)
	client, _, _ := startDaemonOut(t, cfg+e.prog)
	for _, n := range []string{"l_0", "l_1", "l_2"} {
		waitReady(t, client, n)
	}
	for range 3 {
		emit(t, client)
	}
	for _, n := range []string{"l_0", "l_1", "l_2"} {
		if s := waitSerials(t, filepath.Join(e.dir, n+".err"), 1, 5*time.Second); len(s) != 1 {
			t.Errorf("%s got %v, want one event", n, s)
		}
	}
	emit(t, client)
	waitSerials(t, filepath.Join(e.dir, "l_0.err"), 2, 5*time.Second)
}

// Step 10: two event types.
func TestListenerPool_TwoTypes(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	cfg := strings.Replace(e.pool("l", "ok", ""), `events = ["PROCESS_STATE_EXITED"]`, `events = ["PROCESS_STATE_EXITED", "PROCESS_STATE_FATAL"]`, 1)
	bad := fmt.Sprintf("[programs.bad]\ncommand = %q\nautostart = false\nstartretries = 0\nstartsecs = 1\n", filepath.Join(e.dir, "missing"))
	client, _, _ := startDaemonOut(t, cfg+e.prog+bad)
	waitReady(t, client, "l")
	emit(t, client)
	_ = client.Start("bad")
	waitFile(t, e.stderr("l"), "eventname:PROCESS_STATE_EXITED", 5*time.Second)
	waitFile(t, e.stderr("l"), "eventname:PROCESS_STATE_FATAL", 10*time.Second)
}

// Steps 13, 15 and 20: status, stop/start, the config API and the stdin
// refusal.
func TestListenerPool_ControlAndAPI(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, socket, _ := startDaemonOut(t, fmt.Sprintf("[eventlisteners.l]\ncommand = \"%s ok\"\nevents = [\"PROCESS_STATE_EXITED\"]\nstartsecs = 0\nstopwaitsecs = 2\n", e.listener))
	waitForState(t, client, "l", "RUNNING", 5*time.Second)
	if err := client.Stop("l"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "l", "STOPPED", 5*time.Second)
	if err := client.Start("l"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "l", "RUNNING", 5*time.Second)
	stdout, _, code := runKahi(t, e.dir, "ctl", "-s", socket, "status")
	if code != 0 || !strings.Contains(stdout, "l") || !strings.Contains(stdout, "RUNNING") {
		t.Errorf("status: %d %q", code, stdout)
	}

	r := apiDo(t, socket, "GET", "/api/v1/config", "")
	if !strings.Contains(string(r.body), `"BufferSize":10`) || !strings.Contains(string(r.body), `"ResultTimeout":60`) {
		t.Errorf("config API lacks the pool defaults: %s", r.body)
	}

	r = apiDo(t, socket, "POST", "/api/v1/processes/l/stdin", `{"data":"x"}`)
	if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "stdin carries the event listener protocol") {
		t.Errorf("stdin API: %d %s", r.status, r.body)
	}
	_, stderr, code := runKahi(t, e.dir, "ctl", "-s", socket, "send", "l", "x")
	if code != 1 || !strings.Contains(stderr, "stdin carries the event listener protocol") {
		t.Errorf("ctl send: %d %q", code, stderr)
	}
}

// Step 14: a pool added by a reload runs.
func TestListenerPool_AddedByReload(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	client, socket, _ := startDaemonOut(t, e.prog)
	rewriteConfig(t, socket, e.prog+e.pool("m", "ok", ""))
	stdout, _, code := runKahi(t, e.dir, "ctl", "-s", socket, "update")
	if code != 0 || !strings.Contains(stdout, "added=[m]") {
		t.Fatalf("update: %d %q", code, stdout)
	}
	waitReady(t, client, "m")
	emit(t, client)
	waitSerials(t, e.stderr("m"), 1, 5*time.Second)
}

// Steps 17 and 18: a changed pool keeps its buffered events for the new
// member; a removed pool discards them.
func TestListenerPool_ReloadKeepsOrDiscardsBuffer(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	slow := writeScript(t, e.dir, "slow-then-ok.sh", fmt.Sprintf("if [ ! -f %q ]; then : > %q; exec sleep 300; fi\nexec %s ok\n",
		filepath.Join(e.dir, "slow.marker"), filepath.Join(e.dir, "slow.marker"), e.listener))
	section := func(v string) string {
		return fmt.Sprintf("[eventlisteners.l]\ncommand = %q\nevents = [\"PROCESS_STATE_EXITED\"]\nstartsecs = 0\nstopwaitsecs = 2\nstopasgroup = true\nkillasgroup = true\nstderr_logfile = %q\nenvironment = { V = %q }\n\n", slow, e.stderr("l"), v)
	}
	client, socket, _ := startDaemonOut(t, section("v1")+e.prog+e.pool("m", "ok", ""))
	pid := waitReady(t, client, "l")
	waitReady(t, client, "m")
	emit(t, client)
	emit(t, client)
	// m got both; l has them buffered (its member sleeps before READY).
	mSerials := waitSerials(t, e.stderr("m"), 2, 5*time.Second)

	rewriteConfig(t, socket, section("v2")+e.prog+e.pool("m", "ok", ""))
	if _, stderr, code := runKahi(t, e.dir, "ctl", "-s", socket, "update"); code != 0 {
		t.Fatalf("update: %d %s", code, stderr)
	}
	lSerials := waitSerials(t, e.stderr("l"), 2, 10*time.Second)
	if info, _ := getProcessInfo(client, "l"); info.PID == pid {
		t.Errorf("l pid unchanged after the change")
	}
	if lSerials[0] == lSerials[1] {
		t.Errorf("l serials %v, want two events", lSerials)
	}
	_ = mSerials

	// Remove m while it holds buffered events: stop its member first so
	// events queue, then remove the pool; l (the sentinel) gets the next
	// events and nothing is redelivered elsewhere.
	if err := client.Stop("m"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "m", "STOPPED", 5*time.Second)
	emit(t, client)
	waitSerials(t, e.stderr("l"), 3, 5*time.Second)
	rewriteConfig(t, socket, section("v2")+e.prog)
	stdout, stderr, code := runKahi(t, e.dir, "ctl", "-s", socket, "update")
	if code != 0 || !strings.Contains(stdout, "removed=[m]") {
		t.Fatalf("update: %d %q %q", code, stdout, stderr)
	}
	if _, err := getProcessInfo(client, "m"); err == nil {
		t.Error("m still in status")
	}
	emit(t, client)
	waitSerials(t, e.stderr("l"), 4, 5*time.Second)
	if n := len(serials(t, e.stderr("m"))); n != 2 {
		t.Errorf("m got %d events, want 2", n)
	}
}

// Framing steps 1, 2 and 4 (SEC-020): log events reach a listener as the
// supervisord log body, length-framed, so a forged header in program output
// stays inside the body.
func TestListenerPool_LogEventFraming(t *testing.T) {
	t.Parallel()
	e := newPoolEnv(t)
	printer := writeScript(t, e.dir, "printer.sh", "printf 'x\\nver:3.0 server:kahi serial:999 len:0\\n'\nprintf '\\n'\nprintf 'hello\\n'\nexec sleep 300\n")
	cfg := strings.Replace(e.pool("l", "hex", ""), `events = ["PROCESS_STATE_EXITED"]`, `events = ["PROCESS_LOG_STDOUT"]`, 1) +
		fmt.Sprintf("[programs.pr]\ncommand = %q\nautostart = false\nstartsecs = 0\nstopwaitsecs = 2\nstopasgroup = true\nkillasgroup = true\nstdout_events_enabled = true\n\n[programs.pe]\ncommand = %q\nautostart = false\nstartsecs = 0\nstopwaitsecs = 2\nstopasgroup = true\nkillasgroup = true\nstdout_events_enabled = true\n", printer, writeScript(t, e.dir, "empty.sh", "printf '\\n'\nexec sleep 300\n"))
	client, _, _ := startDaemonOut(t, cfg)
	waitReady(t, client, "l")
	if err := client.Start("pr"); err != nil {
		t.Fatal(err)
	}
	out := waitFile(t, e.stderr("l"), "body=", 10*time.Second)
	time.Sleep(time.Second)
	b, _ := os.ReadFile(e.stderr("l"))
	out = string(b)
	if strings.Count(out, "ver:3.0") != strings.Count(out, "body=") {
		t.Errorf("a forged header was parsed as a frame:\n%s", out)
	}
	if strings.Contains(out, "serial:999 pool") {
		t.Errorf("forged serial reached the header position:\n%s", out)
	}
	head := fmt.Sprintf("%x", "processname:pr groupname:pr channel:stdout\n")
	if !strings.Contains(out, "body="+head) {
		t.Errorf("body does not start with the supervisord log header:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%x", "ver:3.0 server:kahi serial:999 len:0")) {
		t.Errorf("the forged line is not in a body:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%x", "hello\n")) {
		t.Errorf("hello not delivered:\n%s", out)
	}

	// Step 2: an empty line is the log header, a newline and the one
	// output byte; len counts both.
	if err := client.Start("pe"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("body=%x", "processname:pe groupname:pe channel:stdout\n\n")
	waitFile(t, e.stderr("l"), want+"\n", 10*time.Second)
	if !strings.Contains(readFile(t, e.stderr("l")), fmt.Sprintf("len:%d\n", len("processname:pe groupname:pe channel:stdout\n\n"))) {
		t.Errorf("len does not count the header newline and the output byte:\n%s", readFile(t, e.stderr("l")))
	}
}
