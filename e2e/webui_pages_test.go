//go:build e2e

package e2e

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/kahiteam/kahi/internal/testutil"
)

// Web UI pages, assets and mounting (FUNC-078..080, FUNC-084, SEC-026) as the
// binary serves them.

// webDaemon is a daemon with the TCP listener and the Web UI, writing its log
// to a file the test can read.
type webDaemon struct {
	base, socket, log, config string
	client                    *ctl.Client
}

// startWeb starts a daemon with credentials admin/secret, the given
// [server.web] extra lines and programs.
func startWeb(t *testing.T, webExtra, programs string) webDaemon {
	t.Helper()
	dir := shortTempDir(t)
	d := webDaemon{
		base:   fmt.Sprintf("http://127.0.0.1:%d", testutil.FreeTCPPort(t)),
		socket: filepath.Join(dir, "kahi.sock"),
		log:    filepath.Join(dir, "kahi.log"),
		config: filepath.Join(dir, "kahi.toml"),
	}
	writeWebConfig(t, d, "[server.web]\nenabled = true\n"+webExtra, programs)
	cmd := startDaemonCmd(t, kahiBinary, d.config, dir)
	t.Cleanup(func() {
		_ = ctl.NewUnixClient(d.socket).Shutdown()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	waitForSocket(t, d.socket, 5*time.Second)
	d.client = ctl.NewUnixClient(d.socket)
	waitForHealth(t, d.client, 3*time.Second)
	waitForTCP(t, strings.TrimPrefix(d.base, "http://"), 10*time.Second)
	return d
}

var webHash string

func writeWebConfig(t *testing.T, d webDaemon, web, programs string) {
	t.Helper()
	if webHash == "" {
		webHash = hashPassword(t, "secret")
	}
	cfg := fmt.Sprintf(`[supervisor]
logfile = %q
log_level = "debug"
shutdown_timeout = 10

[server.unix]
file = %q

[server.http]
enabled = true
listen = %q
username = "admin"
%s = %q

%s
%s`, d.log, d.socket, strings.TrimPrefix(d.base, "http://"), "pass"+"word", webHash, web, programs)
	if err := os.WriteFile(d.config, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// get fetches path with credentials and returns the response and body.
func (d webDaemon) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	return webRequest(t, webClient, http.MethodGet, d.base+path, true)
}

// waitForLog polls the daemon log for want.
func (d webDaemon) waitForLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(d.log); strings.Contains(string(b), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(d.log)
	t.Fatalf("daemon log lacks %q:\n%s", want, b)
}

func TestWebUI_StatusPage(t *testing.T) {
	d := startWeb(t, "", `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
description = "Web server"

[programs.api]
command = "/bin/sleep 300"
startsecs = 60

[programs.zeta]
command = "/bin/sleep 300"
autostart = false
`)
	waitForState(t, d.client, "web", "RUNNING", 5*time.Second)
	time.Sleep(1200 * time.Millisecond) // uptime renders from 1s on
	info, _ := getProcessInfo(d.client, "web")

	resp, body := d.get(t, "/ui/")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /ui/: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		`data-name="web"`, `<span class="state state-running">RUNNING</span>`, fmt.Sprint(info.PID), "0m", "Web server",
		`<span class="state state-starting">STARTING</span>`, `<span class="state state-stopped">STOPPED</span>`,
		`href="/ui/log/web/stdout"`, `href="/ui/log/web/stderr"`,
		`data-bulk="start"`, `data-bulk="stop"`, `data-bulk="restart"`,
		`<script src="/ui/static/app.js">`,
		`<link rel="icon" href="/ui/static/favicon.svg">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status page lacks %q", want)
		}
	}
	for _, action := range []string{"start", "stop", "restart"} {
		if !regexp.MustCompile(`<button[^>]*data-name="web"[^>]*data-action="` + action + `"|<button[^>]*data-action="` + action + `"[^>]*data-name="web"`).MatchString(body) {
			t.Errorf("no %s button for web", action)
		}
	}
	// Rows in name order, like ctl status.
	if a, w, z := strings.Index(body, `data-name="api"`), strings.Index(body, `data-name="web"`), strings.Index(body, `data-name="zeta"`); !(a < w && w < z) {
		t.Errorf("rows not in name order: api %d, web %d, zeta %d", a, w, z)
	}
	// The bulk controls sit in one toolbar; no forms, no inline script, no
	// old per-process bulk path, no Clear Log.
	if !regexp.MustCompile(`(?s)class="[^"]*\btoolbar\b[^"]*">\s*<button[^>]*data-bulk="start"[^>]*>[^<]*</button>\s*<button[^>]*data-bulk="stop"[^>]*>[^<]*</button>\s*<button[^>]*data-bulk="restart"`).MatchString(body) {
		t.Error("bulk buttons are not in the toolbar")
	}
	for _, bad := range []string{"<form", "/api/v1/processes/all/", "Clear Log", "data:"} {
		if strings.Contains(body, bad) {
			t.Errorf("status page contains %q", bad)
		}
	}
	if regexp.MustCompile(`<script(\s[^>]*)?>`).FindAllString(body, -1)[0] != `<script src="/ui/static/app.js">` ||
		len(regexp.MustCompile(`<script`).FindAllString(body, -1)) != 1 {
		t.Error("the page has a script other than app.js")
	}
	// Without JavaScript the table is there and every button is disabled.
	for _, b := range regexp.MustCompile(`<button[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(b, " disabled") {
			t.Errorf("button not disabled server-side: %s", b)
		}
	}
	if !regexp.MustCompile(`(?s)<noscript>.*Actions require JavaScript.*</noscript>`).MatchString(body) {
		t.Error("no <noscript> notice")
	}

	if resp, _ := d.get(t, "/ui/unknown"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /ui/unknown: %d, want 404", resp.StatusCode)
	}
}

// The shipped app.js and log.js carry the behaviour the pages rely on.
func TestWebUI_ScriptContents(t *testing.T) {
	d := startWeb(t, "", "")
	_, app := d.get(t, "/ui/static/app.js")
	for _, want := range []string{
		"Failed to refresh status: ", "Failed to ", " all: ", "Another bulk action is in progress",
		"Daemon is shutting down", "all: done", "/api/v1/all/", ".disabled = true", ".disabled = false",
		"state-unknown", "addEventListener('open'", "5000",
		"PROCESS_STATE_STOPPED", "PROCESS_STATE_STARTING", "PROCESS_STATE_RUNNING", "PROCESS_STATE_BACKOFF",
		"PROCESS_STATE_STOPPING", "PROCESS_STATE_EXITED", "PROCESS_STATE_FATAL",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if !regexp.MustCompile(`(?s)function formatUptime.*'d '.*'h '.*'m'`).MatchString(app) {
		t.Error("app.js has no formatUptime with d, h and m")
	}

	_, logjs := d.get(t, "/ui/static/log.js")
	for _, want := range []string{
		"sgr-fg-", "sgr-bold", ">= 30 && ", "<= 37", ">= 90 && ", "<= 97", "textContent",
		"slice(-100)", "=== 65536", "split('\\n')", ".pop()", "MAX_LINES = 5000", "removeChild(",
		"No output captured for this stream yet", "No such process: ", "Failed to load log: ", ".close()",
	} {
		if !strings.Contains(logjs, want) {
			t.Errorf("log.js lacks %q", want)
		}
	}
	if !strings.Contains(logjs, `\x1b`) && !strings.Contains(logjs, `\u001b`) {
		t.Error("log.js has no escape-sequence pattern")
	}
	if a, b, c := strings.Index(logjs, "new EventSource("), strings.Index(logjs, "addEventListener('open'"), strings.Index(logjs, "length=65536"); !(a >= 0 && a < b && b < c) {
		t.Errorf("log.js order: EventSource %d, open %d, backlog %d", a, b, c)
	}
	sc := bufio.NewScanner(strings.NewReader(logjs))
	for sc.Scan() {
		if line := sc.Text(); strings.Contains(line, "/api/v1/processes/") && !strings.Contains(line, "encodeURIComponent(") {
			t.Errorf("log.js builds a process URL without encodeURIComponent: %s", line)
		}
	}
}

func TestWebUI_LogViewerPages(t *testing.T) {
	d := startWeb(t, "", `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0

[programs.'a"b']
command = "/bin/sleep 300"
autostart = false
`)
	for _, stream := range []string{"stdout", "stderr"} {
		resp, body := d.get(t, "/ui/log/web/"+stream)
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") ||
			!regexp.MustCompile(`<main id="log"[^>]*data-name="web"[^>]*data-stream="`+stream+`"`).MatchString(body) ||
			!strings.Contains(body, `<script src="/ui/static/log.js">`) || len(regexp.MustCompile(`<script`).FindAllString(body, -1)) != 1 ||
			!strings.Contains(body, `<link rel="icon" href="/ui/static/favicon.svg">`) || strings.Contains(body, "data:") {
			t.Errorf("log page %s: %d\n%s", stream, resp.StatusCode, body)
		}
	}
	if resp, body := d.get(t, "/ui/log/web/stdin"); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "invalid stream") {
		t.Errorf("stdin: %d %q", resp.StatusCode, body)
	}
	if resp, body := d.get(t, "/ui/log/nosuch/stdout"); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "No such process: nosuch") {
		t.Errorf("nosuch: %d %q", resp.StatusCode, body)
	}
	resp, body := d.get(t, "/ui/log/a%22b/stdout")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `data-name="a&#34;b"`) || strings.Contains(body, `"a"b"`) {
		t.Errorf("a\"b: %d, name not escaped:\n%s", resp.StatusCode, body)
	}
}

func TestWebUI_Assets(t *testing.T) {
	d := startWeb(t, "", "")
	for path, ct := range map[string]string{
		"/ui/static/app.js":      "text/javascript; charset=utf-8",
		"/ui/static/log.js":      "text/javascript; charset=utf-8",
		"/ui/static/style.css":   "text/css; charset=utf-8",
		"/ui/static/favicon.svg": "image/svg+xml",
	} {
		resp, body := d.get(t, path)
		sum := sha256.Sum256([]byte(body))
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != ct ||
			resp.Header.Get("ETag") != etag || resp.Header.Get("Cache-Control") != "private, max-age=3600" {
			t.Errorf("%s: %d, Content-Type %q, ETag %q (want the content hash %s), Cache-Control %q", path, resp.StatusCode,
				resp.Header.Get("Content-Type"), resp.Header.Get("ETag"), etag, resp.Header.Get("Cache-Control"))
		}
		// A matching If-None-Match is a 304 with no body, another tag a 200.
		for tag, status := range map[string]int{etag: http.StatusNotModified, `"other"`: http.StatusOK} {
			req, _ := http.NewRequest(http.MethodGet, d.base+path, nil)
			req.SetBasicAuth("admin", "secret")
			req.Header.Set("If-None-Match", tag)
			r, err := webClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			n, _ := r.Body.Read(make([]byte, 1))
			_ = r.Body.Close()
			if r.StatusCode != status || (status == http.StatusNotModified && n != 0) {
				t.Errorf("%s If-None-Match %s: %d with %d body bytes, want %d", path, tag, r.StatusCode, n, status)
			}
		}
	}
	if resp, _ := d.get(t, "/ui/static/nope.js"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("nope.js: %d, want 404", resp.StatusCode)
	}
}

// static_dir overrides embedded assets file by file, only for the allowed
// types, never the templates, and never through a symlink out of the dir.
func TestWebUI_StaticOverlay(t *testing.T) {
	overlay := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.js")
	for name, content := range map[string]string{
		"style.css": "/* overlay marker */", "x.js": "// x", "x.html": "<p>x</p>",
		"noext": "plain", "index.html": "OVERLAY INDEX MARKER", "app.js": "AAAA",
	} {
		if err := os.WriteFile(filepath.Join(overlay, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(outside, []byte("// outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(overlay, "escape.js")); err != nil {
		t.Fatal(err)
	}
	d := startWeb(t, fmt.Sprintf("static_dir = %q\n", overlay), "")

	if _, body := d.get(t, "/ui/static/style.css"); body != "/* overlay marker */" {
		t.Errorf("style.css not from static_dir: %q", body)
	}
	if _, body := d.get(t, "/ui/static/log.js"); !strings.Contains(body, "MAX_LINES") {
		t.Error("log.js, absent from static_dir, is not the embedded one")
	}
	for path, status := range map[string]int{
		"/ui/static/x.js": http.StatusOK, "/ui/static/x.html": http.StatusNotFound,
		"/ui/static/noext": http.StatusNotFound, "/ui/static/escape.js": http.StatusNotFound,
	} {
		if resp, body := d.get(t, path); resp.StatusCode != status || strings.Contains(body, "outside") {
			t.Errorf("%s: %d %q, want %d", path, resp.StatusCode, body, status)
		}
	}
	if _, body := d.get(t, "/ui/"); strings.Contains(body, "OVERLAY INDEX MARKER") {
		t.Error("static_dir replaced the page template")
	}

	// The ETag follows the content, also when the file keeps its mtime.
	app := filepath.Join(overlay, "app.js")
	fi, _ := os.Stat(app)
	before, _ := d.get(t, "/ui/static/app.js")
	if err := os.WriteFile(app, []byte("BBBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(app, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, body := d.get(t, "/ui/static/app.js")
	if body != "BBBB" || after.Header.Get("ETag") == before.Header.Get("ETag") {
		t.Errorf("app.js after rewrite: %q, ETag %q before and after", body, after.Header.Get("ETag"))
	}
}

// A static_dir that is missing or not a directory is warned about and the
// embedded assets are served.
func TestWebUI_StaticDirFallback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"missing": filepath.Join(t.TempDir(), "nope"), "file": file} {
		t.Run(name, func(t *testing.T) {
			d := startWeb(t, fmt.Sprintf("static_dir = %q\n", dir), "")
			d.waitForLog(t, "static_dir not found, using embedded assets")
			if resp, body := d.get(t, "/ui/static/app.js"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "formatUptime") {
				t.Errorf("app.js: %d, not the embedded asset", resp.StatusCode)
			}
		})
	}
}

// Every /ui path and / need credentials over TCP; / redirects to /ui/ and
// /ui to /ui/.
func TestWebUI_MountAuthAndRedirects(t *testing.T) {
	d := startWeb(t, "", `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
`)
	for _, path := range []string{"/ui/", "/ui/a/b", "/ui/static/zzz", "/ui/static/app.js", "/ui/static/log.js", "/ui/static/style.css", "/ui/log/web/stdout", "/"} {
		resp, _ := webRequest(t, webClient, http.MethodGet, d.base+path, false)
		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic") {
			t.Errorf("%s without credentials: %d %q, want 401 Basic", path, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
	for _, path := range []string{"/ui/", "/ui/log/web/stdout", "/ui/static/app.js", "/ui/static/log.js", "/ui/static/style.css"} {
		if resp, _ := d.get(t, path); resp.StatusCode != http.StatusOK {
			t.Errorf("%s with credentials: %d, want 200", path, resp.StatusCode)
		}
	}
	if resp, _ := d.get(t, "/"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/" {
		t.Errorf("/: %d to %q, want 302 to /ui/", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ := webRequest(t, webClient, http.MethodGet, d.base+"/ui", false)
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "/ui/" {
		t.Errorf("/ui: %d to %q, want 307 to /ui/", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// A reload that changes [server.web] warns that the change needs a restart
// and leaves the UI as it was.
func TestWebUI_ReloadWarnsOnWebChange(t *testing.T) {
	d := startWeb(t, "", `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
`)
	writeWebConfig(t, d, "", `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
`)
	if _, err := d.client.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	d.waitForLog(t, "server.web changes take effect after restart")
	if resp, _ := d.get(t, "/ui/"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /ui/ after the reload: %d, want 200 until a restart", resp.StatusCode)
	}
}

// Shutdown ends open event streams instead of waiting for the clients.
func TestWebUI_ShutdownEndsStreams(t *testing.T) {
	d := startWeb(t, "", "")
	req, _ := http.NewRequest(http.MethodGet, d.base+"/api/v1/events/stream", nil)
	req.SetBasicAuth("admin", "secret")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	ended := make(chan struct{})
	go func() {
		buf := make([]byte, 512)
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				close(ended)
				return
			}
		}
	}()
	if err := d.client.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(8 * time.Second):
		t.Fatal("event stream still open 8s after shutdown")
	}
}

// Without [server.web] nothing is mounted: /ui/, its assets and / are 404.
// kahi init documents the table, commented out.
func TestWebUI_Disabled(t *testing.T) {
	d := startWebUIDaemon(t, false)
	for _, path := range []string{"/ui/", "/ui/static/app.js", "/"} {
		if resp, _ := webRequest(t, webClient, http.MethodGet, d.base+path, true); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s with the UI disabled: %d, want 404", path, resp.StatusCode)
		}
	}
	out, _, code := runKahi(t, t.TempDir(), "init", "--stdout")
	if code != 0 || !regexp.MustCompile(`(?m)^# \[server\.web\]\s`).MatchString(out) {
		t.Errorf("init --stdout lacks a commented [server.web] table")
	}
}
