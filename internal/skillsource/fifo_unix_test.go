//go:build !windows

package skillsource

import "syscall"

// mkfifo creates a named pipe at path, for the directory special-file test.
func mkfifo(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
