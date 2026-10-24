package api

import (
	"net/http"
	"testing"
)

// The Web UI's XHR POSTs are same-origin, which SEC-025's protection allows;
// the same request from another site stays rejected (SEC-026).
func TestUISecuritySameOriginPost(t *testing.T) {
	srv, pm, _, _ := crossOriginServer()
	pm.processes[0].State = "STOPPED"

	w := serve(srv.handler, post("/api/v1/processes/web/start", map[string]string{"Sec-Fetch-Site": "same-origin"}))
	if w.Code != http.StatusOK {
		t.Errorf("same-origin POST: status %d, body %s; want 200", w.Code, w.Body)
	}
	w = serve(srv.handler, post("/api/v1/processes/web/start", map[string]string{"Sec-Fetch-Site": "cross-site"}))
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: status %d; want 403", w.Code)
	}
}
