package ctl

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowServer answers every request after delay.
func slowServer(delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/all/") {
			_, _ = w.Write([]byte(`{"results":[{"name":"web","status":"ok"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","version":"x"}`))
	}))
}

// Actions that wait on processes are not cut off by the query limit (#222).
func TestActionOutlastsQueryTimeout(t *testing.T) {
	ts := slowServer(300 * time.Millisecond)
	defer ts.Close()
	c := NewTCPClient(strings.TrimPrefix(ts.URL, "http://"), "", "")
	c.queryTimeout = 50 * time.Millisecond

	results, err := c.All("stop")
	if err != nil || len(results) != 1 || results[0].Status != "ok" {
		t.Fatalf("All(stop) = %v, %v; want one ok result after the delay", results, err)
	}
	if err := c.Stop("web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := c.Version(); err == nil {
		t.Fatal("Version outlived the query timeout")
	}
}

func TestActionTimeout(t *testing.T) {
	ts := slowServer(500 * time.Millisecond)
	defer ts.Close()
	c := NewTCPClient(strings.TrimPrefix(ts.URL, "http://"), "", "")
	c.SetActionTimeout(100 * time.Millisecond)

	start := time.Now()
	_, err := c.All("stop")
	if err == nil || err.Error() != "daemon still working after 100ms" {
		t.Fatalf("All(stop) err = %v, want 'daemon still working after 100ms'", err)
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Errorf("gave up after %v, want about 100ms", elapsed)
	}
}

// A query keeps its body readable after the request returns: the context is
// released when the body is closed, not before.
func TestQueryBodyReadableAfterReturn(t *testing.T) {
	ts := slowServer(0)
	defer ts.Close()
	c := NewTCPClient(strings.TrimPrefix(ts.URL, "http://"), "", "")
	v, err := c.Version()
	if err != nil || v["version"] != "x" {
		t.Fatalf("Version = %v, %v", v, err)
	}
}
