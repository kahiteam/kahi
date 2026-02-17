//go:build linux && arm64

package supervisor

import "syscall"

func sysFork() (uintptr, syscall.Errno) {
	pid, _, errno := syscall.RawSyscall(syscall.SYS_CLONE, uintptr(syscall.SIGCHLD), 0, 0)
	return pid, errno
}

func sysDup2(oldfd, newfd int) error {
	return syscall.Dup3(oldfd, newfd, 0)
}
