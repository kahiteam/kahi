package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUISecurityHostileName(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view(`a'b"<c>&d`)), "/ui/")
	requireContains(t, "status page", body,
		`<td class="col-name" data-label="Name">a&#39;b&#34;&lt;c&gt;&amp;d</td>`,
		`data-name="a&#39;b&#34;&lt;c&gt;&amp;d"`)
	for _, raw := range []string{`a'b"<c>&d`, "<c>"} {
		if strings.Contains(body, raw) {
			t.Errorf("the raw name fragment %q appears in the page", raw)
		}
	}
}

func TestUISecurityPathEscape(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("x y/#?")), "/ui/")
	requireContains(t, "status page", body,
		`href="/ui/log/x%20y%2F%23%3F/stdout"`, `href="/ui/log/x%20y%2F%23%3F/stderr"`)
}

func TestUISecurityAssetScan(t *testing.T) {
	forbidden := []string{
		"onclick=", "onload=", "onerror=", "innerHTML", "outerHTML", "insertAdjacentHTML",
		"eval(", "new Function", "style=", "<style", "data:",
	}
	for _, src := range []struct {
		fsys fs.FS
		dir  string
	}{{templateFS, "templates"}, {staticFS, "static"}} {
		err := fs.WalkDir(src.fsys, src.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path == "static/favicon.svg" {
				return err
			}
			body := embedded(t, path)
			for _, bad := range forbidden {
				if strings.Contains(body, bad) {
					t.Errorf("%s contains forbidden %q", path, bad)
				}
			}
			requireNoInlineScript(t, path, body)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestUISecurityNoTemplateBypass(t *testing.T) {
	bypass := map[string]bool{"HTML": true, "JS": true, "URL": true, "HTMLAttr": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "template" && bypass[sel.Sel.Name] {
					t.Errorf("%s uses template.%s, which bypasses escaping", file, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestUISecurityURLEncoding(t *testing.T) {
	for _, name := range []string{"static/app.js", "static/log.js"} {
		for i, line := range strings.Split(embedded(t, name), "\n") {
			if (strings.Contains(line, "/api/v1/processes/") || strings.Contains(line, "/ui/log/")) &&
				!strings.Contains(line, "encodeURIComponent(") {
				t.Errorf("%s:%d builds a URL with a name but no encodeURIComponent: %s", name, i+1, line)
			}
		}
	}
}

func TestUISecurityHeaders(t *testing.T) {
	h := newTestHandler(t, view("web"))
	for _, path := range []string{
		"/ui/", "/ui/log/web/stdout", "/ui/log/web/stdin", "/ui/log/nosuch/stdout",
		"/ui/static/app.js", "/ui/static/nope.js", "/ui/unknown",
	} {
		hdr := get(h, path).Header()
		if got := hdr.Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("GET %s: Content-Security-Policy %q", path, got)
		}
		if got := hdr.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: X-Content-Type-Options %q", path, got)
		}
	}
	want := "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	if contentSecurityPolicy != want {
		t.Errorf("policy %q, want %q", contentSecurityPolicy, want)
	}
}

func TestUISecuritySetHeadersExported(t *testing.T) {
	h := http.Header{}
	SetSecurityHeaders(h)
	if h.Get("Content-Security-Policy") == "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("SetSecurityHeaders set %v", h)
	}
}
