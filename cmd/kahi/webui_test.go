package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
	"github.com/kahiteam/kahi/internal/process"
	"github.com/kahiteam/kahi/internal/web"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Interface stubs: only the methods these tests reach are implemented.
type stubConfig struct{ api.ConfigManager }
type stubDaemon struct{ api.DaemonInfo }

func (stubDaemon) IsShuttingDown() bool { return false }

type listPM struct {
	api.ProcessManager
	infos []api.ProcessInfo
}

func (l listPM) List() []api.ProcessInfo { return l.infos }

// webManager is a real process.Manager over a MockSpawner with one stopped
// program, "web".
func webManager(t *testing.T) *process.Manager {
	t.Helper()
	logger := quietLogger()
	m := process.NewManager(&process.MockSpawner{}, events.NewBus(logger), logger)
	autostart := false
	m.LoadConfig(&config.Config{Programs: map[string]config.ProgramConfig{"web": {
		Command: "/bin/true", Numprocs: 1, Autostart: &autostart, Autorestart: "false",
		Stopsignal: "TERM", Stopwaitsecs: 1, Exitcodes: []int{0},
	}}})
	if infos := m.List(); len(infos) != 1 || infos[0].Name != "web" {
		t.Fatalf("manager processes = %+v, want one named web", infos)
	}
	return m
}

// startUIServer mounts ui on a credentialed api.Server over TCP and returns
// its base URL.
func startUIServer(t *testing.T, pm api.ProcessManager, ui http.Handler) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	logger := quietLogger()
	srv := api.NewServer(api.Config{Username: "admin", Password: string(hash), UI: ui},
		pm, &process.Manager{}, stubConfig{}, stubDaemon{}, events.NewBus(logger), logger)
	if err := srv.StartTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return "http://" + srv.TCPAddr()
}

var testClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func fetch(t *testing.T, url string, auth bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestUIMountRealHandlerAuth(t *testing.T) {
	m := webManager(t)
	ui := buildUIHandler(config.WebServerConfig{Enabled: true}, m, web.NewHandler, quietLogger())
	base := startUIServer(t, m, ui)
	for _, path := range []string{
		"/ui/", "/ui/log/web/stdout", "/ui/static/app.js", "/ui/static/log.js", "/ui/static/style.css",
	} {
		if resp, _ := fetch(t, base+path, false); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without credentials: status %d, want 401", path, resp.StatusCode)
		}
		if resp, _ := fetch(t, base+path, true); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s with credentials: status %d, want 200", path, resp.StatusCode)
		}
	}
	if _, body := fetch(t, base+"/ui/", true); !strings.Contains(body, `data-name="web"`) {
		t.Error("the status page does not list the manager's process")
	}
}

func TestUIUnavailable(t *testing.T) {
	var logs bytes.Buffer
	failing := func(web.ProcessLister, web.Config, *slog.Logger) (*web.Handler, error) {
		return nil, errors.New("templates broken")
	}
	ui := buildUIHandler(config.WebServerConfig{Enabled: true}, listPM{}, failing,
		slog.New(slog.NewTextHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "web UI unavailable") || !strings.Contains(logs.String(), "templates broken") {
		t.Errorf("the build failure was not logged: %s", logs.String())
	}

	m := webManager(t)
	base := startUIServer(t, m, ui)
	for _, path := range []string{"/ui/", "/ui/static/app.js"} {
		resp, body := fetch(t, base+path, true)
		if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "web UI unavailable") {
			t.Errorf("GET %s: status %d, body %q; want 503 web UI unavailable", path, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Errorf("GET %s: Content-Type %q", path, ct)
		}
		if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: the 503 lacks the UI security headers: %v", path, resp.Header)
		}
	}
	if resp, _ := fetch(t, base+"/ui/", false); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the 503 fallback is reachable without credentials: status %d", resp.StatusCode)
	}
	if resp, _ := fetch(t, base+"/api/v1/processes", true); resp.StatusCode != http.StatusOK {
		t.Errorf("the API is affected by the UI failure: status %d", resp.StatusCode)
	}
}

func TestBuildUIHandlerDisabled(t *testing.T) {
	h := buildUIHandler(config.WebServerConfig{Enabled: false}, listPM{}, web.NewHandler, quietLogger())
	if h != nil {
		t.Fatalf("buildUIHandler returned %#v for a disabled UI, want an untyped nil", h)
	}
	m := webManager(t)
	base := startUIServer(t, m, h)
	if resp, _ := fetch(t, base+"/ui/", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /ui/ with the UI disabled: status %d, want 404", resp.StatusCode)
	}
}

func TestUIListerAdapter(t *testing.T) {
	states := []string{"STOPPED", "STARTING", "RUNNING", "BACKOFF", "STOPPING", "EXITED", "FATAL"}
	infos := []api.ProcessInfo{{
		Name: "web", Group: "grp", State: "RUNNING", PID: 42, Uptime: 3720,
		Description: "Web server", ExitStatus: 3,
	}}
	for _, s := range states {
		infos = append(infos, api.ProcessInfo{Name: "p-" + s, State: s})
	}
	infos = append(infos, api.ProcessInfo{Name: "odd", State: "UNKNOWN(9)"})

	views := uiLister{pm: listPM{infos: infos}}.ListWeb()
	if len(views) != len(infos) {
		t.Fatalf("%d views for %d processes", len(views), len(infos))
	}
	want := web.ProcessView{
		Name: "web", Group: "grp", State: "RUNNING", StateLower: "running", PID: 42,
		Uptime: 3720, UptimeStr: "1h 2m", Description: "Web server", ExitStatus: 3,
	}
	if views[0] != want {
		t.Errorf("view = %+v, want %+v", views[0], want)
	}
	for i, s := range states {
		if got := views[i+1].StateLower; got != strings.ToLower(s) {
			t.Errorf("StateLower(%s) = %q", s, got)
		}
	}
	if got := views[len(views)-1].StateLower; got != "unknown" {
		t.Errorf("StateLower(UNKNOWN(9)) = %q, want unknown", got)
	}
}

func TestREADMEDocumentsWebUI(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	for _, want := range []string{"[server.web]", "/ui/", "/api/v1/all/", "### Web UI"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README does not mention %q", want)
		}
	}
	if strings.Contains(readme, "planned; not yet served") {
		t.Error("README still says the Web UI is not served")
	}
	security := section(readme, "## Security")
	if !strings.Contains(security, "reverse proxy") || !strings.Contains(security, "Unix socket") {
		t.Error("the Security section does not warn about a reverse proxy in front of the Unix socket")
	}
	if !strings.Contains(section(readme, "### Web UI"), "strip_ansi") {
		t.Error("the Web UI section does not note the strip_ansi backlog/stream difference")
	}
}

// section returns the text from heading to the next heading of the same level.
func section(doc, heading string) string {
	start := strings.Index(doc, "\n"+heading+"\n")
	if start < 0 {
		return ""
	}
	rest := doc[start+len(heading)+2:]
	level := heading[:strings.Index(heading, " ")+1]
	if end := strings.Index(rest, "\n"+level); end >= 0 {
		return rest[:end]
	}
	return rest
}
