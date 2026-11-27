//go:build windows

package desktop

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	shell32El          = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteEx = shell32El.NewProc("ShellExecuteExW")
	kernel32El         = syscall.NewLazyDLL("kernel32.dll")
	procWaitForObject  = kernel32El.NewProc("WaitForSingleObject")
	procGetExitCode    = kernel32El.NewProc("GetExitCodeProcess")
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     uintptr
}

// RunElevated runs exe with args as administrator (one UAC prompt) and waits
// for it to finish. It returns an error if the user declines or the program
// fails.
func RunElevated(exe string, args ...string) error {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(joinArgs(args))
	info := shellExecuteInfo{
		fMask:        0x00000040, // SEE_MASK_NOCLOSEPROCESS
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		nShow:        0, // SW_HIDE
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, _ := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return errors.New("the firewall step did not start (you may have declined the Windows prompt)")
	}
	if info.hProcess == 0 {
		return nil
	}
	h := syscall.Handle(info.hProcess)
	defer func() { _ = syscall.CloseHandle(h) }()
	procWaitForObject.Call(info.hProcess, 0xFFFFFFFF) // INFINITE
	var code uint32
	procGetExitCode.Call(info.hProcess, uintptr(unsafe.Pointer(&code)))
	if code != 0 {
		return errors.New("the elevated step did not complete")
	}
	return nil
}

func joinArgs(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += syscall.EscapeArg(a)
	}
	return s
}
