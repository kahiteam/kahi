package supervisor

import "golang.org/x/sys/unix"

// maxFilesPerProc returns kern.maxfilesperproc, the macOS cap on NOFILE.
func maxFilesPerProc() (uint64, bool) {
	v, err := unix.SysctlUint32("kern.maxfilesperproc")
	if err != nil {
		return 0, false
	}
	return uint64(v), true
}
