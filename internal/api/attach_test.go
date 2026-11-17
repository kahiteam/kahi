package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachFrames(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, FrameStdout, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := ReadFrame(&b)
	if err != nil || typ != FrameStdout || string(data) != "hello" {
		t.Fatalf("%d %q %v", typ, data, err)
	}
	big := []byte{FrameStdin, 0xff, 0xff, 0xff, 0xff}
	if _, _, err := ReadFrame(bytes.NewReader(big)); err == nil {
		t.Error("oversized frame accepted")
	}
}

// Errors are JSON and come before any upgrade (FUNC-034).
func TestAttachEndpointErrors(t *testing.T) {
	srv, _, _ := testServer()
	for _, c := range []struct {
		name, upgrade string
		status        int
		msg           string
	}{
		{"web", "", 400, "attach requires Upgrade: kahi-attach"},
		{"nosuch", "kahi-attach", 404, "no such process"},
		{"web", "kahi-attach", 400, "process not running: web"},
	} {
		req := httptest.NewRequest("POST", "/api/v1/processes/"+c.name+"/attach", nil)
		if c.upgrade != "" {
			req.Header.Set("Upgrade", c.upgrade)
			req.Header.Set("Connection", "keep-alive, Upgrade")
		}
		w := httptest.NewRecorder()
		srv.handler.ServeHTTP(w, req)
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.msg) {
			t.Errorf("%s/%q: %d %s", c.name, c.upgrade, w.Code, w.Body)
		}
	}
	for msg, want := range map[string]int{"process web: already attached": http.StatusConflict, "process web: attached": http.StatusConflict} {
		if got := classifyError(errString(msg)); got != want {
			t.Errorf("%q -> %d", msg, got)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
