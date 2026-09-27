//go:build windows

package skillsource

import "errors"

// mkfifo is not available on Windows; the directory special-file test skips before
// calling it.
func mkfifo(path string) error {
	return errors.New("named pipes are not constructible on Windows")
}
