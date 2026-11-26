//go:build windows

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const appUserModelID = "Magnetar.BitTorrent"

var registerAUMID sync.Once

// ensureAppID registers Magnetar's AppUserModelID so toasts show its name
// and icon (supported for unpackaged apps since Windows 10 1903).
func ensureAppID() {
	registerAUMID.Do(func() {
		icon := filepath.Join(os.Getenv("LOCALAPPDATA"), "Magnetar", "notification-icon.png")
		if err := os.MkdirAll(filepath.Dir(icon), 0o755); err == nil {
			os.WriteFile(icon, iconLargePNG, 0o644)
		}
		k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+appUserModelID, registry.SET_VALUE)
		if err != nil {
			return
		}
		defer k.Close()
		k.SetStringValue("DisplayName", "Magnetar")
		k.SetStringValue("IconUri", icon)
	})
}

// The toast text is passed through environment variables and escaped in
// PowerShell, so torrent names can never inject markup or commands.
const toastScript = `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$t = [Security.SecurityElement]::Escape($env:MG_TITLE)
$b = [Security.SecurityElement]::Escape($env:MG_BODY)
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml("<toast><visual><binding template='ToastGeneric'><text>$t</text><text>$b</text></binding></visual></toast>")
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:MG_APPID).Show([Windows.UI.Notifications.ToastNotification]::new($xml))`

// Notify shows a native Windows toast notification.
func Notify(title, body string) {
	ensureAppID()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", toastScript)
	cmd.Env = append(os.Environ(), "MG_TITLE="+title, "MG_BODY="+body, "MG_APPID="+appUserModelID)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	go cmd.Run()
}
