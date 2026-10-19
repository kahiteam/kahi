package web

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	cssRule    = regexp.MustCompile(`([^{}]+)\{([^}]*)\}`)
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// cssRuleHas reports whether a rule whose selector list includes selector has
// a body containing decl.
func cssRuleHas(css, selector, decl string) bool {
	for _, m := range cssRule.FindAllStringSubmatch(cssComment.ReplaceAllString(css, ""), -1) {
		for _, sel := range strings.Split(m[1], ",") {
			if strings.TrimSpace(sel) == selector && strings.Contains(m[2], decl) {
				return true
			}
		}
	}
	return false
}

func TestCSSStateColors(t *testing.T) {
	css := embedded(t, "static/style.css")
	for selector, color := range map[string]string{
		".state-running":  "var(--color-running)",
		".state-fatal":    "var(--color-fatal)",
		".state-starting": "var(--color-starting)",
		".state-backoff":  "var(--color-starting)",
		".state-stopped":  "var(--color-stopped)",
		".state-exited":   "var(--color-stopped)",
		".state-unknown":  "var(--color-stopped)",
		".state-stopping": "var(--color-stopping)",
	} {
		if !cssRuleHas(css, selector, color) {
			t.Errorf("no %s rule using %s", selector, color)
		}
	}
}

func TestCSSResponsiveMediaQuery(t *testing.T) {
	for _, name := range []string{"templates/index.html", "templates/log.html"} {
		requireContains(t, name, embedded(t, name), `<meta name="viewport"`)
	}
	requireContains(t, "style.css", embedded(t, "static/style.css"), "@media (max-width: 768px)")
}

func TestCSSDataLabels(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	tbody := regexp.MustCompile(`(?s)<tbody.*</tbody>`).FindString(body)
	tds := regexp.MustCompile(`<td[^>]*>`).FindAllString(tbody, -1)
	if len(tds) == 0 {
		t.Fatal("no table cells rendered")
	}
	for _, td := range tds {
		if !regexp.MustCompile(`data-label="[^"]+"`).MatchString(td) {
			t.Errorf("cell without a data-label: %s", td)
		}
	}
	requireContains(t, "app.js", embedded(t, "static/app.js"), "dataset.label")
}

func TestCSSControlGrouping(t *testing.T) {
	body := mustGet(t, newTestHandler(t, view("web")), "/ui/")
	at := strings.Index(body, `data-action="start"`)
	if at < 0 {
		t.Fatal("no Start button")
	}
	start := strings.LastIndex(body[:at], "<td")
	cell := body[start : at+strings.Index(body[at:], "</td>")]
	requireContains(t, "actions cell", cell,
		`data-action="start" disabled>Start</button>`, `data-action="stop" disabled>Stop</button>`,
		`data-action="restart" disabled>Restart</button>`, ">Tail Stdout</a>", ">Tail Stderr</a>")
}

func TestCSSMonospaceLogViewer(t *testing.T) {
	css := embedded(t, "static/style.css")
	if !cssRuleHas(css, "#log-output", "monospace") {
		t.Error("the log output rule has no monospace font-family")
	}
	if !cssRuleHas(css, "#log-viewer", "background: #1e1e1e") {
		t.Error("the log viewer rule has no dark background")
	}
}

func TestCSSSGRClasses(t *testing.T) {
	css := embedded(t, "static/style.css")
	selectors := []string{".sgr-bold"}
	for _, base := range []int{30, 90} {
		for i := range 8 {
			selectors = append(selectors, ".sgr-fg-"+strconv.Itoa(base+i))
		}
	}
	for _, sel := range selectors {
		if !cssRuleHas(css, sel, "") {
			t.Errorf("style.css has no %s rule", sel)
		}
	}
}

func TestCSSNoExternalURLs(t *testing.T) {
	for _, name := range []string{
		"static/style.css", "static/app.js", "static/log.js",
		"templates/index.html", "templates/log.html",
	} {
		src := embedded(t, name)
		if strings.Contains(src, "http://") || strings.Contains(src, "https://") {
			t.Errorf("%s references an external URL", name)
		}
	}
}
