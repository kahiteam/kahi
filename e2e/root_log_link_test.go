//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// A symlink another user planted in a log directory is not followed by the
// root daemon (SEC-019, decided 2026-11-19): app (uid 1001) puts a relative
// link /logs/app -> ../etc in place, and web's log open through it fails
// instead of creating /etc/web.log as root (v0.3.0 and earlier followed
// relative links).
func TestRootContainer_LogLinkFromOtherUserRefused(t *testing.T) {
	t.Parallel()
	files := usersImage(`[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.planter]
command = "/orphaner symlink ../etc /logs/app"
user = "app"
priority = 1
startsecs = 0
autorestart = false

[programs.web]
command = "/orphaner cat /dev/stdin"
autostart = false
startsecs = 0
startretries = 0
stdout_logfile = "/logs/app/web.log"
`)
	files["logs/|0777"] = "" // not sticky, so Linux protected_symlinks does not decide it
	c := startRootContainer(t, files, nil, nil)
	c.waitStatus("/run/kahi.sock", "planter", "EXITED", 10*time.Second)
	if st := c.stat("/logs/app"); !strings.HasPrefix(st, "1001 ") {
		t.Fatalf("/logs/app is not app's link: %q", st)
	}
	_, _ = c.execAs("0", "/kahi", "ctl", "-s", "/run/kahi.sock", "start", "web")
	s := c.waitStatus("/run/kahi.sock", "web", "FATAL", 10*time.Second)
	if !strings.Contains(s, "symlink app is owned by uid 1001") {
		t.Errorf("web status %q, want the refused link named", s)
	}
	if c.exists("/etc/web.log") {
		t.Error("the root daemon created /etc/web.log through app's link")
	}
}
