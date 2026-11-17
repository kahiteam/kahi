//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	ctlClient "github.com/kahiteam/kahi/internal/ctl"
)

// kahi ctl avail and update <group> (FUNC-085, ADR-051, #161).

func availProg(name, env string, extra string) string {
	return "[programs." + name + "]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\nenvironment = { V = \"" + env + "\" }\n" + extra + "\n"
}

func ctlRun(t *testing.T, socket string, args ...string) (string, string, int) {
	t.Helper()
	return runKahi(t, t.TempDir(), append([]string{"ctl", "-s", socket}, args...)...)
}

func pidOf(t *testing.T, socket, name string) int {
	t.Helper()
	info, err := getProcessInfo(ctlClient.NewUnixClient(socket), name)
	if err != nil {
		t.Fatal(err)
	}
	return info.PID
}

// Steps 1, 2 and 13: one line per program, sorted, single spaces; a program
// added to the file or removed at runtime shows avail; a table has no line.
func TestAvail_Listing(t *testing.T) {
	t.Parallel()
	body := availProg("web", "1", "") + availProg("worker", "1", "") + availProg("a", "1", "") + availProg("b", "1", "") + "[groups.g]\nprograms = [\"a\", \"b\"]\n\n"
	client, socket, _ := startDaemonOut(t, body)
	waitForState(t, client, "worker", "RUNNING", 5*time.Second)
	out, _, code := ctlRun(t, socket, "avail")
	if code != 0 || !strings.Contains(out, "web in use auto 999\nworker in use auto 999\n") || !strings.Contains(out, "a in use auto 999\nb in use auto 999\n") {
		t.Errorf("avail: %d %q", code, out)
	}
	if strings.Contains(out, "\ng ") || strings.HasPrefix(out, "g ") {
		t.Errorf("a line for table g: %q", out)
	}

	if _, stderr, code := ctlRun(t, socket, "remove", "worker", "--stop"); code != 0 {
		t.Fatalf("remove: %s", stderr)
	}
	rewriteConfig(t, socket, body+availProg("later", "1", "autostart = false\npriority = 500"))
	out, _, _ = ctlRun(t, socket, "avail")
	for _, want := range []string{"later avail manual 500\n", "worker avail auto 999\n", "web in use auto 999\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("avail lacks %q: %q", want, out)
		}
	}
}

// Steps 3, 4, 7 and 8: update one group; the others keep their old config.
func TestAvail_UpdateOneGroup(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, availProg("web", "1", "")+availProg("worker", "1", ""))
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "worker", "RUNNING", 5*time.Second)
	webPID, workerPID := pidOf(t, socket, "web"), pidOf(t, socket, "worker")
	changed := availProg("web", "2", "") + availProg("worker", "2", "") + availProg("x", "1", "")
	rewriteConfig(t, socket, changed)

	out, _, code := ctlRun(t, socket, "reread", "web")
	if code != 0 || !strings.Contains(out, "added=[] changed=[web] removed=[]") {
		t.Errorf("reread web: %d %q", code, out)
	}
	out, _, code = ctlRun(t, socket, "update", "web")
	if code != 0 || !strings.Contains(out, "updated: added=[] changed=[web] removed=[]") {
		t.Fatalf("update web: %d %q", code, out)
	}
	waitForState(t, client, "web", "RUNNING", 10*time.Second)
	if pidOf(t, socket, "web") == webPID || pidOf(t, socket, "worker") != workerPID {
		t.Error("update web must restart web only")
	}
	out, _, _ = ctlRun(t, socket, "reread")
	if !strings.Contains(out, "changed=[worker]") {
		t.Errorf("reread after update web: %q", out)
	}
	out, _, code = ctlRun(t, socket, "update", "all")
	if code != 0 || !strings.Contains(out, "updated: added=[x] changed=[worker] removed=[]") {
		t.Errorf("update all: %d %q", code, out)
	}
	out, _, _ = ctlRun(t, socket, "reread")
	if !strings.Contains(out, "added=[] changed=[] removed=[]") {
		t.Errorf("reread after update all: %q", out)
	}
}

// Steps 5, 6, 9 and 10: unknown names, table members and config errors
// change nothing; a removed group is applied when named.
func TestAvail_UpdateErrorsAndRemoval(t *testing.T) {
	t.Parallel()
	body := availProg("web", "1", "") + availProg("old", "1", "")
	client, socket, _ := startDaemonOut(t, body)
	waitForState(t, client, "old", "RUNNING", 5*time.Second)
	webPID := pidOf(t, socket, "web")

	_, stderr, code := ctlRun(t, socket, "update", "nosuch")
	if code != 1 || !strings.Contains(stderr, "no such group: nosuch") {
		t.Errorf("update nosuch: %d %q", code, stderr)
	}
	rewriteConfig(t, socket, availProg("web", "2", "")+"[groups.g]\nprograms = [\"web\"]\n")
	_, stderr, code = ctlRun(t, socket, "update", "web")
	if code != 1 || !strings.Contains(stderr, "web is in group g; use g") {
		t.Errorf("update table member: %d %q", code, stderr)
	}
	rewriteConfig(t, socket, availProg("web", "2", "")+"[programs.bad\n")
	_, stderr, code = ctlRun(t, socket, "update", "web")
	if code != 1 || !strings.Contains(stderr, "config error:") {
		t.Errorf("update with a broken file: %d %q", code, stderr)
	}
	if pidOf(t, socket, "web") != webPID {
		t.Error("a refused update changed web")
	}

	rewriteConfig(t, socket, availProg("web", "1", ""))
	out, _, code := ctlRun(t, socket, "update", "old")
	if code != 0 || !strings.Contains(out, "updated: added=[] changed=[] removed=[old]") {
		t.Fatalf("update old: %d %q", code, out)
	}
	if st, _, _ := ctlRun(t, socket, "status"); strings.Contains(st, "old") {
		t.Errorf("old still listed:\n%s", st)
	}
}

// Step 11: pools are listed like programs; API entries carry group.
func TestAvail_PoolsAndAPI(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, "[eventlisteners.l]\ncommand = \"/bin/cat\"\nevents = [\"PROCESS_STATE_EXITED\"]\nstartsecs = 0\nstopwaitsecs = 2\n\n"+availProg("a", "1", "")+"[groups.g]\nprograms = [\"a\"]\n")
	waitForState(t, client, "l", "RUNNING", 5*time.Second)
	out, _, _ := ctlRun(t, socket, "avail")
	if !strings.Contains(out, "l in use auto 999\n") {
		t.Errorf("avail: %q", out)
	}
	r := apiDo(t, socket, "GET", "/api/v1/config/avail", "")
	var entries []struct {
		Name  string `json:"name"`
		Group string `json:"group"`
	}
	if r.status != http.StatusOK || json.Unmarshal(r.body, &entries) != nil {
		t.Fatalf("API: %d %s", r.status, r.body)
	}
	groups := map[string]string{}
	for _, e := range entries {
		groups[e.Name] = e.Group
	}
	if groups["a"] != "g" || groups["l"] != "l" {
		t.Errorf("groups %v", groups)
	}
}

// Step 14: a partial update leaves webhooks alone; a full one applies them.
func TestAvail_PartialUpdateKeepsWebhooks(t *testing.T) {
	t.Parallel()
	oldHook, newHook := newHookServer(t), newHookServer(t)
	cfg := func(url, env string) string {
		return webhookTOML("t", url, "") + fatalProg("f", false) + availProg("web", env, "")
	}
	client, socket, _ := startDaemonOut(t, cfg(oldHook.URL, "1"))
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	rewriteConfig(t, socket, cfg(newHook.URL, "2"))
	if _, stderr, code := ctlRun(t, socket, "update", "web"); code != 0 {
		t.Fatalf("update web: %s", stderr)
	}
	_ = client.Start("f")
	oldHook.waitCount(t, 1, 10*time.Second)
	if _, stderr, code := ctlRun(t, socket, "update"); code != 0 {
		t.Fatalf("update: %s", stderr)
	}
	waitForState(t, client, "f", "FATAL", 10*time.Second)
	_ = client.Start("f")
	newHook.waitCount(t, 1, 10*time.Second)
	if n := newHook.count(); n != 1 {
		t.Errorf("new receiver got %d", n)
	}
}

// Step 12: avail as the drop user, served by the daemon from its config
// file. The file is root-owned; it is readable (0644) because a daemon that
// dropped to kahi re-reads it as kahi on reload and avail.
func TestRootContainer_AvailAsDropUser(t *testing.T) {
	t.Parallel()
	files := usersImage(`[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi/kahi.sock"

[programs.web]
command = "/orphaner id"
startsecs = 0
autorestart = false
`)
	c := startRootContainer(t, files, nil, []string{"-u", "kahi"})
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, err := c.execAs("1000", "/kahi", "ctl", "-s", "/run/kahi/kahi.sock", "avail")
		if err == nil && strings.Contains(out, "web in use auto 999") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("avail as kahi: %v: %s", err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
