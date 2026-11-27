//go:build windows

package desktop

import (
	"os/exec"
	"strings"
	"syscall"
)

// OpenPath opens a file or folder with its default application.
func OpenPath(p string) error {
	return startExplorer(`explorer.exe "` + p + `"`)
}

// RevealPath shows a file selected in Explorer.
func RevealPath(p string) error {
	return startExplorer(`explorer.exe /select,"` + p + `"`)
}

func startExplorer(cmdline string) error {
	if strings.ContainsAny(cmdline[len("explorer.exe"):], "\x00\n\r") {
		return syscall.EINVAL
	}
	cmd := exec.Command("explorer.exe")
	// Windows paths cannot contain quotes, so quoting them is safe; this keeps
	// explorer's unusual /select, syntax intact.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
