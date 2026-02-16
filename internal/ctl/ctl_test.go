package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockAPIServer returns a test server that mimics the Kahi API.
func mockAPIServer() *httptest.Server {
	mux := http.NewServeMux()

	processes := []ProcessInfo{
		{Name: "web", Group: "web", State: "RUNNING", PID: 1234, Uptime: 90061},
		{Name: "worker", Group: "worker", State: "STOPPED", PID: 0},
		{Name: "api", Group: "api", State: "FATAL", ExitStatus: 1},
	}

	mux.HandleFunc("GET /api/v1/processes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(processes)
	})

	mux.HandleFunc("GET /api/v1/processes/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		for _, p := range processes {
			if p.Name == name {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(p)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("no such process: %s", name)})
	})

	mux.HandleFunc("POST /api/v1/processes/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "name": name})
	})

	mux.HandleFunc("POST /api/v1/processes/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "name": name})
	})

	mux.HandleFunc("POST /api/v1/processes/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "restarted", "name": name})
	})

	mux.HandleFunc("POST /api/v1/processes/{name}/signal", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			Signal string `json:"signal"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Signal == "INVALID" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid signal: INVALID"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "signaled", "name": name})
	})

	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("log line 1\nlog line 2\n"))
	})

	mux.HandleFunc("GET /api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]string{"web", "worker"})
	})

	mux.HandleFunc("POST /api/v1/groups/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "group": name})
	})

	mux.HandleFunc("POST /api/v1/groups/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "group": name})
	})

	mux.HandleFunc("POST /api/v1/groups/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "restarted", "group": name})
	})

	mux.HandleFunc("POST /api/v1/processes/{name}/stdin", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			Data string `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "written", "name": name})
	})

	mux.HandleFunc("GET /api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"test": "config"})
	})

	mux.HandleFunc("POST /api/v1/config/reload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "reloaded",
			"added":   []string{"new"},
			"changed": []string{},
			"removed": []string{},
		})
	})

	mux.HandleFunc("POST /api/v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting_down"})
	})

	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": "1.0.0",
			"commit":  "abc123",
			"pid":     42,
		})
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})

	return httptest.NewServer(mux)
}

func testClient(ts *httptest.Server) *Client {
	addr := strings.TrimPrefix(ts.URL, "http://")
	return NewTCPClient(addr, "", "")
}

func TestClientStart(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.Start("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientStop(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.Stop("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRestart(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.Restart("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientSignal(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.Signal("web", "HUP"); err != nil {
		t.Fatal(err)
	}
}

func TestClientSignalInvalid(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	err := c.Signal("web", "INVALID")
	if err == nil {
		t.Fatal("expected error for invalid signal")
	}
	if !strings.Contains(err.Error(), "invalid signal") {
		t.Fatalf("expected 'invalid signal', got: %s", err)
	}
}

func TestClientStartGroup(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.StartGroup("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientStatus(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	var buf bytes.Buffer
	if err := c.Status(nil, false, &buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "web") {
		t.Fatal("expected 'web' in output")
	}
	if !strings.Contains(output, "RUNNING") {
		t.Fatal("expected 'RUNNING' in output")
	}
	if !strings.Contains(output, "NAME") {
		t.Fatal("expected header 'NAME' in output")
	}
}

func TestClientStatusJSON(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	var buf bytes.Buffer
	if err := c.Status(nil, true, &buf); err != nil {
		t.Fatal(err)
	}

	var procs []ProcessInfo
	if err := json.Unmarshal(buf.Bytes(), &procs); err != nil {
		t.Fatalf("expected valid JSON, got: %s", buf.String())
	}
	if len(procs) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(procs))
	}
}

func TestClientStatusFilter(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	var buf bytes.Buffer
	if err := c.Status([]string{"web"}, false, &buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "web") {
		t.Fatal("expected 'web' in output")
	}
	if strings.Contains(output, "worker") {
		t.Fatal("expected 'worker' to be filtered out")
	}
}

func TestClientTail(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	var buf bytes.Buffer
	if err := c.Tail("web", "stdout", 1600, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "log line 1") {
		t.Fatal("expected log output")
	}
}

func TestClientShutdown(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestClientReload(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	result, err := c.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "reloaded" {
		t.Fatalf("expected reloaded, got %v", result["status"])
	}
}

func TestClientVersion(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	result, err := c.Version()
	if err != nil {
		t.Fatal(err)
	}
	if result["version"] != "1.0.0" {
		t.Fatalf("expected 1.0.0, got %v", result["version"])
	}
}

func TestClientHealth(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	status, err := c.Health()
	if err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("expected ok, got %s", status)
	}
}

func TestClientReady(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	status, err := c.Ready(nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != "ready" {
		t.Fatalf("expected ready, got %s", status)
	}
}

func TestClientConnectionFailure(t *testing.T) {
	c := NewTCPClient("127.0.0.1:1", "", "")
	err := c.Start("web")
	if err == nil {
		t.Fatal("expected connection error")
	}
	if !strings.Contains(err.Error(), "connection failed") {
		t.Fatalf("expected 'connection failed', got: %s", err)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{30 * time.Second, "0m"},
		{90 * time.Minute, "1h 30m"},
		{25*time.Hour + 30*time.Minute, "1d 1h 30m"},
		{48 * time.Hour, "2d 0h 0m"},
	}

	for _, tc := range tests {
		result := formatDuration(tc.input)
		if result != tc.expected {
			t.Errorf("formatDuration(%v): expected %q, got %q", tc.input, tc.expected, result)
		}
	}
}

func TestColorState(t *testing.T) {
	if !strings.Contains(colorState("RUNNING"), "\033[32m") {
		t.Fatal("expected green for RUNNING")
	}
	if !strings.Contains(colorState("FATAL"), "\033[31m") {
		t.Fatal("expected red for FATAL")
	}
	if !strings.Contains(colorState("STARTING"), "\033[33m") {
		t.Fatal("expected yellow for STARTING")
	}
	if colorState("STOPPED") != "STOPPED" {
		t.Fatal("STOPPED should not be colored")
	}
}

func TestStatusTableFormat(t *testing.T) {
	procs := []ProcessInfo{
		{Name: "web", State: "RUNNING", PID: 1234, Uptime: 3600},
		{Name: "api", State: "EXITED", ExitStatus: 1},
	}

	var buf bytes.Buffer
	if err := formatStatusTable(procs, &buf, false); err != nil {
		t.Fatal(err)
	}
	output := buf.String()

	if !strings.Contains(output, "NAME") {
		t.Fatal("expected header")
	}
	if !strings.Contains(output, "1234") {
		t.Fatal("expected PID")
	}
	if !strings.Contains(output, "exit 1") {
		t.Fatal("expected exit status for EXITED process")
	}
}

func TestClientPID(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	pid, err := c.PID("web")
	if err != nil {
		t.Fatal(err)
	}
	if pid != "1234" {
		t.Fatalf("expected 1234, got %s", pid)
	}
}

func TestClientProcessNotFound(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	_, err := c.PID("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientEmptyStatusTable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]ProcessInfo{})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	var buf bytes.Buffer
	if err := c.Status(nil, false, &buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "NAME") {
		t.Fatal("expected header even with no processes")
	}
}

func TestClientStopGroup(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.StopGroup("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRestartGroup(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.RestartGroup("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientReread(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	result, err := c.Reread()
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "reloaded" {
		t.Fatalf("expected reloaded, got %v", result["status"])
	}
}

func TestClientWriteStdin(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	if err := c.WriteStdin("web", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestNewUnixClient(t *testing.T) {
	c := NewUnixClient("/tmp/kahi-test.sock")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.baseURL != "http://unix" {
		t.Fatalf("expected baseURL 'http://unix', got %q", c.baseURL)
	}
	if c.httpClient == nil {
		t.Fatal("expected non-nil httpClient")
	}
}

func TestClientPIDDaemon(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	pid, err := c.PID("")
	if err != nil {
		t.Fatal(err)
	}
	if pid == "" {
		t.Fatal("expected non-empty pid")
	}
}

func TestClientTailError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "process not running"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	var buf bytes.Buffer
	err := c.Tail("web", "stdout", 1600, &buf)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "process not running") {
		t.Fatalf("expected 'process not running', got: %s", err)
	}
}

func TestColorStateStopping(t *testing.T) {
	result := colorState("STOPPING")
	if !strings.Contains(result, "\033[33m") {
		t.Fatal("expected yellow for STOPPING")
	}
	if !strings.Contains(result, "STOPPING") {
		t.Fatal("expected STOPPING in output")
	}
}

func TestClientTailFollow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}/stream",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprint(w, "data: hello world\n\n")
			flusher.Flush()
		})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var buf bytes.Buffer
	_ = c.TailFollow(ctx, "web", "stdout", &buf)

	if !strings.Contains(buf.String(), "hello world") {
		t.Fatalf("expected 'hello world', got: %s", buf.String())
	}
}

func TestClientBasicAuth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/processes/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "admin", "secret")

	if err := c.Start("web"); err != nil {
		t.Fatal(err)
	}
}

func TestClientTailFollowError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}/stream",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "process not running"})
		})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	ctx := context.Background()
	var buf bytes.Buffer
	err := c.TailFollow(ctx, "web", "stdout", &buf)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "process not running") {
		t.Fatalf("expected 'process not running', got: %s", err)
	}
}

func TestClientTailErrorNonJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	var buf bytes.Buffer
	err := c.Tail("web", "stdout", 1600, &buf)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "server error") {
		t.Fatalf("expected 'server error', got: %s", err)
	}
}

func TestClientTailFollowErrorNonJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}/stream",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	ctx := context.Background()
	var buf bytes.Buffer
	err := c.TailFollow(ctx, "web", "stdout", &buf)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "server error") {
		t.Fatalf("expected 'server error', got: %s", err)
	}
}

func TestClientTailDefaultStream(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	var buf bytes.Buffer
	if err := c.Tail("web", "", 1600, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "log line 1") {
		t.Fatal("expected log output with default stream")
	}
}

func TestClientTailFollowDefaultStream(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}/stream",
		func(w http.ResponseWriter, r *http.Request) {
			stream := r.PathValue("stream")
			if stream != "stdout" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprint(w, "data: default stream\n\n")
			flusher.Flush()
		})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "", "")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var buf bytes.Buffer
	_ = c.TailFollow(ctx, "web", "", &buf)

	if !strings.Contains(buf.String(), "default stream") {
		t.Fatalf("expected 'default stream', got: %s", buf.String())
	}
}

func TestClientTailFollowWithAuth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}/stream",
		func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "admin" || pass != "secret" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprint(w, "data: authed\n\n")
			flusher.Flush()
		})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	c := NewTCPClient(addr, "admin", "secret")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var buf bytes.Buffer
	_ = c.TailFollow(ctx, "web", "stdout", &buf)

	if !strings.Contains(buf.String(), "authed") {
		t.Fatalf("expected 'authed', got: %s", buf.String())
	}
}

func TestClientReadyWithProcesses(t *testing.T) {
	ts := mockAPIServer()
	defer ts.Close()
	c := testClient(ts)

	status, err := c.Ready([]string{"web", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if status != "ready" {
		t.Fatalf("expected ready, got %s", status)
	}
}
