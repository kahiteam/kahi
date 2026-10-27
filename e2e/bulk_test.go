//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Bulk actions (FUNC-083): POST /api/v1/all/{start,stop,restart} against the
// real binary, over the Unix socket unless a test needs TCP.

type bulkResult struct {
	Name, Status, Error string
}

// postBulk sends POST /api/v1/all/<action> and decodes the 200 results.
func postBulk(t *testing.T, socket, action string) []bulkResult {
	t.Helper()
	var body struct{ Results []bulkResult }
	apiDo(t, socket, "POST", "/api/v1/all/"+action, "").jsonBody(t, "all/"+action, http.StatusOK, &body)
	if body.Results == nil {
		t.Fatalf("all/%s: results missing", action)
	}
	return body.Results
}

func bulkNames(rs []bulkResult) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return strings.Join(names, ",")
}

func bulkByName(rs []bulkResult) map[string]bulkResult {
	m := map[string]bulkResult{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

// Start goes up by priority, stop goes down and waits for each process to
// exit before signalling the next; restart reports one entry per process in
// start order. autostart = false programs are included.
func TestBulk_Order(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events")
	var cfg strings.Builder
	for _, p := range []struct {
		name     string
		priority int
	}{{"p10", 10}, {"a", 20}, {"b", 20}, {"p30", 30}} {
		script := writeScript(t, dir, p.name+".sh", fmt.Sprintf(
			"trap 'echo term:%[1]s >> %[2]s; sleep 0.3; echo exit:%[1]s >> %[2]s; exit 0' TERM\necho start:%[1]s >> %[2]s\nwhile true; do sleep 0.1; done",
			p.name, events))
		fmt.Fprintf(&cfg, "[programs.%s]\ncommand = %q\nautostart = false\nstartsecs = 0\npriority = %d\nstopwaitsecs = 5\n\n", p.name, script, p.priority)
	}
	_, socket := startDaemon(t, cfg.String())

	rs := postBulk(t, socket, "start")
	if bulkNames(rs) != "p10,a,b,p30" {
		t.Fatalf("start results order %s, want p10,a,b,p30", bulkNames(rs))
	}
	for _, r := range rs {
		if r.Status != "ok" || r.Error != "" {
			t.Errorf("start %+v, want ok", r)
		}
	}
	waitForLines(t, events, 4, 5*time.Second)
	_ = os.Remove(events)

	rs = postBulk(t, socket, "stop")
	if bulkNames(rs) != "p30,b,a,p10" {
		t.Fatalf("stop results order %s, want p30,b,a,p10", bulkNames(rs))
	}
	for _, r := range rs {
		if r.Status != "ok" {
			t.Errorf("stop %+v, want ok", r)
		}
	}
	got, _ := os.ReadFile(events)
	want := "term:p30\nexit:p30\nterm:b\nexit:b\nterm:a\nexit:a\nterm:p10\nexit:p10\n"
	if string(got) != want {
		t.Fatalf("stop sequence:\n%s\nwant each process to exit before the next is signalled:\n%s", got, want)
	}

	rs = postBulk(t, socket, "restart")
	if bulkNames(rs) != "p10,a,b,p30" {
		t.Fatalf("restart results order %s, want p10,a,b,p30", bulkNames(rs))
	}
	for _, r := range rs {
		if r.Status != "ok" {
			t.Errorf("restart %+v, want ok", r)
		}
	}
}

// all/start skips what is already up, refuses a stopping process, starts
// stopped, exited and fatal ones, and goes on after an error.
func TestBulk_StartPerState(t *testing.T) {
	dir := t.TempDir()
	stubborn := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+filepath.Join(dir, "ready")+"\nexec sleep 300")
	exits := writeScript(t, dir, "exits.sh", "exit 0")
	runs := filepath.Join(dir, "runs")
	failing := writeScript(t, dir, "failing.sh", "echo run >> "+runs+"\nexit 1")
	flag := filepath.Join(dir, "flag")
	fatalOnce := writeScript(t, dir, "fatal.sh", "if [ -f "+flag+" ]; then exec sleep 300; fi\ntouch "+flag+"\nexit 1")
	client, socket := startDaemon(t, `
[programs.running]
command = "/bin/sleep 300"
startsecs = 0

[programs.starting]
command = "/bin/sleep 300"
startsecs = 60

[programs.backoff]
command = "`+failing+`"
startsecs = 1
startretries = 100

[programs.stopping]
command = "`+stubborn+`"
startsecs = 0
stopwaitsecs = 10

[programs.stopped]
command = "/bin/sleep 300"
autostart = false

[programs.exited]
command = "`+exits+`"
startsecs = 0
autorestart = "false"

[programs.fatal]
command = "`+fatalOnce+`"
startsecs = 1
startretries = 0

[programs.broken]
command = "`+filepath.Join(dir, "missing")+`"
autostart = false
`)
	waitForState(t, client, "running", "RUNNING", 5*time.Second)
	waitForState(t, client, "exited", "EXITED", 5*time.Second)
	waitForState(t, client, "fatal", "FATAL", 5*time.Second)
	waitForState(t, client, "stopping", "RUNNING", 5*time.Second)
	waitForMarker(t, filepath.Join(dir, "ready"), "ready", 5*time.Second)
	if err := client.Stop("stopping"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "stopping", "STOPPING", 2*time.Second)
	// From the third failure the backoff is 4s, a wide enough window.
	waitForLines(t, runs, 3, 10*time.Second)
	waitForState(t, client, "backoff", "BACKOFF", 5*time.Second)
	spawns := countLines(runs)

	rs := bulkByName(postBulk(t, socket, "start"))
	for name, want := range map[string]string{
		"running": "skipped", "starting": "skipped", "backoff": "skipped",
		"stopped": "ok", "exited": "ok", "fatal": "ok",
	} {
		if rs[name].Status != want || rs[name].Error != "" {
			t.Errorf("%s: %+v, want %s", name, rs[name], want)
		}
	}
	if r := rs["stopping"]; r.Status != "error" || r.Error != "process is stopping" {
		t.Errorf("stopping: %+v, want error \"process is stopping\"", r)
	}
	if r := rs["broken"]; r.Status != "error" || !strings.Contains(r.Error, "spawn") {
		t.Errorf("broken: %+v, want a spawn error", r)
	}
	if got := countLines(runs); got != spawns {
		t.Errorf("backoff spawned %d times during the bulk start, want 0", got-spawns)
	}
	waitForState(t, client, "fatal", "RUNNING", 5*time.Second)
	waitForState(t, client, "stopped", "RUNNING", 5*time.Second)
}

// all/stop skips what is down, stops BACKOFF and STARTING processes, waits for
// a process already STOPPING without signalling it again, and SIGKILLs one
// that ignores TERM within stopwaitsecs. slowstop has the highest priority, so
// the descending stop reaches it first, while it is still STOPPING.
func TestBulk_StopPerState(t *testing.T) {
	dir := t.TempDir()
	terms := filepath.Join(dir, "terms")
	slowStop := writeScript(t, dir, "slow.sh",
		"trap 'echo term >> "+terms+"; sleep 3; exit 0' TERM\necho ready > "+filepath.Join(dir, "ready")+"\nwhile true; do sleep 0.1; done")
	ignoresTerm := writeScript(t, dir, "ignores.sh", "trap '' TERM\necho ready > "+filepath.Join(dir, "ready2")+"\nexec sleep 300")
	runs := filepath.Join(dir, "runs")
	failing := writeScript(t, dir, "failing.sh", "echo run >> "+runs+"\nexit 1")
	exits := writeScript(t, dir, "exits.sh", "exit 0")
	client, socket := startDaemon(t, `
[programs.stopped]
priority = 500
command = "/bin/sleep 300"
autostart = false

[programs.exited]
priority = 500
command = "`+exits+`"
startsecs = 0
autorestart = "false"

[programs.fatal]
priority = 500
command = "`+filepath.Join(dir, "missing")+`"
startretries = 0

[programs.backoff]
priority = 500
command = "`+failing+`"
startsecs = 1
startretries = 100

[programs.starting]
priority = 500
command = "/bin/sleep 300"
startsecs = 60

[programs.slowstop]
command = "`+slowStop+`"
startsecs = 0
stopwaitsecs = 10
priority = 999

[programs.ignoresterm]
priority = 500
command = "`+ignoresTerm+`"
startsecs = 0
stopwaitsecs = 1
`)
	waitForState(t, client, "exited", "EXITED", 5*time.Second)
	waitForState(t, client, "fatal", "FATAL", 5*time.Second)
	waitForState(t, client, "slowstop", "RUNNING", 5*time.Second)
	waitForMarker(t, filepath.Join(dir, "ready"), "ready", 5*time.Second)
	waitForMarker(t, filepath.Join(dir, "ready2"), "ready", 5*time.Second)
	if err := client.Stop("slowstop"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "slowstop", "STOPPING", 2*time.Second)
	waitForLines(t, runs, 3, 10*time.Second)
	waitForState(t, client, "backoff", "BACKOFF", 5*time.Second)

	rs := bulkByName(postBulk(t, socket, "stop"))
	for name, want := range map[string]string{
		"stopped": "skipped", "exited": "skipped", "fatal": "skipped",
		"backoff": "ok", "starting": "ok", "slowstop": "ok", "ignoresterm": "ok",
	} {
		if rs[name].Status != want || rs[name].Error != "" {
			t.Errorf("%s: %+v, want %s", name, rs[name], want)
		}
	}
	for _, name := range []string{"backoff", "starting", "slowstop", "ignoresterm"} {
		if info, _ := getProcessInfo(client, name); info.State != "STOPPED" {
			t.Errorf("%s: state %s after all/stop, want STOPPED", name, info.State)
		}
	}
	if got := countLines(terms); got != 1 {
		t.Errorf("slowstop received TERM %d times, want 1 (not signalled again)", got)
	}
}

// One bulk action at a time: a second one gets 409 CONFLICT. A client that
// disconnects does not leave the bulk lock held.
func TestBulk_ConflictAndDisconnect(t *testing.T) {
	dir := t.TempDir()
	ignoresTerm := writeScript(t, dir, "ignores.sh", "trap '' TERM\necho ready > "+filepath.Join(dir, "ready")+"\nexec sleep 300")
	client, socket := startDaemon(t, `
[programs.slow]
command = "`+ignoresTerm+`"
startsecs = 0
stopwaitsecs = 3
`)
	waitForState(t, client, "slow", "RUNNING", 5*time.Second)
	waitForMarker(t, filepath.Join(dir, "ready"), "ready", 5*time.Second)

	// The first all/stop holds the lock for about 3s; this client gives up
	// after 500ms.
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://unix/api/v1/all/stop", nil)
	go func() {
		if resp, err := hc.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	waitForState(t, client, "slow", "STOPPING", 2*time.Second)

	apiDo(t, socket, "POST", "/api/v1/all/start", "").
		wantError(t, "second bulk call", http.StatusConflict, "bulk action in progress", "CONFLICT")

	// After the abandoned stop completes, the lock is free again.
	waitForState(t, client, "slow", "STOPPED", 10*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := apiDo(t, socket, "POST", "/api/v1/all/start", "")
		if r.status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("all/start after the abandoned stop: %d %s", r.status, r.body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitForState(t, client, "slow", "RUNNING", 5*time.Second)
}

// During shutdown a bulk call is 409 SHUTTING_DOWN, also while another bulk
// call holds the lock.
func TestBulk_ShuttingDown(t *testing.T) {
	dir := t.TempDir()
	ignoresTerm := writeScript(t, dir, "ignores.sh", "trap '' TERM\necho ready > "+filepath.Join(dir, "ready")+"\nexec sleep 300")
	_, _, done, client, socket := shutdownDaemon(t, 10, `
[programs.worker]
command = "`+ignoresTerm+`"
autostart = true
startsecs = 0
stopwaitsecs = 3
`)
	waitForMarker(t, filepath.Join(dir, "ready"), "ready", 5*time.Second)

	// A bulk stop holds the lock while the worker ignores TERM.
	go func() { apiDo(t, socket, "POST", "/api/v1/all/stop", "") }()
	waitForState(t, client, "worker", "STOPPING", 2*time.Second)

	// The shutdown handler answers first and begins shutdown 100ms later, so
	// wait until /healthz reports it.
	apiDo(t, socket, "POST", "/api/v1/shutdown", "")
	deadline := time.Now().Add(5 * time.Second)
	for apiDo(t, socket, "GET", "/healthz", "").status != http.StatusServiceUnavailable {
		if time.Now().After(deadline) {
			t.Fatal("/healthz never reported shutting down")
		}
		time.Sleep(20 * time.Millisecond)
	}
	apiDo(t, socket, "POST", "/api/v1/all/start", "").
		wantError(t, "bulk during shutdown", http.StatusConflict, "daemon is shutting down", "SHUTTING_DOWN")
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("daemon did not exit")
	}
}

// With no programs every bulk action returns an empty list; unknown actions
// are 404 and other methods 405.
func TestBulk_EmptyAndRoutes(t *testing.T) {
	_, socket := startDaemon(t, "")
	for _, action := range []string{"start", "stop", "restart"} {
		r := apiDo(t, socket, "POST", "/api/v1/all/"+action, "")
		if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"results":[]}` {
			t.Errorf("all/%s with no programs: %d %s, want 200 {\"results\":[]}", action, r.status, r.body)
		}
	}
	apiDo(t, socket, "POST", "/api/v1/all/foo", "").
		wantError(t, "all/foo", http.StatusNotFound, "not found", "NOT_FOUND")
	apiDo(t, socket, "GET", "/api/v1/all/stop", "").
		wantError(t, "GET all/stop", http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

// Over TCP a bulk call needs credentials, and a cross-site request is refused
// before it runs.
func TestBulk_AuthAndCrossOrigin(t *testing.T) {
	d := startWebUIDaemon(t, false)
	resp, body := webRequest(t, webClient, http.MethodPost, d.base+"/api/v1/all/stop", false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("all/stop without credentials: %d %s, want 401", resp.StatusCode, body)
	}

	req, _ := http.NewRequest(http.MethodPost, d.base+"/api/v1/all/stop", nil)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := webClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var e map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&e)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || e["code"] != "FORBIDDEN" {
		t.Fatalf("cross-site all/stop: %d %v, want 403 FORBIDDEN", resp.StatusCode, e)
	}

	if info, _ := getProcessInfo(d.client, "sleeper"); info.State != "RUNNING" {
		t.Fatalf("sleeper %s after refused bulk stops, want RUNNING", info.State)
	}
}

// A program named "all" is a process, not the bulk route.
func TestBulk_ProcessNamedAll(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.all]
command = "/bin/sleep 300"
autostart = false

[programs.other]
command = "/bin/sleep 300"
autostart = false
`)
	var act map[string]string
	apiDo(t, socket, "POST", "/api/v1/processes/all/start", "").jsonBody(t, "start all", http.StatusOK, &act)
	if act["status"] != "started" || act["name"] != "all" {
		t.Fatalf("start body %v", act)
	}
	waitForState(t, client, "all", "RUNNING", 5*time.Second)
	if info, _ := getProcessInfo(client, "other"); info.State != "STOPPED" {
		t.Fatalf("other %s, want STOPPED: the bulk start ran", info.State)
	}
}
