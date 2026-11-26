//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	procGetConsole   = kernel32.NewProc("GetConsoleWindow")
	procAttachCon    = kernel32.NewProc("AttachConsole")
	procMessageBoxW  = user32.NewProc("MessageBoxW")
	attachParentProc = ^uintptr(0) // ATTACH_PARENT_PROCESS
)

func init() {
	// Release builds use the GUI subsystem (no console window on double-click).
	// When started from a terminal, attach to it so flags like --version print.
	if w, _, _ := procGetConsole.Call(); w == 0 {
		if r, _, _ := procAttachCon.Call(attachParentProc); r != 0 {
			if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
				os.Stdout, os.Stderr = f, f
			}
		}
	}
}

func hasConsole() bool {
	w, _, _ := procGetConsole.Call()
	return w != 0
}

func showFatal(err error) {
	if hasConsole() {
		return
	}
	title, _ := syscall.UTF16PtrFromString("Magnetar")
	msg, _ := syscall.UTF16PtrFromString(err.Error())
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), 0x10)
}
