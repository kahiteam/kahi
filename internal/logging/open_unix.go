//go:build unix

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openLogFile opens path with logFileOpenFlags while hardening against symlink
// attacks on both the parent directories and the final component.
//
// SEC-019 / ADR-022: the parent directory is reached by openLogParent, one
// component at a time with O_NOFOLLOW; a symlink on the way is followed
// only when root or the daemon's own user owns it (macOS /var is a root
// symlink), so a link another user planted fails the open instead of
// redirecting the write (decided 2026-11-19; os.Root rooted at "/" followed
// every relative link). The final component is opened with openat(2) and
// O_NOFOLLOW relative to that directory (SEC-012).
func openLogFile(path string, perm os.FileMode) (*os.File, error) {
	dir, base, err := openLogParent(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	flags := logFileOpenFlags | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(int(dir.Fd()), base, flags, uint32(perm.Perm()))
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// trustedLinkOwner reports whether a symlink owned by uid may be followed in
// a log path: root's and the daemon's own. Tests replace it.
var trustedLinkOwner = func(uid uint32) bool { return uid == 0 || int(uid) == os.Geteuid() }

// maxLogLinks bounds the symlinks followed while resolving one log path.
const maxLogLinks = 40

// openLogParent opens the directory that holds path's final component and
// returns it with that component's name. See openLogFile.
func openLogParent(path string) (*os.File, string, error) {
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	// A log path must name a file, not a directory root or a traversal element.
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return nil, "", &os.PathError{Op: "open", Path: path, Err: unix.EISDIR}
	}
	start := "."
	if filepath.IsAbs(clean) {
		start = "/"
	}
	fd, err := unix.Open(start, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", &os.PathError{Op: "open", Path: start, Err: err}
	}
	comps := splitPath(filepath.Dir(clean))
	links := 0
	for len(comps) > 0 {
		c := comps[0]
		comps = comps[1:]
		next, err := unix.Openat(fd, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			_ = unix.Close(fd)
			fd = next
			continue
		}
		var st unix.Stat_t
		if serr := unix.Fstatat(fd, c, &st, unix.AT_SYMLINK_NOFOLLOW); serr != nil || st.Mode&unix.S_IFMT != unix.S_IFLNK {
			_ = unix.Close(fd)
			return nil, "", &os.PathError{Op: "open", Path: path, Err: err}
		}
		if !trustedLinkOwner(st.Uid) {
			_ = unix.Close(fd)
			return nil, "", &os.PathError{Op: "open", Path: path, Err: fmt.Errorf("symlink %s is owned by uid %d; only root or the daemon's user may own a symlink in a log path", c, st.Uid)}
		}
		if links++; links > maxLogLinks {
			_ = unix.Close(fd)
			return nil, "", &os.PathError{Op: "open", Path: path, Err: unix.ELOOP}
		}
		target, err := readlinkat(fd, c)
		if err != nil {
			_ = unix.Close(fd)
			return nil, "", &os.PathError{Op: "readlink", Path: path, Err: err}
		}
		if filepath.IsAbs(target) {
			root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				_ = unix.Close(fd)
				return nil, "", &os.PathError{Op: "open", Path: "/", Err: err}
			}
			_ = unix.Close(fd)
			fd = root
		}
		comps = append(splitPath(target), comps...)
	}
	return os.NewFile(uintptr(fd), filepath.Dir(clean)), base, nil
}

// splitPath returns the components of p, without empty and "." entries.
func splitPath(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

func readlinkat(dirfd int, name string) (string, error) {
	for size := 256; ; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(dirfd, name, buf)
		if err != nil {
			return "", err
		}
		if n < size {
			return string(buf[:n]), nil
		}
	}
}

// probeLogFile reports whether openLogFile(path) would fail, without
// creating or opening the file (FUNC-087): the parent is resolved by the
// same walk (openLogParent; directories are opened read-only), an existing file
// must not be a symlink or directory and must be writable, a missing one
// needs a writable parent.
func probeLogFile(path string) error {
	dirFile, base, err := openLogParent(path)
	if err != nil {
		return err
	}
	defer func() { _ = dirFile.Close() }()
	dfd := int(dirFile.Fd())

	var st unix.Stat_t
	err = unix.Fstatat(dfd, base, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case err == unix.ENOENT:
		err = unix.Faccessat(dfd, ".", unix.W_OK|unix.X_OK, 0)
	case err != nil:
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		err = unix.ELOOP
	case st.Mode&unix.S_IFMT == unix.S_IFDIR:
		err = unix.EISDIR
	default:
		err = unix.Faccessat(dfd, base, unix.W_OK, 0)
	}
	if err != nil {
		return &os.PathError{Op: "openat", Path: path, Err: err}
	}
	return nil
}

// shiftBackups rotates path: the oldest backup .backups is removed, .N
// becomes .N+1 and the file becomes .1. The names are changed relative to
// the directory openLogParent verified, so a parent swapped for a symlink
// cannot move or delete files elsewhere (SEC-019).
func shiftBackups(path string, backups int) error {
	dir, base, err := openLogParent(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	dfd := int(dir.Fd())
	_ = unix.Unlinkat(dfd, fmt.Sprintf("%s.%d", base, backups), 0)
	// Missing intermediates are expected.
	for i := backups - 1; i >= 1; i-- {
		_ = unix.Renameat(dfd, fmt.Sprintf("%s.%d", base, i), dfd, fmt.Sprintf("%s.%d", base, i+1))
	}
	if err := unix.Renameat(dfd, base, dfd, base+".1"); err != nil {
		return &os.LinkError{Op: "rename", Old: path, New: path + ".1", Err: err}
	}
	return nil
}
