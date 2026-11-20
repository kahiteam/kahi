//go:build !unix

// Package sockutil creates Unix domain sockets that are owner-only from the
// moment they exist.
//
// SEC-023 / ADR-026: platforms without umask semantics fall back to a plain
// bind; the at-creation guarantee does not apply here. No shipped
// target builds this file: GoReleaser targets linux and darwin only.
package sockutil

import (
	"net"
	"os"
)

// ListenUnix binds a Unix domain socket at path. See listen_unix.go; mode is
// not applied here, and the caller's mode check fails the start.
func ListenUnix(path string, mode os.FileMode) (net.Listener, error) {
	return net.Listen("unix", path)
}
