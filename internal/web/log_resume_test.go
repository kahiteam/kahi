package web

import (
	"strings"
	"testing"
)

// handlerSource returns the source of the listener log.js registers for an
// SSE event type, up to the closing "});".
func handlerSource(t *testing.T, js, event string) string {
	t.Helper()
	start := strings.Index(js, "source.addEventListener('"+event+"'")
	if start < 0 {
		t.Fatalf("log.js registers no %q listener", event)
	}
	end := strings.Index(js[start:], "});")
	if end < 0 {
		t.Fatalf("%q listener not closed", event)
	}
	return js[start : start+end]
}

// On a resumed stream the viewer ends the pending partial line before it
// marks a gap or a reset (FUNC-079, ADR-033): markBreak flushes first.
func TestLogViewerGapReset(t *testing.T) {
	js := embedded(t, "static/log.js")
	mark := js[strings.Index(js, "function markBreak"):]
	mark = mark[:strings.Index(mark, "}")]
	if f, r := strings.Index(mark, "flushPending()"), strings.Index(mark, "renderLine(text)"); f < 0 || r < 0 || f > r {
		t.Fatalf("markBreak must flush the pending line before rendering the marker:\n%s", mark)
	}
	for event, text := range map[string]string{"gap": "' bytes not shown]'", "reset": "'[log restarted]'"} {
		h := handlerSource(t, js, event)
		if !strings.Contains(h, "markBreak(") || !strings.Contains(h, text) {
			t.Errorf("%s listener does not render %s through markBreak:\n%s", event, text, h)
		}
	}
}
