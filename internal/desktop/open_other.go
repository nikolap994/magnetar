//go:build !windows

package desktop

import (
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/godbus/dbus/v5"
)

// OpenPath opens a file or folder with its default application.
func OpenPath(p string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	cmd := exec.Command(opener, p)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// RevealPath shows a file selected in the file manager (freedesktop
// FileManager1 interface), or opens its folder.
func RevealPath(p string) error {
	if conn, err := dbus.SessionBus(); err == nil {
		u := url.URL{Scheme: "file", Path: p}
		call := conn.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1").
			Call("org.freedesktop.FileManager1.ShowItems", 0, []string{u.String()}, "")
		if call.Err == nil {
			return nil
		}
	}
	return OpenPath(filepath.Dir(p))
}
