//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// register associates magnet: links and .torrent files with Magnetar for the
// current user (HKCU, no administrator rights needed).
func register() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	cmd := fmt.Sprintf(`"%s" "%%1"`, exe)
	icon := fmt.Sprintf(`"%s",0`, exe)

	set := func(path string, values map[string]string) error {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer k.Close()
		for name, v := range values {
			if err := k.SetStringValue(name, v); err != nil {
				return err
			}
		}
		return nil
	}
	// Remember the previous magnet handler (e.g. another torrent client) so
	// unregister can restore it.
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\magnet\shell\open\command`, registry.QUERY_VALUE); err == nil {
		if prev, _, err := k.GetStringValue(""); err == nil && prev != cmd && !strings.Contains(strings.ToLower(prev), "magnetar") {
			set(`Software\Magnetar`, map[string]string{"PreviousMagnetHandler": prev})
		}
		k.Close()
	}
	steps := []struct {
		path   string
		values map[string]string
	}{
		{`Software\Classes\magnet`, map[string]string{"": "URL:Magnet link", "URL Protocol": ""}},
		{`Software\Classes\magnet\DefaultIcon`, map[string]string{"": icon}},
		{`Software\Classes\magnet\shell\open\command`, map[string]string{"": cmd}},
		{`Software\Classes\Magnetar.Torrent`, map[string]string{"": "BitTorrent file"}},
		{`Software\Classes\Magnetar.Torrent\DefaultIcon`, map[string]string{"": icon}},
		{`Software\Classes\Magnetar.Torrent\shell\open\command`, map[string]string{"": cmd}},
		{`Software\Classes\.torrent`, map[string]string{"": "Magnetar.Torrent", "Content Type": "application/x-bittorrent"}},
		{`Software\Classes\.torrent\OpenWithProgids`, map[string]string{"Magnetar.Torrent": ""}},
		// ProgID for the magnet: protocol, referenced by the Capabilities below.
		{`Software\Classes\Magnetar.Magnet`, map[string]string{"": "Magnet link", "URL Protocol": ""}},
		{`Software\Classes\Magnetar.Magnet\DefaultIcon`, map[string]string{"": icon}},
		{`Software\Classes\Magnetar.Magnet\shell\open\command`, map[string]string{"": cmd}},
		// Registered application: makes Magnetar appear by name in "Pick an app"
		// and Settings → Default apps.
		{`Software\Magnetar\Capabilities`, map[string]string{
			"ApplicationName":        "Magnetar",
			"ApplicationDescription": "Fast, secure BitTorrent client",
			"ApplicationIcon":        icon,
		}},
		{`Software\Magnetar\Capabilities\URLAssociations`, map[string]string{"magnet": "Magnetar.Magnet"}},
		{`Software\Magnetar\Capabilities\FileAssociations`, map[string]string{".torrent": "Magnetar.Torrent"}},
		{`Software\RegisteredApplications`, map[string]string{"Magnetar": `Software\Magnetar\Capabilities`}},
	}
	for _, s := range steps {
		if err := set(s.path, s.values); err != nil {
			return err
		}
	}
	// Tell Explorer that associations changed.
	shell32 := syscall.NewLazyDLL("shell32.dll")
	shell32.NewProc("SHChangeNotify").Call(0x08000000, 0, 0, 0) // SHCNE_ASSOCCHANGED
	fmt.Println("Magnetar is now the handler for magnet links and .torrent files.")
	if userChoiceElsewhere() {
		// Windows protects the user's default-app choice; open the page where
		// they can pick Magnetar with one click.
		openDefaultAppsSettings()
	}
	return nil
}

// userChoiceElsewhere reports whether Windows has a protected per-user choice
// (UserChoice) for magnet links that points to another application.
func userChoiceElsewhere() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\magnet\UserChoice`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	progID, _, _ := k.GetStringValue("ProgId")
	return progID != "" && progID != "Magnetar.Magnet"
}

func openDefaultAppsSettings() {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", "ms-settings:defaultapps?registeredAppUser=Magnetar").Start()
}

func unregister() error {
	var prev string
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Magnetar`, registry.QUERY_VALUE); err == nil {
		prev, _, _ = k.GetStringValue("PreviousMagnetHandler")
		k.Close()
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.SET_VALUE); err == nil {
		k.DeleteValue("Magnetar")
		k.Close()
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\.torrent\OpenWithProgids`, registry.SET_VALUE); err == nil {
		k.DeleteValue("Magnetar.Torrent")
		k.Close()
	}
	for _, p := range []string{
		`Software\Magnetar\Capabilities\URLAssociations`, `Software\Magnetar\Capabilities\FileAssociations`, `Software\Magnetar\Capabilities`,
		`Software\Classes\Magnetar.Magnet\shell\open\command`, `Software\Classes\Magnetar.Magnet\shell\open`,
		`Software\Classes\Magnetar.Magnet\shell`, `Software\Classes\Magnetar.Magnet\DefaultIcon`, `Software\Classes\Magnetar.Magnet`,
		`Software\Classes\magnet\shell\open\command`, `Software\Classes\magnet\shell\open`, `Software\Classes\magnet\shell`,
		`Software\Classes\magnet\DefaultIcon`, `Software\Classes\magnet`,
		`Software\Classes\Magnetar.Torrent\shell\open\command`, `Software\Classes\Magnetar.Torrent\shell\open`,
		`Software\Classes\Magnetar.Torrent\shell`, `Software\Classes\Magnetar.Torrent\DefaultIcon`, `Software\Classes\Magnetar.Torrent`,
	} {
		registry.DeleteKey(registry.CURRENT_USER, p)
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\.torrent`, registry.QUERY_VALUE|registry.SET_VALUE); err == nil {
		if v, _, _ := k.GetStringValue(""); v == "Magnetar.Torrent" {
			k.DeleteValue("")
			k.DeleteValue("Content Type")
		}
		k.Close()
	}
	// Remove keys that are now empty so nothing is left behind.
	for _, p := range []string{`Software\Classes\.torrent\OpenWithProgids`, `Software\Classes\.torrent`} {
		if isEmptyKey(p) {
			registry.DeleteKey(registry.CURRENT_USER, p)
		}
	}
	if prev != "" {
		if k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\magnet\shell\open\command`, registry.SET_VALUE); err == nil {
			k.SetStringValue("", prev)
			k.Close()
		}
		if k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\magnet`, registry.SET_VALUE); err == nil {
			k.SetStringValue("", "URL:Magnet link")
			k.SetStringValue("URL Protocol", "")
			k.Close()
		}
	}
	registry.DeleteKey(registry.CURRENT_USER, `Software\Magnetar`)
	fmt.Println("Removed Magnetar file associations.")
	return nil
}

func isEmptyKey(path string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return false
	}
	defer k.Close()
	info, err := k.Stat()
	return err == nil && info.ValueCount == 0 && info.SubKeyCount == 0
}
