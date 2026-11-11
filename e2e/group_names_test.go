//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Homogeneous groups (FUNC-009): every program is a group of the same name
// holding its instances, and a [groups] table cannot take a program's name
// (#214).

// groupMembers returns the names of the processes the API lists in group.
func groupMembers(t *testing.T, socket, group string) []string {
	t.Helper()
	var list []processInfo
	apiDo(t, socket, "GET", "/api/v1/processes", "").jsonBody(t, "list", http.StatusOK, &list)
	var names []string
	for _, p := range list {
		if p.Group == group {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	return names
}

func TestGroupNames_ProgramIsGroup(t *testing.T) {
	t.Parallel()
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
numprocs = 2

[programs.solo]
command = "/bin/sleep 300"
startsecs = 0
`)
	waitForState(t, client, "web_0", "RUNNING", 5*time.Second)
	waitForState(t, client, "solo", "RUNNING", 5*time.Second)

	if got := groupMembers(t, socket, "web"); strings.Join(got, ",") != "web_0,web_1" {
		t.Errorf("group web = %v, want web_0 and web_1", got)
	}
	if got := groupMembers(t, socket, "solo"); strings.Join(got, ",") != "solo" {
		t.Errorf("group solo = %v, want the single process solo", got)
	}
}

// writeConfig writes a daemon config whose socket is in a short directory.
func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	cfg := filepath.Join(dir, "kahi.toml")
	full := "[server.unix]\nfile = \"" + filepath.Join(shortTempDir(t), "k.sock") + "\"\n\n" + body
	if err := os.WriteFile(cfg, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGroupNames_CollisionRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := writeConfig(t, dir, `
[programs.web]
command = "/bin/sleep 300"

[programs.api]
command = "/bin/sleep 300"

[groups.web]
programs = ["api"]
`)
	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
	if code != 1 || !strings.Contains(stderr, "groups.web: name collides with program web") {
		t.Errorf("exit %d, stderr %q; want 1 and the collision error", code, stderr)
	}
}

func TestGroupNames_IncludeRedefinesProgram(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "conf.d", "web.toml")
	if err := os.WriteFile(extra, []byte("[programs.web]\ncommand = \"/bin/sleep 600\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// include is a top-level key, so it precedes the tables.
	cfg := filepath.Join(dir, "kahi.toml")
	body := "include = [\"conf.d/*.toml\"]\n\n[server.unix]\nfile = \"" + filepath.Join(shortTempDir(t), "k.sock") + "\"\n\n" +
		"[programs.web]\ncommand = \"/bin/sleep 300\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
	want := `duplicate program name "web": defined in both main config and ` + extra
	if code != 1 || !strings.Contains(stderr, want) {
		t.Errorf("exit %d, stderr %q; want 1 and %q", code, stderr, want)
	}
}

// A [groups] table that names no program, an unknown program, or a program
// another table already lists is refused at load (FUNC-010).
func TestGroupNames_TableErrorsRefused(t *testing.T) {
	t.Parallel()
	progs := "[programs.web]\ncommand = \"/bin/sleep 300\"\n\n[programs.api]\ncommand = \"/bin/sleep 300\"\n\n"
	for _, tc := range []struct{ name, groups, want string }{
		{"unknown", "[groups.services]\nprograms = [\"nope\"]\n", `groups.services: references nonexistent program "nope"`},
		{"twice", "[groups.a]\nprograms = [\"web\"]\n\n[groups.b]\nprograms = [\"web\"]\n", `program "web" is in multiple groups: a and b`},
		{"empty", "[groups.services]\nprograms = []\n", "groups.services: empty group (no programs)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := writeConfig(t, dir, progs+tc.groups)
			_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
			if code != 1 || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit %d, stderr %q; want 1 and %q", code, stderr, tc.want)
			}
		})
	}
}
