//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// leakedProcesses returns the processes a test leaked: still running after
// the suite, re-parented to init, in the test binary's session and started
// after start. Daemons and their programs stay in that session (programs only
// get their own process group), so a daemon killed without stopping its
// programs shows up here (#242), as does the child of a program's shell that
// the daemon SIGKILLed. Processes of other test packages running at the same
// time still have their parents, so they do not match.
func leakedProcesses(start time.Time) ([]string, []int) {
	mySid, err := unix.Getsid(0)
	if err != nil {
		return []string{"getsid: " + err.Error()}, nil
	}
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=,etime=,args=").Output()
	if err != nil {
		return []string{"ps: " + err.Error()}, nil
	}
	// etime has whole seconds and can round up, hence the slack.
	age := time.Since(start) + 2*time.Second
	var lines []string
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] != "1" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid == os.Getpid() {
			continue
		}
		if elapsed, ok := parseEtime(f[2]); !ok || elapsed > age {
			continue
		}
		if sid, err := unix.Getsid(pid); err != nil || sid != mySid {
			continue
		}
		lines = append(lines, strings.TrimSpace(line)+"  (cwd "+processCwd(pid)+")")
		pids = append(pids, pid)
	}
	return lines, pids
}

// processCwd returns a process's working directory, which is its daemon's
// and usually names the leaking test's temp dir, or "?".
func processCwd(pid int) string {
	if dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		return dir
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "n") {
				return l[1:]
			}
		}
	}
	return "?"
}

// parseEtime parses ps etime, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, bool) {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var secs int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, false
		}
		secs = secs*60 + n
	}
	return time.Duration(days*86400+secs) * time.Second, true
}

// checkLeaks reports and kills processes the suite leaked, after giving
// stopping processes a few seconds to exit. It returns 1 if any were found.
func checkLeaks(start time.Time) int {
	var lines []string
	var pids []int
	for deadline := time.Now().Add(5 * time.Second); ; {
		lines, pids = leakedProcesses(start)
		if len(lines) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(lines) == 0 {
		return 0
	}
	fmt.Fprintf(os.Stderr, "e2e: %d processes left running after the suite: a daemon was killed without stopping its programs (use stopDaemon), or a program's shell was killed while a child it forked ran on (exec the last command):\n", len(lines))
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, "  "+l)
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return 1
}
