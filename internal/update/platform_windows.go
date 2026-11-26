//go:build windows

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func detectMethod(exe string) string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		installed := filepath.Join(base, "Programs", "Magnetar", "magnetar.exe")
		if strings.EqualFold(filepath.Clean(exe), filepath.Clean(installed)) {
			return MethodWindowsSetup
		}
	}
	if dirWritable(filepath.Dir(exe)) {
		return MethodWindowsPortable
	}
	return MethodUnsupported
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".magnetar-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// applySetup runs the downloaded installer silently; it waits for this
// process to exit, installs the new version and starts it again.
func (u *Updater) applySetup(setup string) error {
	if err := startDetached(setup, "/S", "/launch"); err != nil {
		return err
	}
	u.log.Info("installer started, shutting down for update")
	go func() { time.Sleep(300 * time.Millisecond); u.restart() }()
	return nil
}

func (u *Updater) applyDeb(string) error { return errUnsupported }

func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return cmd.Start()
}
