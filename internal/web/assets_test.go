package web

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func overlayHandler(t *testing.T, dir string) *Handler {
	t.Helper()
	h, err := NewHandler(stubLister{view("web")}, Config{StaticDir: dir}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	h := newTestHandler(t, view("web"))
	for path, want := range map[string]string{
		"/ui/static/app.js":      "text/javascript; charset=utf-8",
		"/ui/static/log.js":      "text/javascript; charset=utf-8",
		"/ui/static/style.css":   "text/css; charset=utf-8",
		"/ui/static/favicon.svg": "image/svg+xml",
	} {
		w := get(h, path)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Errorf("GET %s: status %d, %d bytes", path, w.Code, w.Body.Len())
		}
		if ct := w.Header().Get("Content-Type"); ct != want {
			t.Errorf("GET %s: Content-Type %q, want %q", path, ct, want)
		}
	}
}

func TestStaticETagIsContentHash(t *testing.T) {
	want := fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(embedded(t, "static/app.js"))))
	if got := get(newTestHandler(t, view("web")), "/ui/static/app.js").Header().Get("ETag"); got != want {
		t.Errorf("ETag %s, want the content hash %s", got, want)
	}
}

func TestStaticETagChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.js")
	writeFile(t, file, "AAAA")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	h := overlayHandler(t, dir)
	before := get(h, "/ui/static/app.js").Header().Get("ETag")

	// Same length and, after Chtimes, the same mtime: only the content differs.
	writeFile(t, file, "BBBB")
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after := get(h, "/ui/static/app.js").Header().Get("ETag")
	if before == "" || before == after {
		t.Errorf("ETag before %q, after %q; want different tags for different content", before, after)
	}
}

func TestStaticETagCacheHit(t *testing.T) {
	h := newTestHandler(t, view("web"))
	etag := get(h, "/ui/static/style.css").Header().Get("ETag")
	if w := get(h, "/ui/static/style.css", "If-None-Match", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("matching If-None-Match: status %d, %d bytes; want 304 with no body", w.Code, w.Body.Len())
	}
	if w := get(h, "/ui/static/style.css", "If-None-Match", `"other"`); w.Code != http.StatusOK {
		t.Errorf("other If-None-Match: status %d, want 200", w.Code)
	}
}

func TestStaticCacheControl(t *testing.T) {
	if got := get(newTestHandler(t, view("web")), "/ui/static/app.js").Header().Get("Cache-Control"); got != "private, max-age=3600" {
		t.Errorf("Cache-Control %q", got)
	}
}

func TestStaticOverlayPerFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "style.css"), "/* overlay marker */")
	h := overlayHandler(t, dir)
	if body := get(h, "/ui/static/style.css").Body.String(); body != "/* overlay marker */" {
		t.Errorf("style.css = %q, want the overlay file", body)
	}
	if body := get(h, "/ui/static/app.js").Body.String(); body != embedded(t, "static/app.js") {
		t.Error("app.js should fall back to the embedded file")
	}
}

func assertFallbackWarning(t *testing.T, dir string) {
	t.Helper()
	var buf bytes.Buffer
	h, err := NewHandler(stubLister{}, Config{StaticDir: dir}, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "static_dir not found, using embedded assets") {
		t.Errorf("no fallback warning; log: %s", buf.String())
	}
	if body := get(h, "/ui/static/app.js").Body.String(); body != embedded(t, "static/app.js") {
		t.Error("app.js not served from the embedded assets")
	}
}

// Only the asset types the UI uses are served from static_dir: an overlay
// .html or extension-less file would otherwise run same-origin script.
func TestStaticOverlayAllowlist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.html"), "<script src=x.js></script>")
	writeFile(t, filepath.Join(dir, "x.js"), "alert(1)")
	writeFile(t, filepath.Join(dir, "noext"), "<html>")
	writeFile(t, filepath.Join(dir, "style.css"), "/* ok */")
	h := overlayHandler(t, dir)
	for _, path := range []string{"/ui/static/x.html", "/ui/static/noext"} {
		if w := get(h, path); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404 (type not allowed)", path, w.Code)
		}
	}
	for _, path := range []string{"/ui/static/x.js", "/ui/static/style.css"} {
		if w := get(h, path); w.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200 (allowed type)", path, w.Code)
		}
	}
}

func TestStaticOverlayMissingDir(t *testing.T) {
	assertFallbackWarning(t, filepath.Join(t.TempDir(), "nonexistent"))
}

func TestStaticOverlayNotADir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain-file")
	writeFile(t, file, "x")
	assertFallbackWarning(t, file)
}

func TestStaticOverlayConfined(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "outside")
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape.js")); err != nil {
		t.Fatal(err)
	}

	a, err := newAssets(dir, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../web.go", "escape.js"} {
		data, _, err := a.read(name)
		if err == nil || errors.Is(err, fs.ErrNotExist) || len(data) != 0 {
			t.Errorf("read(%q) = %d bytes, err %v; want a non-ErrNotExist error and no data", name, len(data), err)
		}
	}
	if w := get(overlayHandler(t, dir), "/ui/static/escape.js"); w.Code != http.StatusNotFound {
		t.Errorf("GET /ui/static/escape.js: status %d, want 404", w.Code)
	}
}

func TestStaticOverlayNoTemplates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "OVERLAY-TEMPLATE-MARKER")
	if body := mustGet(t, overlayHandler(t, dir), "/ui/"); strings.Contains(body, "OVERLAY-TEMPLATE-MARKER") {
		t.Error("static_dir overrode the status page template")
	}
}

func TestStaticNotFound(t *testing.T) {
	if w := get(newTestHandler(t), "/ui/static/nope.js"); w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestStaticTemplatesFavicon(t *testing.T) {
	for _, name := range []string{"templates/index.html", "templates/log.html"} {
		tmpl := embedded(t, name)
		requireContains(t, name, tmpl, `<link rel="icon" href="/ui/static/favicon.svg">`)
		if strings.Contains(tmpl, "data:") {
			t.Errorf("%s contains a data: URI", name)
		}
	}
}

func TestStaticContentTypeMap(t *testing.T) {
	// Simulate a host MIME database that maps .js to application/javascript.
	if err := mime.AddExtensionType(".js", "application/javascript"); err != nil {
		t.Fatal(err)
	}
	if ct := get(newTestHandler(t), "/ui/static/app.js").Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type %q depends on the host MIME database", ct)
	}
}

func TestStaticServedWithoutModTime(t *testing.T) {
	// Embedded files have no modification time; ServeContent must not emit a
	// Last-Modified header derived from it.
	if lm := get(newTestHandler(t), "/ui/static/app.js").Header().Get("Last-Modified"); lm != "" {
		if _, err := time.Parse(http.TimeFormat, lm); err == nil {
			t.Errorf("Last-Modified %q sent for an embedded file", lm)
		}
	}
}
