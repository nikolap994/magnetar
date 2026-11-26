//go:build windows

package desktop

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procIsIconic                 = user32.NewProc("IsIconic")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")

	focusMu    sync.Mutex
	focusPIDs  map[uint32]bool
	focusFound uintptr
	// Windows limits how many callbacks a process can create, so make one.
	enumCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if !focusPIDs[pid] {
			return 1
		}
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		titled, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if visible != 0 && titled > 0 {
			focusFound = hwnd
			return 0 // stop
		}
		return 1
	})
)

// FocusWindow brings Magnetar's app window to the front, restoring it if it
// is minimised. It reports whether a window was found.
func FocusWindow() bool {
	focusMu.Lock()
	defer focusMu.Unlock()
	focusPIDs = windowPIDs()
	focusFound = 0
	if len(focusPIDs) == 0 {
		return false
	}
	procEnumWindows.Call(enumCallback, 0)
	if focusFound == 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(focusFound); iconic != 0 {
		procShowWindow.Call(focusFound, 9) // SW_RESTORE
	}
	procSetForegroundWindow.Call(focusFound)
	return true
}

// AllowFocus lets the running Magnetar bring its window to the front. A
// process started by the user (e.g. for a magnet link) may grant this.
func AllowFocus() {
	procAllowSetForegroundWindow.Call(^uintptr(0)) // ASFW_ANY
}
