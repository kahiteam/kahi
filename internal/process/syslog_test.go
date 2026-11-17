package process

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The syslog line handler forwards per stream, follows redirect_stderr,
// strips ANSI, and its sinks close with the process (FUNC-020).
func TestManagerSyslogHandler(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ksl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "log.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	recv := func() string {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(string(buf[:n]), "\n")
	}

	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.SetSyslogAddress("unix://" + path)
	mgr.mu.Lock()
	cfg := defaultProgramConfig()
	cfg.StderrSyslog = true
	cfg.RedirectStderr = true // stderr follows stdout_syslog, which is off
	if h := mgr.syslogHandlerLocked("none", cfg); h != nil {
		t.Error("redirect_stderr without stdout_syslog forwards nothing")
	}
	cfg = defaultProgramConfig()
	cfg.StdoutSyslog, cfg.StripAnsi, cfg.RedirectStderr = true, true, true
	h := mgr.syslogHandlerLocked("web", cfg)
	cfg2 := defaultProgramConfig()
	cfg2.StderrSyslog = true
	h2 := mgr.syslogHandlerLocked("err", cfg2)
	mgr.mu.Unlock()

	h("web", "stderr", []byte("\x1b[31mred\x1b[0m"))
	if m := recv(); !strings.HasSuffix(m, " web["+strconv.Itoa(os.Getpid())+"]: red") {
		t.Errorf("redirected, stripped message %q", m)
	}
	h2("err", "stdout", []byte("not forwarded"))
	h2("err", "stderr", []byte("forwarded"))
	if m := recv(); !strings.HasSuffix(m, ": forwarded") {
		t.Errorf("stderr message %q", m)
	}

	mgr.mu.Lock()
	n := len(mgr.syslogs)
	mgr.deleteProcessLocked("web")
	left := len(mgr.syslogs)
	mgr.mu.Unlock()
	if n != 2 || left != 1 {
		t.Errorf("sinks %d, after delete %d", n, left)
	}
	mgr.CloseLogs()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if len(mgr.syslogs) != 0 {
		t.Error("CloseLogs left sinks")
	}
}
