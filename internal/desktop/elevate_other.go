//go:build !windows

package desktop

import "errors"

// RunElevated is only implemented on Windows.
func RunElevated(exe string, args ...string) error {
	return errors.New("not supported on this system")
}
