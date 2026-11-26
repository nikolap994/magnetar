//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
)

// SetAutostart starts Magnetar (without opening a window) when the user signs
// in, using an XDG autostart entry.
func SetAutostart(enable bool) error {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".config")
	}
	path := filepath.Join(dir, "autostart", "magnetar.desktop")
	if !enable {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Magnetar\nExec=%q --no-browser\nIcon=magnetar\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", exe)
	return os.WriteFile(path, []byte(entry), 0o644)
}
