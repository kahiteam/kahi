//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

const accessConfig = `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi/kahi.sock"

[programs.web]
command = "/orphaner cat /dev/stdin"
startsecs = 0
`

// After a drop the daemon reads its config as the drop user (FUNC-045,
// decided 2026-11-19): a root-only config starts (read as root) with a
// WARN, and reload and avail fail with both ways out while web runs on.
func TestRootContainer_ConfigUnreadableAfterDrop(t *testing.T) {
	t.Parallel()
	files := usersImage(accessConfig)
	delete(files, "kahi.toml")
	files["kahi.toml|0600"] = accessConfig
	c := startRootContainer(t, files, nil, []string{"-u", "kahi"})
	c.waitLogs("config not readable as kahi (uid 1000) after the privilege drop: cannot read config: /kahi.toml: open /kahi.toml: permission denied; " +
		"reload, update, reread and avail will fail until the daemon is restarted or the file is readable by kahi (uid 1000) " +
		"(for example chgrp kahi /kahi.toml; chmod 0640 /kahi.toml)")
	if s := c.waitStatus("/run/kahi/kahi.sock", "web", "RUNNING", 10*time.Second); !strings.Contains(s, "RUNNING") {
		t.Fatalf("web: %s", s)
	}

	hint := "permission denied; the daemon runs as kahi (uid 1000): restart it to apply config changes, or make the file readable by kahi (uid 1000)"
	for _, cmd := range []string{"reload", "avail"} {
		out, err := c.execAs("1000", "/kahi", "ctl", "-s", "/run/kahi/kahi.sock", cmd)
		if err == nil || !strings.Contains(out, "cannot read config: /kahi.toml") || !strings.Contains(out, hint) {
			t.Errorf("ctl %s: %v: %s", cmd, err, out)
		}
	}
	if s := c.status("/run/kahi/kahi.sock", "web"); !strings.Contains(s, "RUNNING") {
		t.Errorf("web after the failed reload: %s", s)
	}
}

// An include directory the drop user cannot list reads as empty (glob
// skips it): the WARN names what a reload would remove.
func TestRootContainer_IncludeDirUnreadableAfterDrop(t *testing.T) {
	t.Parallel()
	files := usersImage("include = [\"conf.d/*.toml\"]\n\n" + accessConfig)
	files["conf.d/|0700"] = ""
	files["conf.d/api.toml"] = "[programs.api]\ncommand = \"/orphaner cat /dev/stdin\"\nstartsecs = 0\n"
	c := startRootContainer(t, files, nil, []string{"-u", "kahi"})
	c.waitLogs("config reads differently as kahi (uid 1000) after the privilege drop (missing: api); an include directory is probably not readable by kahi (uid 1000)")
	if s := c.waitStatus("/run/kahi/kahi.sock", "api", "RUNNING", 10*time.Second); !strings.Contains(s, "RUNNING") {
		t.Errorf("api: %s", s)
	}
}
