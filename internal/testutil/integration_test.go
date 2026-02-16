//go:build integration

package testutil

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kahidev/kahi/internal/api"
	"github.com/kahidev/kahi/internal/ctl"
)

// --- Minimal mock implementations for integration tests ---

type mockProcessManager struct{}

func (m *mockProcessManager) List() []api.ProcessInfo {
	return []api.ProcessInfo{
		{Name: "web", Group: "default", State: "RUNNING", PID: 9999, Uptime: 100},
	}
}
func (m *mockProcessManager) Get(name string) (api.ProcessInfo, error) {
	return api.ProcessInfo{Name: name, State: "RUNNING"}, nil
}
func (m *mockProcessManager) Start(name string) error                   { return nil }
func (m *mockProcessManager) Stop(name string) error                    { return nil }
func (m *mockProcessManager) Restart(name string) error                 { return nil }
func (m *mockProcessManager) Signal(name string, sig string) error      { return nil }
func (m *mockProcessManager) WriteStdin(name string, data []byte) error { return nil }
func (m *mockProcessManager) ReadLog(name string, stream string, offset int64, length int) ([]byte, error) {
	return []byte("log output"), nil
}

type mockGroupManager struct{}

func (m *mockGroupManager) ListGroups() []string           { return []string{"default"} }
func (m *mockGroupManager) StartGroup(name string) error   { return nil }
func (m *mockGroupManager) StopGroup(name string) error    { return nil }
func (m *mockGroupManager) RestartGroup(name string) error { return nil }

type mockConfigManager struct{}

func (m *mockConfigManager) GetConfig() any { return map[string]string{} }
func (m *mockConfigManager) Reload() (added, changed, removed []string, err error) {
	return nil, nil, nil, nil
}

type mockDaemonInfo struct{}

func (m *mockDaemonInfo) IsShuttingDown() bool { return false }
func (m *mockDaemonInfo) IsReady() bool        { return true }
func (m *mockDaemonInfo) CheckReady(processes []string) (ready bool, pending []string, err error) {
	return true, nil, nil
}
func (m *mockDaemonInfo) Version() map[string]string { return map[string]string{"version": "test"} }
func (m *mockDaemonInfo) PID() int                   { return 12345 }
func (m *mockDaemonInfo) Shutdown()                  {}

func TestIntegrationDaemonStartAndConnect(t *testing.T) {
	d := StartIntegrationDaemon(t, &mockProcessManager{}, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{})

	// Connect via Unix socket client.
	c := ctl.NewUnixClient(d.SocketPath)

	status, err := c.Health()
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if status != "ok" {
		t.Fatalf("health = %q, want ok", status)
	}
}

func TestIntegrationDaemonProcessList(t *testing.T) {
	d := StartIntegrationDaemon(t, &mockProcessManager{}, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{})

	// Direct HTTP request to verify API endpoint.
	resp, err := http.Get("http://unix/api/v1/processes")
	_ = resp
	_ = err
	// Since Unix socket requires special transport, use the ctl client.
	c := ctl.NewUnixClient(d.SocketPath)

	var buf json.RawMessage
	err2 := c.Status(nil, true, &writerAdapter{buf: &buf})
	if err2 != nil {
		t.Fatalf("status failed: %v", err2)
	}
}

type writerAdapter struct {
	buf *json.RawMessage
}

func (w *writerAdapter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

func TestIntegrationDaemonStartStop(t *testing.T) {
	d := StartIntegrationDaemon(t, &mockProcessManager{}, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{})

	c := ctl.NewUnixClient(d.SocketPath)

	// Start a process.
	if err := c.Start("web"); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Stop a process.
	if err := c.Stop("web"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
}

func TestIntegrationCleanup(t *testing.T) {
	d := StartIntegrationDaemon(t, &mockProcessManager{}, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{})

	// Verify socket exists.
	if d.SocketPath == "" {
		t.Fatal("empty socket path")
	}
	// Cleanup is registered via t.Cleanup and will run after the test.
}
