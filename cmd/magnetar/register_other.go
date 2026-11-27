//go:build !windows && !darwin

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nikolap994/magnetar/web"
)

const desktopEntry = `[Desktop Entry]
Type=Application
Name=Magnetar
GenericName=BitTorrent Client
Comment=Fast, secure BitTorrent client
Exec=%s %%U
Icon=magnetar
Terminal=false
Categories=Network;FileTransfer;P2P;
MimeType=x-scheme-handler/magnet;application/x-bittorrent;
Keywords=torrent;magnet;download;p2p;
StartupNotify=false
`

func dataHome() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// register installs a desktop entry and makes Magnetar the default handler
// for magnet links and .torrent files (freedesktop.org desktops).
func register() error {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		return errors.New("register is supported on Linux and Windows")
	}
	// Installed from the .deb package: the system desktop entry already exists,
	// so only make it the default handler for this user.
	if _, err := os.Stat("/usr/share/applications/magnetar.desktop"); err == nil {
		return setDefaultHandler()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	if strings.ContainsAny(exe, " \"'\\$`") {
		exe = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(exe) + `"`
	}
	dh, err := dataHome()
	if err != nil {
		return err
	}
	appDir := filepath.Join(dh, "applications")
	iconDir := filepath.Join(dh, "icons", "hicolor", "scalable", "apps")
	for _, d := range []string{appDir, iconDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	desktop := filepath.Join(appDir, "magnetar.desktop")
	if err := os.WriteFile(desktop, []byte(fmt.Sprintf(desktopEntry, exe)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iconDir, "magnetar.svg"), logoSVG(), 0o644); err != nil {
		return err
	}
	if p, err := exec.LookPath("update-desktop-database"); err == nil {
		_ = exec.Command(p, appDir).Run() // refreshes the desktop database; optional
	}
	fmt.Println("Installed", desktop)
	return setDefaultHandler()
}

// setDefaultHandler makes magnetar.desktop the user's default for magnet links
// and .torrent files, which is what browsers use via xdg-open.
func setDefaultHandler() error {
	p, err := exec.LookPath("xdg-mime")
	if err != nil {
		// Fall back to editing mimeapps.list directly.
		return writeMimeApps()
	}
	for _, mt := range []string{"x-scheme-handler/magnet", "application/x-bittorrent"} {
		if out, err := exec.Command(p, "default", "magnetar.desktop", mt).CombinedOutput(); err != nil {
			return fmt.Errorf("xdg-mime: %v: %s", err, out)
		}
	}
	fmt.Println("Magnetar is now the handler for magnet links and .torrent files.")
	return nil
}

func writeMimeApps() error {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cfg = filepath.Join(home, ".config")
	}
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		return err
	}
	path := filepath.Join(cfg, "mimeapps.list")
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	want := map[string]bool{"x-scheme-handler/magnet": true, "application/x-bittorrent": true}
	var out []string
	inDefaults, wrote := false, false
	emit := func() {
		// Keep entries inside the section, before any trailing blank lines.
		i := len(out)
		for i > 0 && strings.TrimSpace(out[i-1]) == "" {
			i--
		}
		tail := append([]string(nil), out[i:]...)
		out = out[:i]
		for _, mt := range []string{"x-scheme-handler/magnet", "application/x-bittorrent"} {
			out = append(out, mt+"=magnetar.desktop;")
		}
		out = append(out, tail...)
		wrote = true
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			if inDefaults && !wrote {
				emit()
			}
			inDefaults = t == "[Default Applications]"
		}
		if inDefaults {
			if k, _, ok := strings.Cut(t, "="); ok && want[k] {
				continue
			}
		}
		if l != "" || len(out) > 0 {
			out = append(out, l)
		}
	}
	if inDefaults && !wrote {
		emit()
	}
	if !wrote {
		out = append(out, "[Default Applications]")
		emit()
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Println("Magnetar is now the handler for magnet links and .torrent files.")
	return nil
}

func unregister() error {
	dh, err := dataHome()
	if err != nil {
		return err
	}
	os.Remove(filepath.Join(dh, "applications", "magnetar.desktop"))
	os.Remove(filepath.Join(dh, "icons", "hicolor", "scalable", "apps", "magnetar.svg"))
	fmt.Println("Removed Magnetar desktop entry.")
	return nil
}

func logoSVG() []byte {
	b, err := fs.ReadFile(web.FS(), "logo.svg")
	if err != nil {
		return nil
	}
	return b
}
