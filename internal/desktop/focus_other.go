//go:build !windows

package desktop

import "os/exec"

// FocusWindow brings Magnetar's app window to the front using wmctrl or
// xdotool when available (X11 and XWayland). It reports success.
func FocusWindow() bool {
	if len(windowPIDs()) == 0 {
		return false
	}
	if p, err := exec.LookPath("wmctrl"); err == nil {
		if exec.Command(p, "-x", "-a", "Magnetar").Run() == nil {
			return true
		}
	}
	if p, err := exec.LookPath("xdotool"); err == nil {
		if exec.Command(p, "search", "--classname", "Magnetar", "windowactivate").Run() == nil {
			return true
		}
	}
	return false
}

// AllowFocus is only needed on Windows.
func AllowFocus() {}
