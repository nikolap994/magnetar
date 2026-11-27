//go:build !windows

package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

func detectMethod(exe string) string {
	if runtime.GOOS != "linux" {
		return MethodUnsupported
	}
	if _, err := os.Stat("/.flatpak-info"); err == nil {
		return MethodFlatpak
	}
	if os.Getenv("MAGNETAR_CONTAINER") != "" {
		return MethodContainer
	}
	if exe == "/usr/bin/magnetar" {
		if _, err := os.Stat("/var/lib/dpkg/info/magnetar.list"); err == nil {
			return MethodDeb
		}
		return MethodUnsupported // installed by some other package manager
	}
	if dirWritable(filepath.Dir(exe)) {
		return MethodLinuxPortable
	}
	return MethodUnsupported
}

func dirWritable(dir string) bool {
	return syscall.Access(dir, 2 /* W_OK */) == nil
}

func (u *Updater) applySetup(string) error { return errUnsupported }

// applyDeb installs the verified package with apt through pkexec, which shows
// the desktop's normal password prompt.
func (u *Updater) applyDeb(deb string) error {
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		openFile(deb)
		return fmt.Errorf("could not ask for administrator rights; the verified package was opened for installation: %s", deb)
	}
	// apt needs to read the file as root-owned _apt user; make it world-readable.
	_ = os.Chmod(filepath.Dir(deb), 0o755) // apt runs as _apt and must read the package
	_ = os.Chmod(deb, 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, pkexec, "/usr/bin/apt-get", "install", "-y", "--allow-downgrades", deb).CombinedOutput()
	if err != nil {
		return fmt.Errorf("package installation failed: %v: %s", err, lastLine(out))
	}
	if err := startDetached("/usr/bin/magnetar", append([]string{"--after-update"}, RestartArgs...)...); err != nil {
		return fmt.Errorf("start new version: %w", err)
	}
	u.log.Info("package installed, restarting")
	go func() { time.Sleep(300 * time.Millisecond); u.restart() }()
	return nil
}

func lastLine(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return s[i+1:]
		}
	}
	return s
}

func openFile(p string) {
	if x, err := exec.LookPath("xdg-open"); err == nil {
		_ = exec.Command(x, p).Start()
	}
}

func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
