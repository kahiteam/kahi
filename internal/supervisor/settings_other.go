//go:build !darwin

package supervisor

// maxFilesPerProc applies only to macOS.
func maxFilesPerProc() (uint64, bool) { return 0, false }
