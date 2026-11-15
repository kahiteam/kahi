//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// minfds / minprocs and the per-process directory check, as root in a
// container (ADR-037; FUNC-067, FUNC-068, ADR-047).

func limitsConfig(supervisor string) string {
	return fmt.Sprintf(`[supervisor]
shutdown_timeout = 5
%s

[server.unix]
file = "/run/kahi.sock"

[programs.limits]
command = "/orphaner limits"
user = "1001"
startsecs = 0
autorestart = "false"

# The daemon's own limits (PID 1): children inherit its NOFILE soft limit
# once kahi has called Setrlimit, but a Go child raises its own.
[programs.daemon]
command = "/orphaner limits 1"
startsecs = 0
autorestart = "false"
`, supervisor)
}

func TestRootContainer_MinfdsMinprocs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, program, supervisor, ulimit, want string
	}{
		// NPROC: Go leaves it alone, so the child sees the raised soft
		// limit itself.
		{"raise soft NPROC", "limits", "minprocs = 512", "nproc=100:1024", "NPROC 512 1024"},
		// NOFILE: an explicit minfds sets the soft limit to exactly minfds,
		// raised or lowered (decided 2026-11-15); children inherit it.
		{"explicit minfds raises", "daemon", "minfds = 4096", "nofile=1024:8192", "NOFILE 4096 8192"},
		{"explicit minfds lowers", "daemon", "minfds = 4096", "nofile=8192:8192", "NOFILE 4096 8192"},
		// Without minfds NOFILE is left alone.
		{"default untouched", "daemon", "", "nofile=8192:8192", "NOFILE 8192 8192"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ct := startRootContainer(t, map[string]string{"kahi.toml": limitsConfig(c.supervisor)}, []string{"--ulimit", c.ulimit}, nil)
			ct.waitLogs(`"msg":"supervisor running"`)
			prefix := strings.Fields(c.want)[0] + " "
			if got := ct.waitOutput(c.program, prefix); got != c.want {
				t.Errorf("%s limits %q, want %q", c.program, got, c.want)
			}
		})
	}
}

func TestRootContainer_LimitsAboveHard(t *testing.T) {
	t.Parallel()
	t.Run("explicit minprocs fails", func(t *testing.T) {
		t.Parallel()
		ct := startRootContainer(t, map[string]string{"kahi.toml": limitsConfig("minprocs = 512")},
			[]string{"--ulimit", "nproc=100:100", "--cap-drop", "SYS_RESOURCE"}, nil)
		ct.waitLogs("cannot set NPROC to 512: hard limit is 100 (current soft=100, hard=100)")
	})
	t.Run("default minprocs warns", func(t *testing.T) {
		t.Parallel()
		ct := startRootContainer(t, map[string]string{"kahi.toml": limitsConfig("")},
			[]string{"--ulimit", "nproc=100:100", "--cap-drop", "SYS_RESOURCE"}, nil)
		logs := ct.waitLogs(`"msg":"supervisor running"`)
		if !strings.Contains(logs, "NPROC is 100, below the default minprocs 200; set supervisor.minprocs = 100 or lower to silence this") {
			t.Errorf("no default-value warning:\n%s", logs)
		}
	})
	t.Run("explicit minfds fails", func(t *testing.T) {
		t.Parallel()
		ct := startRootContainer(t, map[string]string{"kahi.toml": limitsConfig("minfds = 4096")},
			[]string{"--ulimit", "nofile=1024:2048", "--cap-drop", "SYS_RESOURCE"}, nil)
		// Go has raised the soft limit (to about the hard limit) before kahi
		// reads it, so only the fixed parts are compared.
		logs := ct.waitLogs("cannot set NOFILE to 4096: hard limit is 2048 (current soft=")
		if !strings.Contains(logs, ", hard=2048)") {
			t.Errorf("error lacks the hard limit:\n%s", logs)
		}
	})
	t.Run("default minfds warns", func(t *testing.T) {
		t.Parallel()
		ct := startRootContainer(t, map[string]string{"kahi.toml": limitsConfig("")},
			[]string{"--ulimit", "nofile=512:512", "--cap-drop", "SYS_RESOURCE"}, nil)
		logs := ct.waitLogs(`"msg":"supervisor running"`)
		if !strings.Contains(logs, "NOFILE is 512, below the default minfds 1024; set supervisor.minfds = 512 or lower to silence this") {
			t.Errorf("no default-value warning:\n%s", logs)
		}
	})
	t.Run("root raises the hard limit", func(t *testing.T) {
		t.Parallel()
		ct := startRootContainer(t, map[string]string{"kahi.toml": limitsConfig("minfds = 4096")},
			[]string{"--ulimit", "nofile=1024:2048", "--cap-add", "SYS_RESOURCE"}, nil)
		var logs string
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			if logs = ct.logs(); strings.Contains(logs, `"msg":"supervisor running"`) || strings.Contains(logs, "cannot set NOFILE") {
				break
			}
		}
		if strings.Contains(logs, "cannot set NOFILE") {
			t.Skipf("runtime does not let root raise the hard limit (rootless podman):\n%s", logs)
		}
		if got := ct.waitOutput("daemon", "NOFILE "); got != "NOFILE 4096 4096" {
			t.Errorf("daemon limits %q, want NOFILE 4096 4096", got)
		}
	})
}

// A per-process directory the target user cannot enter is a spawn error
// that names the directory, checked as that user (FUNC-067).
func TestRootContainer_ProcessDirectoryAsUser(t *testing.T) {
	t.Parallel()
	ct := startRootContainer(t, map[string]string{
		"srv/locked/|0700": "",
		"kahi.toml": `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.locked]
command = "/orphaner id"
user = "33"
directory = "/srv/locked"
startsecs = 0
startretries = 0
`}, nil, nil)
	deadline := time.Now().Add(20 * time.Second)
	for {
		logs := ct.logs()
		if strings.Contains(logs, "spawn error: cannot access directory: /srv/locked: permission denied") ||
			strings.Contains(logs, "cannot access directory: /srv/locked: permission denied") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no directory spawn error for user 33:\n%s", logs)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
