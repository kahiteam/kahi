//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Group add and remove with supervisorctl semantics, [groups] tables that
// own their members, and the group events (FUNC-010, FUNC-011, FUNC-035,
// FUNC-053, ADR-034, #212).

// decodeJSON decodes an event's data.
func decodeJSON(t *testing.T, data string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), v); err != nil {
		t.Fatalf("event data %q: %v", data, err)
	}
}

// groupEvents streams PROCESS_GROUP_ADDED/REMOVED and returns a function
// that waits for the next one, as "TYPE group processes".
func groupEvents(t *testing.T, socket string) func() string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_GROUP_ADDED,PROCESS_GROUP_REMOVED")
	return func() string {
		t.Helper()
		select {
		case m := <-msgs:
			var d map[string]string
			decodeJSON(t, m.data, &d)
			return m.event + " " + d["group"] + " " + d["processes"]
		case <-time.After(10 * time.Second):
			t.Fatal("no group event within 10s")
			return ""
		}
	}
}

// statusGroups maps each listed process to its group.
func statusGroups(t *testing.T, socket string) map[string]string {
	t.Helper()
	var list []processInfo
	apiDo(t, socket, "GET", "/api/v1/processes", "").jsonBody(t, "list", http.StatusOK, &list)
	out := make(map[string]string, len(list))
	for _, p := range list {
		out[p.Name] = p.Group
	}
	return out
}

func wantCtl(t *testing.T, socket string, wantCode int, wantOut, wantErr string, args ...string) {
	t.Helper()
	out, errOut, code := runCtl(t, socket, args...)
	wantRun(t, strings.Join(args, " "), out, errOut, code, wantCode, wantOut, wantErr)
}

const twoSleepers = `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2

[programs.api]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
`

func TestGroupLifecycle_RemoveAdd(t *testing.T) {
	t.Parallel()
	client, socket := startDaemon(t, twoSleepers)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	nextEvent := groupEvents(t, socket)

	// A running process blocks the removal; nothing changes.
	wantCtl(t, socket, 1, "", "group web has running processes, stop first (or use kahi ctl remove --stop)\n", "remove", "web")
	if _, ok := statusGroups(t, socket)["web"]; !ok {
		t.Fatal("web left status after a refused remove")
	}
	apiDo(t, socket, "POST", "/api/v1/groups/web/remove", "").
		wantError(t, "remove running", http.StatusConflict, "group web has running processes, stop first", "CONFLICT")

	wantCtl(t, socket, 1, "", "group already active: web\n", "add", "web")
	apiDo(t, socket, "POST", "/api/v1/groups/web/add", "").
		wantError(t, "add active", http.StatusConflict, "group already active: web", "CONFLICT")
	wantCtl(t, socket, 1, "", "no such group: nosuch\n", "remove", "nosuch")
	wantCtl(t, socket, 1, "", "no such group: nosuch (not in the loaded config; run kahi ctl update, or kahi ctl add --reload)\n", "add", "nosuch")
	for _, cmd := range []string{"remove", "add"} {
		apiDo(t, socket, "POST", "/api/v1/groups/nosuch/"+cmd, "").
			wantError(t, cmd+" nosuch", http.StatusNotFound, "no such group: nosuch", "NOT_FOUND")
	}

	// Stopped, it is removed: gone from status before REMOVED arrives.
	wantCtl(t, socket, 0, "web: stopped\n", "", "stop", "web")
	wantCtl(t, socket, 0, "web: removed\n", "", "remove", "web")
	if ev := nextEvent(); ev != "PROCESS_GROUP_REMOVED web web" {
		t.Fatalf("event %q, want REMOVED web listing web", ev)
	}
	if _, ok := statusGroups(t, socket)["web"]; ok {
		t.Fatal("status still lists web after remove")
	}

	// The loaded config still defines it, so a reread offers it back.
	wantCtl(t, socket, 0, "would apply: added=[web] changed=[] removed=[]\n", "", "reread")

	wantCtl(t, socket, 0, "web: added\n", "", "add", "web")
	if ev := nextEvent(); ev != "PROCESS_GROUP_ADDED web web" {
		t.Fatalf("event %q, want ADDED web listing web", ev)
	}
	if g := statusGroups(t, socket)["web"]; g != "web" {
		t.Fatalf("web in group %q after add, want web", g)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
}

// Only STOPPED, EXITED and FATAL count as stopped; a removed group's pending
// backoff retry never spawns.
func TestGroupLifecycle_RemovableStates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stubborn := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nwhile true; do sleep 0.1; done")
	client, socket := startDaemon(t, `
[programs.ex]
command = "/bin/sh -c 'exit 0'"
startsecs = 0
autorestart = "false"

[programs.fat]
command = "/nonexistent/kahi-e2e"
startretries = 0

[programs.bo]
command = "/bin/sh -c 'exit 1'"
startsecs = 1
startretries = 3

[programs.stub]
command = "`+stubborn+`"
startsecs = 0
stopwaitsecs = 3
`)
	waitForState(t, client, "ex", "EXITED", 5*time.Second)
	waitForState(t, client, "fat", "FATAL", 5*time.Second)
	waitForState(t, client, "bo", "BACKOFF", 5*time.Second)
	waitForState(t, client, "stub", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)

	wantCtl(t, socket, 0, "ex: removed\n", "", "remove", "ex")
	wantCtl(t, socket, 0, "fat: removed\n", "", "remove", "fat")
	wantCtl(t, socket, 1, "", "group bo has running processes, stop first (or use kahi ctl remove --stop)\n", "remove", "bo")

	if err := client.Stop("stub"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "stub", "STOPPING", 2*time.Second)
	wantCtl(t, socket, 1, "", "group stub has running processes, stop first (or use kahi ctl remove --stop)\n", "remove", "stub")

	// Stopped during BACKOFF and removed: its pending retry never spawns.
	if err := client.Stop("bo"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "bo", "STOPPED", 5*time.Second)
	wantCtl(t, socket, 0, "bo: removed\n", "", "remove", "bo")
	time.Sleep(3 * time.Second)
	if _, ok := statusGroups(t, socket)["bo"]; ok {
		t.Fatal("bo is listed again 3s after its removal")
	}
	apiDo(t, socket, "GET", "/api/v1/processes/bo", "").
		wantError(t, "removed bo", http.StatusNotFound, "no such process: bo", "NOT_FOUND")
}

// add uses the loaded config; a reload or update picks up the file; a group
// the file no longer defines is forgotten; a changed program restarts
// without group events.
func TestGroupLifecycle_ConfigFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	webMarker, extraMarker := filepath.Join(dir, "web"), filepath.Join(dir, "extra")
	// One script for every rewrite, so only the version argument changes.
	script := writeScript(t, dir, "marker.sh", "echo \"$1\" > \"$2\"\nexec sleep 300")
	markerProgram := func(_ *testing.T, name, marker, version string) string {
		return fmt.Sprintf("[programs.%s]\ncommand = \"%s %s %s\"\nautostart = true\nstartsecs = 0\nstopwaitsecs = 2\n", name, script, version, marker)
	}
	_, socket := startDaemon(t, markerProgram(t, "web", webMarker, "v1"))
	waitForMarker(t, webMarker, "v1", 5*time.Second)
	nextEvent := groupEvents(t, socket)

	// reread previews without applying; update applies and adds.
	rewriteConfig(t, socket, markerProgram(t, "web", webMarker, "v1")+markerProgram(t, "extra", extraMarker, "v1"))
	wantCtl(t, socket, 0, "would apply: added=[extra] changed=[] removed=[]\n", "", "reread")
	if _, ok := statusGroups(t, socket)["extra"]; ok {
		t.Fatal("reread applied the change")
	}
	wantCtl(t, socket, 0, "updated: added=[extra] changed=[] removed=[]\n", "", "update")
	if ev := nextEvent(); ev != "PROCESS_GROUP_ADDED extra extra" {
		t.Fatalf("event %q, want ADDED extra", ev)
	}
	waitForMarker(t, extraMarker, "v1", 5*time.Second)

	// add uses the loaded config, not the edited file.
	wantCtl(t, socket, 0, "web: stopped\n", "", "stop", "web")
	wantCtl(t, socket, 0, "web: removed\n", "", "remove", "web")
	nextEvent()
	rewriteConfig(t, socket, markerProgram(t, "web", webMarker, "v2")+markerProgram(t, "extra", extraMarker, "v1"))
	_ = os.Remove(webMarker)
	wantCtl(t, socket, 0, "web: added\n", "", "add", "web")
	nextEvent()
	waitForMarker(t, webMarker, "v1", 5*time.Second)

	// Removed again, then update brings it back from the file, as added.
	wantCtl(t, socket, 0, "web: stopped\n", "", "stop", "web")
	wantCtl(t, socket, 0, "web: removed\n", "", "remove", "web")
	nextEvent()
	wantCtl(t, socket, 0, "updated: added=[web] changed=[] removed=[]\n", "", "update")
	if ev := nextEvent(); ev != "PROCESS_GROUP_ADDED web web" {
		t.Fatalf("event %q, want ADDED web", ev)
	}
	waitForMarker(t, webMarker, "v2", 5*time.Second)

	// A changed program restarts with no group event.
	rewriteConfig(t, socket, markerProgram(t, "web", webMarker, "v2")+markerProgram(t, "extra", extraMarker, "v2"))
	wantCtl(t, socket, 0, "updated: added=[] changed=[extra] removed=[]\n", "", "update")
	waitForMarker(t, extraMarker, "v2", 10*time.Second)

	// A removed group that the file drops is forgotten.
	wantCtl(t, socket, 0, "web: stopped\n", "", "stop", "web")
	wantCtl(t, socket, 0, "web: removed\n", "", "remove", "web")
	if ev := nextEvent(); ev != "PROCESS_GROUP_REMOVED web web" {
		t.Fatalf("event %q, want REMOVED web (and no event for the extra restart)", ev)
	}
	rewriteConfig(t, socket, markerProgram(t, "extra", extraMarker, "v2"))
	wantCtl(t, socket, 0, "updated: added=[] changed=[] removed=[]\n", "", "update")
	wantCtl(t, socket, 1, "", "no such group: web (not in the loaded config; run kahi ctl update, or kahi ctl add --reload)\n", "add", "web")

	// A running program the file drops is stopped and removed.
	rewriteConfig(t, socket, "")
	wantCtl(t, socket, 0, "updated: added=[] changed=[] removed=[extra]\n", "", "update")
	if ev := nextEvent(); ev != "PROCESS_GROUP_REMOVED extra extra" {
		t.Fatalf("event %q, want REMOVED extra", ev)
	}
	if len(statusGroups(t, socket)) != 0 {
		t.Fatalf("status %v, want empty", statusGroups(t, socket))
	}
}

const tableMembers = `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
numprocs = 2
priority = 200

[programs.api]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
priority = 100
`

// A [groups] table owns its members: they report it as their group, add and
// remove act on the table, web:* still reaches a member, and reloads split
// and merge tables (FUNC-010).
func TestGroupLifecycle_Tables(t *testing.T) {
	t.Parallel()
	client, socket := startDaemon(t, tableMembers)
	waitForState(t, client, "web_0", "RUNNING", 5*time.Second)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)
	nextEvent := groupEvents(t, socket)
	nextState := stateEvents(t, socket, "web_0")

	// A table added over active programs replaces their own groups.
	rewriteConfig(t, socket, tableMembers+"\n[groups.services]\nprograms = [\"web\", \"api\"]\n")
	wantCtl(t, socket, 0, "updated: added=[services] changed=[] removed=[api web]\n", "", "update")
	var evs []string
	for range 3 {
		evs = append(evs, nextEvent())
	}
	want := []string{"PROCESS_GROUP_REMOVED api api", "PROCESS_GROUP_REMOVED web web_0,web_1", "PROCESS_GROUP_ADDED services api,web_0,web_1"}
	if strings.Join(evs, "|") != strings.Join(want, "|") {
		t.Fatalf("events %v, want %v", evs, want)
	}
	if g := statusGroups(t, socket); g["web_0"] != "services" || g["web_1"] != "services" || g["api"] != "services" {
		t.Fatalf("groups %v, want every member in services", g)
	}
	for {
		if e := nextState(10 * time.Second); e.data["state"] == "RUNNING" {
			if e.data["group"] != "services" {
				t.Fatalf("RUNNING of web_0 carries group %q, want services", e.data["group"])
			}
			break
		}
	}

	// Members are addressed through the table, except for name:*.
	for _, cmd := range []string{"remove", "add"} {
		wantCtl(t, socket, 1, "", "web is in group services; use services\n", cmd, "web")
	}
	apiDo(t, socket, "POST", "/api/v1/groups/web/remove", "").
		wantError(t, "remove member", http.StatusNotFound, "web is in group services; use services", "NOT_FOUND")
	wantCtl(t, socket, 0, "services:*: stopped\n", "", "stop", "services:*")
	wantCtl(t, socket, 0, "web:*: started\n", "", "start", "web:*")
	waitForState(t, client, "web_1", "RUNNING", 5*time.Second)
	if info, _ := getProcessInfo(client, "api"); info.State != "STOPPED" {
		t.Fatalf("api %s after web:*, want STOPPED", info.State)
	}

	// The table is added and removed as a whole.
	wantCtl(t, socket, 0, "services:*: stopped\n", "", "stop", "services:*")
	wantCtl(t, socket, 0, "services: removed\n", "", "remove", "services")
	if ev := nextEvent(); ev != "PROCESS_GROUP_REMOVED services api,web_0,web_1" {
		t.Fatalf("event %q", ev)
	}
	wantCtl(t, socket, 0, "services: added\n", "", "add", "services")
	nextEvent()
	waitForState(t, client, "api", "RUNNING", 5*time.Second)

	// A member that leaves the table gets its own group; the rest stay.
	rewriteConfig(t, socket, tableMembers+"\n[groups.services]\nprograms = [\"web\"]\n")
	wantCtl(t, socket, 0, "updated: added=[api] changed=[services] removed=[]\n", "", "update")
	if g := statusGroups(t, socket); g["api"] != "api" || g["web_0"] != "services" {
		t.Fatalf("groups %v, want api on its own and web in services", g)
	}
	evs = nil
	for range 3 {
		evs = append(evs, nextEvent())
	}
	want = []string{"PROCESS_GROUP_REMOVED services api,web_0,web_1", "PROCESS_GROUP_ADDED services web_0,web_1", "PROCESS_GROUP_ADDED api api"}
	if strings.Join(evs, "|") != strings.Join(want, "|") {
		t.Fatalf("events %v, want %v", evs, want)
	}

	// Dropping the table while its programs stay splits it.
	rewriteConfig(t, socket, tableMembers)
	wantCtl(t, socket, 0, "updated: added=[web] changed=[] removed=[services]\n", "", "update")
	if g := statusGroups(t, socket); g["web_0"] != "web" || g["web_1"] != "web" {
		t.Fatalf("groups %v, want web_0 and web_1 in web", g)
	}
	waitForState(t, client, "web_0", "RUNNING", 5*time.Second)

	// Dropping a table and its programs removes the processes.
	rewriteConfig(t, socket, tableMembers+"\n[programs.solo]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n\n[groups.svc]\nprograms = [\"solo\"]\n")
	wantCtl(t, socket, 0, "updated: added=[svc] changed=[] removed=[]\n", "", "update")
	rewriteConfig(t, socket, tableMembers)
	wantCtl(t, socket, 0, "updated: added=[] changed=[] removed=[svc]\n", "", "update")
	if _, ok := statusGroups(t, socket)["solo"]; ok {
		t.Fatal("solo still listed after its table and program were dropped")
	}
}

// A table starts its members in priority order (api 100 before web 200).
func TestGroupLifecycle_TableStartOrder(t *testing.T) {
	t.Parallel()
	cfg := strings.ReplaceAll(tableMembers, "startsecs = 0\n", "startsecs = 0\nautostart = false\n")
	_, socket := startDaemon(t, cfg+"\n[groups.services]\nprograms = [\"web\", \"api\"]\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_STATE_STARTING")
	wantCtl(t, socket, 0, "services:*: started\n", "", "start", "services:*")
	var order []string
	for len(order) < 3 {
		select {
		case m := <-msgs:
			var d map[string]string
			decodeJSON(t, m.data, &d)
			order = append(order, d["name"])
		case <-time.After(10 * time.Second):
			t.Fatalf("STARTING events %v, want 3", order)
		}
	}
	if order[0] != "api" {
		t.Fatalf("start order %v, want api (priority 100) first", order)
	}
}

// Config errors and warnings on reread, update and reload (FUNC-035).
func TestConfigOps_ErrorsAndWarnings(t *testing.T) {
	t.Parallel()
	_, socket := startDaemon(t, twoSleepers)

	rewriteConfig(t, socket, "[programs.web\n")
	for _, cmd := range []string{"reread", "update"} {
		out, errOut, code := runCtl(t, socket, cmd)
		if code != 1 || out != "" || !strings.HasPrefix(errOut, "config error: ") {
			t.Errorf("%s with a broken file: exit %d, stdout %q, stderr %q; want exit 1 and 'config error: ...'", cmd, code, out, errOut)
		}
	}
	if len(statusGroups(t, socket)) != 2 {
		t.Fatal("a refused update changed the running programs")
	}

	rewriteConfig(t, socket, strings.Replace(twoSleepers, "stopwaitsecs = 2\n", "stopwaitsecs = 2\nbogus = 1\n", 1))
	for _, cmd := range []string{"reread", "update", "reload"} {
		_, errOut, code := runCtl(t, socket, cmd)
		if code != 0 || !strings.Contains(errOut, "warning: ") || !strings.Contains(errOut, "bogus") {
			t.Errorf("%s: exit %d, stderr %q; want a 'warning: ...' line naming bogus", cmd, code, errOut)
		}
	}
	for _, path := range []string{"/api/v1/config/reload?dry_run=true", "/api/v1/config/reload"} {
		var body struct {
			Warnings []string `json:"warnings"`
		}
		apiDo(t, socket, "POST", path, "").jsonBody(t, path, http.StatusOK, &body)
		if len(body.Warnings) == 0 || !strings.Contains(strings.Join(body.Warnings, " "), "bogus") {
			t.Errorf("%s warnings %v, want the bogus key", path, body.Warnings)
		}
	}
}

// remove --stop stops a running group and removes it; add --reload loads the
// file first, so it also adds a group the file defines newly (#212).
func TestGroupLifecycle_StopAndReloadFlags(t *testing.T) {
	t.Parallel()
	client, socket := startDaemon(t, twoSleepers)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	wantCtl(t, socket, 0, "web: removed\n", "", "remove", "--stop", "web")
	if _, ok := statusGroups(t, socket)["web"]; ok {
		t.Fatal("web still listed after remove --stop")
	}
	wantCtl(t, socket, 0, "web: added\n", "", "add", "--reload", "web")
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	rewriteConfig(t, socket, twoSleepers+"\n[programs.extra]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\n")
	wantCtl(t, socket, 0, "extra: added\n", "", "add", "--reload", "extra")
	waitForState(t, client, "extra", "RUNNING", 5*time.Second)
}
