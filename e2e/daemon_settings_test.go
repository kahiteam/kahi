//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Daemon startup settings: [supervisor] directory, umask, minfds, minprocs
// (FUNC-047, FUNC-067, FUNC-068, ADR-047; #113, #160).

// rawDaemon starts "kahi daemon args..." in dir, through "/bin/sh -c
// 'prefix; exec kahi ...'" when prefix is set, and waits for socket.
func rawDaemon(t *testing.T, dir, socket, prefix string, args ...string) (*ctl.Client, *exec.Cmd, *os.File) {
	t.Helper()
	var cmd *exec.Cmd
	if prefix != "" {
		quoted := make([]string, 0, len(args)+2)
		for _, a := range append([]string{kahiBinary, "daemon"}, args...) {
			quoted = append(quoted, "'"+a+"'")
		}
		cmd = exec.Command("/bin/sh", "-c", prefix+"; exec "+strings.Join(quoted, " "))
	} else {
		cmd = exec.Command(kahiBinary, append([]string{"daemon"}, args...)...)
	}
	cmd.Dir = dir
	out := daemonOutput(t)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	client := ctl.NewUnixClient(socket)
	waitForHealth(t, client, 3*time.Second)
	return client, cmd, out
}

// realDir is shortDir with symlinks resolved, so it compares equal to what
// pwd prints (macOS /var is /private/var).
func realDir(t *testing.T) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(shortDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitTail(t *testing.T, client *ctl.Client, name, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		if err := client.Tail(name, "stdout", 4096, &buf); err == nil && strings.Contains(buf.String(), want) {
			return buf.String()
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s output never contained %q (got %q)", name, want, buf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitOutputFile(t *testing.T, f *os.File, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(f.Name())
		if strings.Contains(string(b), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon output lacks %q after %v:\n%s", want, timeout, b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDaemonSettings_Directory(t *testing.T) {
	t.Parallel()
	start, d, d2 := realDir(t), realDir(t), realDir(t)
	writeScript(t, d, "hello.sh", "echo hello-from-D\nexec sleep 300")
	pwdScript := writeScript(t, start, "pwd.sh", "pwd\nexec sleep 300")
	socket := filepath.Join(start, "k.sock")
	cfg := func(dir string) string {
		return fmt.Sprintf(`[supervisor]
directory = %q
shutdown_timeout = 10

[server.unix]
file = %q

[programs.hello]
command = "./hello.sh"
startsecs = 0

[programs.pwd]
command = %q
startsecs = 0
`, dir, socket, pwdScript)
	}
	if err := os.WriteFile(filepath.Join(start, "kahi.toml"), []byte(cfg(d)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Relative -c and -p: the config path is made absolute before the
	// directory change, the PID file resolves after it.
	client, _, out := rawDaemon(t, start, socket, "", "-c", "kahi.toml", "-p", "kahi.pid")
	waitTail(t, client, "hello", "hello-from-D")
	waitTail(t, client, "pwd", d)
	if _, err := os.Stat(filepath.Join(d, "kahi.pid")); err != nil {
		t.Errorf("relative PID file not created in the daemon directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(start, "kahi.toml"), []byte(cfg(d2)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Reload(); err != nil {
		t.Fatalf("reload with a relative -c after the directory change: %v", err)
	}
	waitOutputFile(t, out, "supervisor.directory change takes effect after restart", 5*time.Second)
}

func TestDaemonSettings_InheritedDirectory(t *testing.T) {
	t.Parallel()
	start := realDir(t)
	pwdScript := writeScript(t, start, "pwd.sh", "pwd\nexec sleep 300")
	client, _ := startDaemonIn(t, start, `
[programs.pwd]
command = "`+pwdScript+`"
startsecs = 0
`)
	waitTail(t, client, "pwd", start)
}

// startDaemonIn is startDaemon with the daemon started in dir.
func startDaemonIn(t *testing.T, dir, body string) (*ctl.Client, *os.File) {
	t.Helper()
	socket := filepath.Join(dir, "k.sock")
	cfg := fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n%s", socket, body)
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	client, _, out := rawDaemon(t, dir, socket, "", "-c", path)
	return client, out
}

func TestDaemonSettings_DirectoryErrors(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	socket := filepath.Join(dir, "k.sock")
	missing := filepath.Join(dir, "nonexistent")
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("[supervisor]\ndirectory = %q\n\n[server.unix]\nfile = %q\n", missing, socket)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "daemon", "-c", path)
	if code != 1 || !strings.Contains(stderr, "directory does not exist: "+missing) {
		t.Errorf("exit %d, stderr %q; want 1 and 'directory does not exist: %s'", code, stderr, missing)
	}
	if _, err := os.Stat(socket); err == nil {
		t.Error("socket created although the directory change failed")
	}

	if os.Geteuid() == 0 {
		return // root ignores the mode below
	}
	locked := filepath.Join(dir, "locked")
	inner := filepath.Join(locked, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := os.WriteFile(path, []byte(fmt.Sprintf("[supervisor]\ndirectory = %q\n\n[server.unix]\nfile = %q\n", inner, socket)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runKahi(t, dir, "daemon", "-c", path)
	if code != 1 || !strings.Contains(stderr, "cannot access directory: "+inner+": permission denied") {
		t.Errorf("exit %d, stderr %q; want 1 and 'cannot access directory: %s: permission denied'", code, stderr, inner)
	}
}

func TestProcessDirectory_SpawnErrors(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	missing := filepath.Join(dir, "nonexistent")
	late := filepath.Join(dir, "late")
	locked := filepath.Join(dir, "locked")
	inner := filepath.Join(locked, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	nonRoot := os.Geteuid() != 0
	if nonRoot {
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	}
	pwdScript := writeScript(t, dir, "pwd.sh", "pwd\nexec sleep 300")
	client, out := startDaemonIn(t, dir, fmt.Sprintf(`
[programs.missing]
command = "/bin/sleep 300"
directory = %q
startsecs = 0
startretries = 2

[programs.late]
command = "/bin/sleep 300"
directory = %q
startsecs = 0
startretries = 10

[programs.locked]
command = "/bin/sleep 300"
directory = %q
startsecs = 0
startretries = 0

[programs.p]
command = %q
directory = "%%(here)s/%%(program_name)s"
startsecs = 0
`, missing, late, inner, pwdScript))

	waitOutputFile(t, out, "directory does not exist: "+late, 5*time.Second)
	if err := os.Mkdir(late, 0o755); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "late", "RUNNING", 30*time.Second)

	waitForState(t, client, "missing", "FATAL", 30*time.Second)
	info, err := getProcessInfo(client, "missing")
	if err != nil || info.Description != "spawn error: directory does not exist: "+missing {
		t.Errorf("missing: %+v (%v), want description 'spawn error: directory does not exist: %s'", info, err, missing)
	}
	b, _ := os.ReadFile(out.Name())
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"msg":"spawn failed"`) && strings.Contains(line, "directory does not exist: "+missing) {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d spawn attempts logged for missing, want 3 (startretries 2)", n)
	}

	if nonRoot {
		waitForState(t, client, "locked", "FATAL", 10*time.Second)
		info, _ := getProcessInfo(client, "locked")
		if want := "spawn error: cannot access directory: " + inner + ": permission denied"; info.Description != want {
			t.Errorf("locked description %q, want %q", info.Description, want)
		}
	}
	waitTail(t, client, "p", filepath.Join(dir, "p"))
}

func TestDaemonSettings_Umask(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	socket := filepath.Join(dir, "k.sock")
	logfile := filepath.Join(dir, "kahi.log")
	var um string
	cfg := func(daemonUmask, u3 string) string {
		return fmt.Sprintf(`[supervisor]
umask = %[1]q
logfile = %[2]q
shutdown_timeout = 10

[server.unix]
file = %[3]q

[programs.a]
command = %[5]q
startsecs = 0

[programs.b]
command = %[5]q
umask = "0077"
startsecs = 0

[programs.u3]
command = %[5]q
umask = %[4]q
startsecs = 0

[programs.u4]
command = %[5]q
umask = "0022"
startsecs = 0
`, daemonUmask, logfile, socket, u3, um)
	}
	path := filepath.Join(dir, "kahi.toml")
	um = writeScript(t, dir, "umask.sh", "umask\nexec sleep 300")
	if err := os.WriteFile(path, []byte(cfg("0027", "022")), 0o644); err != nil {
		t.Fatal(err)
	}
	client, _, _ := rawDaemon(t, dir, socket, "umask 0002", "-c", path)
	waitTail(t, client, "a", "0027")
	waitTail(t, client, "b", "0077")
	waitTail(t, client, "u3", "0022")
	waitTail(t, client, "u4", "0022")
	if fi, err := os.Stat(logfile); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("daemon log mode %v (%v), want 0640 under umask 0027", fi.Mode().Perm(), err)
	}
	pids := map[string]int{}
	for _, n := range []string{"u3", "u4"} {
		info, _ := getProcessInfo(client, n)
		pids[n] = info.PID
	}

	// "022" and "0022" are equal: no change. A [supervisor] umask change
	// needs a restart.
	if err := os.WriteFile(path, []byte(cfg("0007", "0022")), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runKahi(t, dir, "ctl", "-s", socket, "reload")
	if code != 0 || !strings.Contains(stdout, "added=[] changed=[] removed=[]") {
		t.Errorf("reload: exit %d, stdout %q, stderr %q; want no change", code, stdout, stderr)
	}
	for n, pid := range pids {
		if info, _ := getProcessInfo(client, n); info.PID != pid {
			t.Errorf("%s restarted by an equal umask spelling", n)
		}
	}
	logs, _ := os.ReadFile(logfile)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(string(logs), "supervisor.umask change takes effect after restart") {
		if time.Now().After(deadline) {
			t.Fatalf("daemon log lacks the umask restart notice:\n%s", logs)
		}
		time.Sleep(50 * time.Millisecond)
		logs, _ = os.ReadFile(logfile)
	}
	if err := client.Restart("a"); err != nil {
		t.Fatal(err)
	}
	// The buffer keeps the first run's line: wait for the second, then make
	// sure the reloaded umask never showed.
	deadline = time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		_ = client.Tail("a", "stdout", 4096, &buf)
		out := buf.String()
		if strings.Contains(out, "0007") {
			t.Fatalf("restarted program saw the reloaded umask: %q", out)
		}
		if strings.Count(out, "0027") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restarted program printed no second umask line: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDaemonSettings_InheritedUmaskAndSocket(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	socket := filepath.Join(dir, "k.sock")
	path := filepath.Join(dir, "kahi.toml")
	um := writeScript(t, dir, "umask.sh", "umask\nexec sleep 300")
	body := fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n[programs.a]\ncommand = %q\nstartsecs = 0\n", socket, um)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	client, _, _ := rawDaemon(t, dir, socket, "umask 0002", "-c", path)
	waitTail(t, client, "a", "0002")

	dir2 := realDir(t)
	socket2 := filepath.Join(dir2, "k.sock")
	path2 := filepath.Join(dir2, "kahi.toml")
	if err := os.WriteFile(path2, []byte(fmt.Sprintf("[supervisor]\numask = \"000\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n", socket2)), 0o644); err != nil {
		t.Fatal(err)
	}
	rawDaemon(t, dir2, socket2, "", "-c", path2)
	if fi, err := os.Stat(socket2); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("socket mode %v (%v) under umask 000, want 0700", fi.Mode().Perm(), err)
	}
}

func TestDaemonSettings_InvalidUmask(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	for _, c := range []struct{ body, want string }{
		{"[supervisor]\numask = \"0999\"\n", "invalid umask: 0999"},
		{"[supervisor]\numask = \"abc\"\n", "invalid umask: abc"},
		{"[programs.p]\ncommand = \"/bin/true\"\numask = \"01777\"\n", "invalid umask: 01777"},
	} {
		path := filepath.Join(dir, "kahi.toml")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := runKahi(t, dir, "daemon", "-c", path); code != 1 || !strings.Contains(stderr, c.want) {
			t.Errorf("%q: exit %d, stderr %q; want 1 and %q", c.body, code, stderr, c.want)
		}
	}
}

func TestDaemonSettings_LimitsReloadAndMigrate(t *testing.T) {
	t.Parallel()
	dir := realDir(t)
	socket := filepath.Join(dir, "k.sock")
	path := filepath.Join(dir, "kahi.toml")
	write := func(minfds int) {
		body := fmt.Sprintf("[supervisor]\nminfds = %d\nnocleanup = true\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n", minfds, socket)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(64)
	client, _, out := rawDaemon(t, dir, socket, "", "-c", path)
	waitOutputFile(t, out, "supervisor running", 5*time.Second)
	b, _ := os.ReadFile(out.Name())
	if strings.Contains(string(b), "supervisor.minfds") {
		t.Errorf("minfds warning although minfds is implemented:\n%s", b)
	}
	if !strings.Contains(string(b), "supervisor.nocleanup has no effect: kahi does not auto-name child logs") {
		t.Errorf("no nocleanup warning:\n%s", b)
	}
	write(128)
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	waitOutputFile(t, out, "supervisor.minfds change takes effect after restart", 5*time.Second)

	conf := filepath.Join(dir, "supervisord.conf")
	if err := os.WriteFile(conf, []byte("[supervisord]\nminfds=4096\nminprocs=500\ndirectory=/srv\numask=022\nnocleanup=true\nchildlogdir=/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runKahi(t, dir, "migrate", conf)
	if code != 0 {
		t.Fatalf("migrate: exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"[supervisor]", "minfds = 4096", "minprocs = 500", `directory = "/srv"`, `umask = "022"`,
		"# UNSUPPORTED: nocleanup = true (has no effect: kahi does not auto-name child logs)",
		"# UNSUPPORTED: childlogdir = /x (has no effect: kahi does not auto-name child logs)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("migrate output lacks %q:\n%s", want, stdout)
		}
	}
}

// Linux only: prlimit lowers the hard limit for the daemon.
func TestDaemonSettings_ExplicitMinfdsAboveHard(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("prlimit is Linux-only")
	}
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		t.Skip("prlimit not installed")
	}
	dir := realDir(t)
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("[supervisor]\nminfds = 4096\n\n[server.unix]\nfile = %q\n", filepath.Join(dir, "k.sock"))), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, prlimit, "--nofile=1024:2048", kahiBinary, "daemon", "-c", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	// Go raises the soft limit (to about the hard limit) before kahi reads
	// it, so only the fixed parts are compared.
	if err == nil || !strings.Contains(stderr.String(), "cannot set NOFILE to 4096: hard limit is 2048 (current soft=") || !strings.Contains(stderr.String(), ", hard=2048)") {
		t.Errorf("err %v, stderr %q; want exit 1 and the NOFILE hard-limit error", err, stderr.String())
	}
}

// With -d the parent checks a relative PID file against [supervisor]
// directory, as the child resolves it, and refuses a second start.
func TestDaemonSettings_DaemonizeWithDirectory(t *testing.T) {
	t.Parallel()
	start, d := realDir(t), realDir(t)
	socket := filepath.Join(start, "k.sock")
	path := filepath.Join(start, "kahi.toml")
	cfg := fmt.Sprintf("[supervisor]\ndirectory = %q\nlogfile = %q\nshutdown_timeout = 5\n\n[server.unix]\nfile = %q\n", d, filepath.Join(start, "kahi.log"), socket)
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runKahi(t, start, "daemon", "-d", "-c", "kahi.toml", "-p", "kahi.pid")
	if code != 0 {
		t.Fatalf("daemon -d: exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	pidfile := filepath.Join(d, "kahi.pid")
	pid := readPID(t, pidfile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if _, err := os.Stat(filepath.Join(start, "kahi.pid")); err == nil {
		t.Error("PID file written in the start directory, not in [supervisor] directory")
	}
	if _, stderr, code := runKahi(t, start, "daemon", "-d", "-c", "kahi.toml", "-p", "kahi.pid"); code != 1 || !strings.Contains(stderr, "another instance running") || !strings.Contains(stderr, pidfile) {
		t.Errorf("second -d start: exit %d, stderr %q; want 1 and 'another instance running' naming %s", code, stderr, pidfile)
	}
	if _, stderr, code := runKahi(t, start, "ctl", "-s", socket, "shutdown"); code != 0 {
		t.Fatalf("ctl shutdown: exit %d, %q", code, stderr)
	}
	waitForDeath(t, pid, 5*time.Second)
}
