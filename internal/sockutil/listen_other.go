//go:build !unix

// Package sockutil creates Unix domain sockets that are owner-only from the
// moment they exist.
//
// SEC-023 / ADR-026: platforms without umask semantics fall back to a plain
// bind. The caller's chmod still narrows the mode afterwards, so this keeps the
// previous behavior; the at-creation guarantee does not apply here. No shipped
// target builds this file: GoReleaser targets linux and darwin only.
package sockutil

import "net"

// ListenUnix binds a Unix domain socket at path. See listen_unix.go.
func ListenUnix(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
