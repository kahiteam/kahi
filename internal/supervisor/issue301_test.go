package supervisor

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// The shutdown timeout is read under the lock a reload writes s.config
// with; run with -race (#301).
func TestShutdownTimeoutReadUnderLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte("[supervisor]\nshutdown_timeout = 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(SupervisorConfig{Config: cfg, ConfigPath: path, Logger: discardLogger()})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			_, _ = s.Reload()
		}
	}()
	for range 200 {
		if d := s.shutdownTimeout(); d.Seconds() != 7 {
			t.Fatalf("timeout %v", d)
		}
	}
	wg.Wait()
}
