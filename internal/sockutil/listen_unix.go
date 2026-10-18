//go:build unix

// Package sockutil creates Unix domain sockets that are owner-only from the
// moment they exist.
//
// SEC-023 / ADR-026: net.Listen("unix", ...) creates the socket inode at
// 0777 &^ umask, and callers narrow it with os.Chmod only afterwards. Kahi
// inherits its umask from whatever launched it, so under a permissive umask the
// socket is briefly connectable by other users, and a connection accepted in
// that window survives the chmod. Binding under a tightened umask removes the
// dependence on the inherited one.
package sockutil

import (
	"net"
	"sync"
	"syscall"
)

// umaskMu serializes the read-tighten-bind-restore sequence. Without it two
// concurrent calls interleave: the second saves the first's temporary mask as
// its "previous" umask and restores it last, leaving the daemon permanently at
// the temporary mask.
var umaskMu sync.Mutex

// afterUmask is a test hook, called while the temporary umask is in effect and
// before the bind. It lets tests prove that a second call cannot enter the
// critical section while the first holds it.
var afterUmask = func() {}

// ListenUnix binds a Unix domain socket at path with the umask tightened to
// the inherited umask plus 077, so the socket is created no wider than 0700.
// The caller still applies the configured mode with chmod afterwards.
//
// The mask is only ever tightened, never loosened. POSIX cannot read the umask
// without setting it, so it is read by setting the strictest mask, 0777. The
// effective umask is therefore 0777 during the read, inherited|077 during the
// bind, and the inherited value again afterwards. A file another goroutine
// creates during this window may come out more restrictive than requested, but
// never wider than the inherited umask allows.
func ListenUnix(path string) (net.Listener, error) {
	umaskMu.Lock()
	defer umaskMu.Unlock()

	old := syscall.Umask(0o777)
	defer syscall.Umask(old)
	syscall.Umask(old | 0o077)

	afterUmask()
	return net.Listen("unix", path)
}
