//go:build integration

package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/events"
)

// IntegrationDaemon is a real Kahi daemon started for integration testing.
type IntegrationDaemon struct {
	Server     *api.Server
	Bus        *events.Bus
	SocketPath string
	Dir        string
	cancel     context.CancelFunc
}

// StartIntegrationDaemon starts a real API server on a random Unix socket
// for integration testing. Registers cleanup to shut down the server.
func StartIntegrationDaemon(t *testing.T, pm api.ProcessManager, gm api.GroupManager, cm api.ConfigManager, di api.DaemonInfo) *IntegrationDaemon {
	t.Helper()

	dir := TempDir(t)
	socketPath := fmt.Sprintf("%s/kahi-integration-%d.sock", dir, time.Now().UnixNano())
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bus := events.NewBus(logger)

	cfg := api.Config{
		UnixSocket: socketPath,
		SocketMode: 0700,
	}

	srv := api.NewServer(cfg, pm, gm, cm, di, bus, logger)

	if err := srv.StartUnix(socketPath, 0700); err != nil {
		t.Fatalf("cannot start integration daemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	t.Cleanup(func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Stop(shutdownCtx)
		_ = os.RemoveAll(dir)
	})

	// Wait for socket to be ready.
	WaitFor(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 5*time.Second)

	_ = ctx

	return &IntegrationDaemon{
		Server:     srv,
		Bus:        bus,
		SocketPath: socketPath,
		Dir:        dir,
		cancel:     cancel,
	}
}
