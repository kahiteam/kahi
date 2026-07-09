//go:build unix

package logging

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openLogFile opens path with logFileOpenFlags while hardening against symlink
// attacks on both the parent directories and the final component.
//
// SEC-019 / ADR-022: the SEC-012 O_NOFOLLOW guard constrains only the last
// component, so an attacker who swaps a parent directory for a symlink could
// redirect a (possibly privileged) daemon's log writes to an attacker-chosen
// file. Hardening works in two stages:
//
//  1. The parent directory is resolved through os.Root, rooted at the
//     filesystem root ("/") for absolute paths or the current directory for
//     relative ones. os.Root walks each component refusing any symlink that
//     escapes the root (an absolute target, or one climbing out via ".."),
//     which is the attacker-redirect case, while still resolving legitimate
//     in-tree symlinks such as macOS /var -> private/var. A swapped parent
//     that points outside the tree therefore fails the open instead of
//     redirecting the write.
//
//  2. The final component is opened atomically with openat(2) relative to the
//     verified parent directory fd, carrying O_NOFOLLOW so a symlink at the log
//     path itself is still rejected (preserving SEC-012). os.Root does not
//     honour O_NOFOLLOW on the final component, so the raw openat is required.
func openLogFile(path string, perm os.FileMode) (*os.File, error) {
	clean := filepath.Clean(path)
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)

	// A log path must name a file, not a directory root or a traversal element.
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return nil, &os.PathError{Op: "open", Path: path, Err: unix.EISDIR}
	}

	var root *os.Root
	var relDir string
	var err error
	if filepath.IsAbs(clean) {
		root, err = os.OpenRoot("/")
		relDir = strings.TrimPrefix(dir, "/")
	} else {
		root, err = os.OpenRoot(".")
		relDir = dir
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if relDir == "" {
		relDir = "."
	}

	dirFile, err := root.Open(relDir)
	if err != nil {
		return nil, err
	}
	defer dirFile.Close()

	flags := logFileOpenFlags | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(int(dirFile.Fd()), base, flags, uint32(perm.Perm()))
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
