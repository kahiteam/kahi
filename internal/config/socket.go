package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// RootSocketPath is the default Unix socket for a daemon run as root.
const RootSocketPath = "/var/run/kahi.sock"

// DefaultSocketPath returns the Unix socket used when [server.unix] file is
// unset, for the daemon and for kahi ctl alike (FUNC-044). Root gets
// /var/run/kahi.sock. Other users get $XDG_RUNTIME_DIR/kahi.sock, a directory
// only they can reach, or else kahi.sock in a kahi-<uid> directory under the
// temp dir, which the daemon creates owner-only (PrivateSocketDir). A fixed
// name directly in a shared /tmp would let another user take the path first.
func DefaultSocketPath() string {
	return defaultSocketPath(os.Geteuid(), os.Getenv("XDG_RUNTIME_DIR"), os.TempDir())
}

// PrivateSocketDir is the per-user directory DefaultSocketPath falls back to.
func PrivateSocketDir() string {
	return privateSocketDir(os.Geteuid(), os.TempDir())
}

func defaultSocketPath(euid int, xdgRuntimeDir, tmp string) string {
	if euid == 0 {
		return RootSocketPath
	}
	if filepath.IsAbs(xdgRuntimeDir) {
		return filepath.Join(xdgRuntimeDir, "kahi.sock")
	}
	return filepath.Join(privateSocketDir(euid, tmp), "kahi.sock")
}

func privateSocketDir(euid int, tmp string) string {
	return filepath.Join(tmp, "kahi-"+strconv.Itoa(euid))
}
