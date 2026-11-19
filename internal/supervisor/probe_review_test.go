package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An existing private socket directory must also let the daemon create
// the socket in it (PrepareSocketDir's CreateTemp).
func TestProbePrivateDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes any directory")
	}
	dir := filepath.Join(t.TempDir(), "kahi-x")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := probePrivateDir(dir); err == nil || !strings.HasPrefix(err.Error(), "permission denied: cannot create socket in "+dir) {
		t.Errorf("0500: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := probePrivateDir(dir); err != nil {
		t.Errorf("0700: %v", err)
	}
}
