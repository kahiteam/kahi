package web

import (
	"bytes"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

type stubLister []ProcessView

func (s stubLister) ListWeb() []ProcessView { return s }

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// view returns a RUNNING process with the derived fields already set; their
// computation belongs to the daemon's adapter, not to this package.
func view(name string) ProcessView {
	return ProcessView{
		Name: name, Group: name, State: "RUNNING", StateLower: "running",
		PID: 42, Uptime: 3720, UptimeStr: "1h 2m", Description: "Web server",
	}
}

func newTestHandler(t *testing.T, procs ...ProcessView) *Handler {
	t.Helper()
	h, err := NewHandler(stubLister(procs), Config{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(h http.Handler, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func mustGet(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	w := get(h, path)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %s", path, w.Code, w.Body)
	}
	return w.Body.String()
}

// embedded returns an embedded file such as "static/app.js".
func embedded(t *testing.T, name string) string {
	t.Helper()
	var src fs.FS = staticFS
	if strings.HasPrefix(name, "templates/") {
		src = templateFS
	}
	data, err := fs.ReadFile(src, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireContains(t *testing.T, label, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%s does not contain %q", label, sub)
		}
	}
}

var scriptTag = regexp.MustCompile(`<script[^>]*>`)

func requireNoInlineScript(t *testing.T, label, body string) {
	t.Helper()
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !strings.Contains(tag, "src=") {
			t.Errorf("%s has a <script> element without src: %s", label, tag)
		}
	}
}

// --- Status page (FUNC-078) ---

func TestStatusPage(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	requireContains(t, "status page", body,
		`<td class="col-name" data-label="Name">web</td>`,
		`<td class="col-group" data-label="Group">web</td>`,
		`class="state state-running"`, ">42<", "1h 2m", "Web server")
}

func TestStatusPageContentType(t *testing.T) {
	w := get(newTestHandler(t, view("web")), "/ui/")
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestStatusPageButtons(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	for _, action := range []string{"start", "stop", "restart"} {
		requireContains(t, "status page", body, `data-name="web" data-action="`+action+`"`)
	}
	requireContains(t, "status page", body, `href="/ui/log/web/stdout"`, `href="/ui/log/web/stderr"`)
	if strings.Contains(body, "Clear Log") {
		t.Error("the status page still offers Clear Log")
	}
}

func TestStatusPageAllControls(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	start := strings.Index(body, `class="header-actions toolbar"`)
	if start < 0 {
		t.Fatal("no toolbar element")
	}
	toolbar := body[start : start+strings.Index(body[start:], "</div>")]
	for _, action := range []string{"start", "stop", "restart"} {
		requireContains(t, "toolbar", toolbar, `<button type="button"`, `data-bulk="`+action+`"`)
	}
	if strings.Contains(body, "<form") {
		t.Error("the status page has a <form> element")
	}
	if strings.Contains(body, "/api/v1/processes/all/") {
		t.Error("the status page still posts to /api/v1/processes/all/")
	}
}

func TestStatusPageNoJS(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	requireContains(t, "status page", body, `<tbody id="process-list">`, `<td class="col-name" data-label="Name">web</td>`)
	for _, tag := range regexp.MustCompile(`<button[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(tag, " disabled") {
			t.Errorf("button is not rendered disabled: %s", tag)
		}
	}
	noscript := regexp.MustCompile(`(?s)<noscript>.*</noscript>`).FindString(body)
	requireContains(t, "noscript", noscript, "Actions require JavaScript")
}

func TestStatusPageScriptReference(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	requireContains(t, "status page", body, `<script src="/ui/static/app.js"></script>`)
	requireNoInlineScript(t, "status page", body)
}

func TestStatusPageNonRootPath(t *testing.T) {
	if w := get(newTestHandler(t, view("web")), "/ui/unknown"); w.Code != http.StatusNotFound {
		t.Errorf("GET /ui/unknown: status %d, want 404", w.Code)
	}
}

func TestStatusPageListerOrder(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("zeta"), view("alpha"), view("mid")), "/ui/")
	var got []string
	for _, m := range regexp.MustCompile(`<tr data-name="([^"]+)">`).FindAllStringSubmatch(body, -1) {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != "zeta,alpha,mid" {
		t.Errorf("row order %v, want the lister's order [zeta alpha mid]", got)
	}
}

func TestStatusPageUnknownState(t *testing.T) {
	p := view("odd")
	p.State, p.StateLower = "UNKNOWN(9)", "unknown"
	body := mustGet(t, newTestHandler(t, p), "/ui/")
	requireContains(t, "status page", body, `<span class="state state-unknown">UNKNOWN(9)</span>`)
}

func TestStatusPageAssetMessages(t *testing.T) {
	js := embedded(t, "static/app.js")
	requireContains(t, "app.js", js,
		"'Failed to refresh status: '", "'Failed to '", "' all: '",
		"'Another bulk action is in progress'", "'Daemon is shutting down'", "' all: done'",
		"'/api/v1/all/'", ".disabled = true", ".disabled = false", "'state-unknown'",
		"addEventListener('open'", "function formatUptime(", "'d '", "'h '", "'m'", "5000")
	for _, ev := range []string{"STOPPED", "STARTING", "RUNNING", "BACKOFF", "STOPPING", "EXITED", "FATAL"} {
		requireContains(t, "app.js", js, "'PROCESS_STATE_"+ev+"'")
	}
	requireContains(t, "app.js", js, "source.addEventListener(STATE_EVENTS[i]")
}

// --- Log viewer (FUNC-079) ---

func TestLogViewerPage(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/log/web/stdout")
	requireContains(t, "log page", body,
		`<main id="log" data-name="web" data-stream="stdout">`,
		`<script src="/ui/static/log.js"></script>`)
	requireNoInlineScript(t, "log page", body)
}

func TestLogViewerStderr(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/log/web/stderr")
	requireContains(t, "log page", body, `data-stream="stderr"`)
}

func TestLogViewerInvalidStream(t *testing.T) {
	w := get(newTestHandler(t, view("web")), "/ui/log/web/stdin")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid stream") {
		t.Errorf("status %d, body %q; want 400 invalid stream", w.Code, w.Body)
	}
}

func TestLogViewerUnknownProcess(t *testing.T) {
	w := get(newTestHandler(t, view("web")), "/ui/log/nosuch/stdout")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "No such process: nosuch") {
		t.Errorf("status %d, body %q; want 404 No such process", w.Code, w.Body)
	}
}

func TestLogViewerNameEscaping(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view(`a"b`)), "/ui/log/a%22b/stdout")
	requireContains(t, "log page", body, `data-name="a&#34;b"`)
	if strings.Contains(body, `="a"b"`) {
		t.Error(`the raw name a"b appears inside an attribute`)
	}
}

func TestLogViewerAssetSGR(t *testing.T) {
	requireContains(t, "log.js", embedded(t, "static/log.js"),
		"'sgr-fg-'", "'sgr-bold'", ">= 30 && ", "<= 37", ">= 90 && ", "<= 97",
		`/\x1b`, "textContent")
}

func TestLogViewerAssetOrder(t *testing.T) {
	js := embedded(t, "static/log.js")
	es := strings.Index(js, "new EventSource(")
	open := strings.Index(js, "addEventListener('open'")
	read := strings.Index(js, "length=65536")
	if es < 0 || open < 0 || read < 0 || es >= open || open >= read {
		t.Errorf("want new EventSource( (%d) < addEventListener('open' (%d) < length=65536 (%d)", es, open, read)
	}
	requireContains(t, "log.js", js, "slice(-100)", "=== 65536", `split('\n')`, ".pop()")
}

func TestLogViewerAssetMessages(t *testing.T) {
	requireContains(t, "log.js", embedded(t, "static/log.js"),
		"'No output captured for this stream yet'", "'No such process: '",
		"'Failed to load log: ", "source.close()")
}

func TestLogViewerAssetEncoding(t *testing.T) {
	for i, line := range strings.Split(embedded(t, "static/log.js"), "\n") {
		if strings.Contains(line, "/api/v1/processes/") && !strings.Contains(line, "encodeURIComponent(") {
			t.Errorf("log.js:%d builds a process URL without encodeURIComponent: %s", i+1, line)
		}
	}
}

func TestFormatUptime(t *testing.T) {
	for _, tt := range []struct {
		seconds int64
		want    string
	}{{0, "0m"}, {300, "5m"}, {3660, "1h 1m"}, {3720, "1h 2m"}, {90060, "1d 1h 1m"}} {
		if got := FormatUptime(tt.seconds); got != tt.want {
			t.Errorf("FormatUptime(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestTemplateRenderFailureIsNotPartial(t *testing.T) {
	// render buffers the template, so a failure cannot leave half a page.
	h := newTestHandler(t, view("web"))
	w := httptest.NewRecorder()
	h.render(w, "missing.html", nil)
	if w.Code != http.StatusInternalServerError || bytes.Contains(w.Body.Bytes(), []byte("<html")) {
		t.Errorf("status %d, body %q; want a plain 500", w.Code, w.Body)
	}
}
