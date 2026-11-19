//go:build !unix

package logging

import (
	"os"
	"path/filepath"
)

// openLogFile opens path with logFileOpenFlags.
//
// SEC-019 / ADR-022: platforms without openat(2)/O_NOFOLLOW per-component
// semantics cannot cheaply walk the path refusing every symlinked component,
// so this fallback keeps only the SEC-012 final-component guard (the
// O_NOFOLLOW carried in logFileOpenFlags). Residual trusted-path assumption:
// the parent directories of a configured log path must not be attacker
// controlled, because a swapped parent-directory symlink is not detected here.
func openLogFile(path string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, logFileOpenFlags, perm)
}

// probeLogFile reports whether openLogFile(path) would fail, without
// creating the file, as far as it can without access(2): an existing path
// must not be a directory, a missing one needs an existing parent
// directory. Write permission is not checked on these platforms (no
// release target uses this file).
func probeLogFile(path string) error {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
		}
		return nil
	}
	_, err := os.Stat(filepath.Dir(path))
	return err
}
