// Package desktop integrates Magnetar with the desktop: an app window for the
// UI, a tray icon, and opening URLs.
package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Window sizes for the main UI and the "add torrent" popup used for magnet links.
const (
	MainSize  = "1320,840"
	PopupSize = "720,700"
)

// OpenWindow shows url in a standalone app window (Edge/Chromium "app" mode,
// using a private profile so browser extensions cannot see the UI). It falls
// back to the default browser when no Chromium-based browser is available.
func OpenWindow(url, profileDir, size string, appWindow bool) error {
	if appWindow {
		if bin := chromiumBinary(); bin != "" {
			args := []string{
				"--app=" + url,
				"--user-data-dir=" + profileDir,
				"--window-size=" + size,
				"--no-first-run",
				"--no-default-browser-check",
				"--disable-extensions",
				"--disable-sync",
				"--disable-background-networking",
				"--class=Magnetar",
			}
			cmd := exec.Command(bin, args...)
			if err := cmd.Start(); err == nil {
				go cmd.Wait()
				return nil
			}
		}
	}
	return OpenBrowser(url)
}

func chromiumBinary() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				candidates = append(candidates,
					filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
					filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
				)
			}
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c
			}
		}
		return ""
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	default:
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "microsoft-edge-stable", "microsoft-edge", "brave-browser", "vivaldi-stable"} {
			if p, err := exec.LookPath(name); err == nil {
				// Snap-packaged Chromium cannot use a profile outside ~/snap; skip it.
				if strings.HasPrefix(p, "/snap/") {
					continue
				}
				return p
			}
		}
		return ""
	}
}

// OpenBrowser opens url in the default browser without invoking a shell.
func OpenBrowser(url string) error {
	var cmds [][]string
	switch runtime.GOOS {
	case "windows":
		cmds = [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	case "darwin":
		cmds = [][]string{{"open", url}}
	default:
		if isWSL() {
			cmds = append(cmds, []string{"wslview", url}, []string{"rundll32.exe", "url.dll,FileProtocolHandler", url})
		}
		cmds = append(cmds, []string{"xdg-open", url}, []string{"gio", "open", url}, []string{"sensible-browser", url})
	}
	var err error
	for _, c := range cmds {
		path, lerr := exec.LookPath(c[0])
		if lerr != nil {
			err = lerr
			continue
		}
		cmd := exec.Command(path, c[1:]...)
		if err = cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	return err
}

func isWSL() bool {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
