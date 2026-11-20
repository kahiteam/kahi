//go:build unix

// Package sockutil creates Unix domain sockets that are owner-only from the
// moment they exist.
//
// SEC-023 / ADR-026: net.Listen("unix", ...) creates the socket inode at
// 0777 &^ umask. Kahi inherits its umask from whatever launched it, so under
// a permissive umask a socket narrowed by chmod afterwards is briefly
// connectable by other users, and a connection accepted in that window
// survives the chmod. Binding under a umask derived from the wanted mode
// removes both the dependence on the inherited umask and the chmod.
package sockutil

import (
	"net"
	"os"
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

// ListenUnix binds a Unix domain socket at path, created with exactly mode's
// owner bits and never any group or other bit, so no chmod by path is needed
// afterwards (decided 2026-11-20, v0.3.0 security review: a chmod by path
// after the bind followed a symlink swapped in between, as root, when the
// socket directory belonged to the drop user).
//
// The bind runs under the umask (0777 &^ mode) | 077 instead of the inherited
// one: the inherited umask no longer narrows the configured mode (the chmod
// that used to correct it is gone), and group and other bits are masked
// whatever mode says. POSIX cannot read the umask without setting it, so it
// is read by setting the strictest mask, 0777. The effective umask is
// therefore 0777 during the read, the bind mask during the bind, and the
// inherited value again afterwards. A file another goroutine creates in this
// window may come out without group or other bits, never with more.
func ListenUnix(path string, mode os.FileMode) (net.Listener, error) {
	umaskMu.Lock()
	defer umaskMu.Unlock()

	old := syscall.Umask(0o777)
	defer syscall.Umask(old)
	syscall.Umask(BindUmask(mode))

	afterUmask()
	return net.Listen("unix", path)
}

// BindUmask is the umask ListenUnix binds under for mode.
func BindUmask(mode os.FileMode) int {
	return int(0o777&^mode.Perm()) | 0o077
}
