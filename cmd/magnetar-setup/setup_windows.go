//go:build windows

package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	appName     = "Magnetar"
	publisher   = "Magnetar contributors"
	homepage    = "https://github.com/nikolap994/magnetar"
	uninstKey   = `Software\Microsoft\Windows\CurrentVersion\Uninstall\Magnetar`
	createNoWin = 0x08000000

	mbOK          = 0x0
	mbYesNo       = 0x4
	mbYesNoCancel = 0x3
	mbIconError   = 0x10
	mbIconInfo    = 0x40
	mbIconQ       = 0x20
	idYes         = 6
	idNo          = 7
)

var (
	user32     = syscall.NewLazyDLL("user32.dll")
	procMsgBox = user32.NewProc("MessageBoxW")
	shell32    = syscall.NewLazyDLL("shell32.dll")
	procNotify = shell32.NewProc("SHChangeNotify")
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	procMoveEx = kernel32.NewProc("MoveFileExW")
)

func message(title, text string, flags uint32) int {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMsgBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(flags|0x00010000)) // MB_SETFOREGROUND
	return int(r)
}

func installDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "Programs", appName), nil
}

func hidden(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWin}
	return cmd
}

// stopRunning waits for a running Magnetar to exit (the updater shuts it down
// gracefully so the session is saved) and force-closes it after 15 seconds.
func stopRunning() {
	for range 30 {
		out, err := hidden("tasklist", "/FI", "IMAGENAME eq magnetar.exe", "/NH").Output()
		if err != nil || !strings.Contains(strings.ToLower(string(out)), "magnetar.exe") {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	hidden("taskkill", "/IM", "magnetar.exe", "/F").Run()
	time.Sleep(700 * time.Millisecond)
}

func install(silent, launch bool) error {
	exe, err := readPayload()
	if err != nil {
		return err
	}
	dir, err := installDir()
	if err != nil {
		return err
	}
	associate := true
	if !silent {
		switch message("Install "+appName+" "+Version,
			fmt.Sprintf("%s %s will be installed for your account in:\n%s\n\n"+
				"Also open magnet links and .torrent files with Magnetar?\n\n"+
				"Yes - register Magnetar for magnet links and .torrent files (recommended)\n"+
				"No - install only\nCancel - do not install", appName, Version, dir),
			mbYesNoCancel|mbIconQ) {
		case idYes:
		case idNo:
			associate = false
		default:
			return nil
		}
	}

	stopRunning()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, "magnetar.exe")
	if err := writeFile(target, exe); err != nil {
		return fmt.Errorf("copy magnetar.exe: %w", err)
	}
	if self, err := os.Executable(); err == nil {
		if err := copyFile(self, filepath.Join(dir, "uninstall.exe")); err != nil {
			return fmt.Errorf("write uninstaller: %w", err)
		}
	}
	if lic, err := payload.ReadFile("payload/LICENSE.txt"); err == nil {
		writeFile(filepath.Join(dir, "LICENSE.txt"), lic)
	}

	if err := writeUninstallEntry(dir, int64(len(exe))); err != nil {
		return err
	}
	// On an update (started by the in-app updater), only refresh shortcuts the
	// user still has: a desktop icon they deleted stays deleted.
	if err := shortcuts(target, true, launch); err != nil && !silent {
		message(appName, "Could not create shortcuts: "+err.Error(), mbIconInfo)
	}
	// Updates (/launch) keep whatever default-app choice the user has made.
	if associate && !launch {
		if err := hidden(target, "register").Run(); err != nil {
			if silent {
				return fmt.Errorf("register magnet link and .torrent handlers: %w", err)
			}
			message(appName, fmt.Sprintf("Magnetar was installed, but couldn't register its file handlers:\n\n%v", err), mbIconError|mbOK)
		}
	}
	procNotify.Call(0x08000000, 0, 0, 0)

	done := appName + " is installed.\n\nLaunch it now?"
	if associate {
		done = appName + " is installed.\n\nWindows requires you to confirm protected defaults in Settings → Apps → " +
			"Default apps → Magnetar → Set default. If .torrent files still open with qBittorrent, choose Magnetar for " +
			"the .torrent file type in Default Apps.\n\nLaunch it now?"
	}
	if launch || (!silent && message(appName, done, mbYesNo|mbIconInfo) == idYes) {
		exec.Command(target).Start()
	}
	return nil
}

func writeFile(path string, data []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	os.Remove(path)
	return os.Rename(tmp, path)
}

func copyFile(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	b, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return writeFile(dst, b)
}

func writeUninstallEntry(dir string, size int64) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	uninst := filepath.Join(dir, "uninstall.exe")
	for name, v := range map[string]string{
		"DisplayName":          appName,
		"DisplayVersion":       Version,
		"Publisher":            publisher,
		"DisplayIcon":          filepath.Join(dir, "magnetar.exe") + ",0",
		"InstallLocation":      dir,
		"URLInfoAbout":         homepage,
		"HelpLink":             homepage + "/issues",
		"UninstallString":      `"` + uninst + `" /uninstall`,
		"QuietUninstallString": `"` + uninst + `" /uninstall /S`,
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	k.SetDWordValue("NoModify", 1)
	k.SetDWordValue("NoRepair", 1)
	k.SetDWordValue("EstimatedSize", uint32(size/1024))
	return nil
}

// shortcuts creates or removes the Start Menu and desktop shortcuts using the
// Windows Script Host COM object via PowerShell (arguments are passed as
// environment variables, never interpolated into the script).
func shortcuts(target string, create, refreshOnly bool) error {
	appData := os.Getenv("APPDATA")
	profile := os.Getenv("USERPROFILE")
	links := []string{filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs`, appName+".lnk")}
	if desktop, err := knownDesktop(); err == nil {
		links = append(links, filepath.Join(desktop, appName+".lnk"))
	} else if profile != "" {
		links = append(links, filepath.Join(profile, "Desktop", appName+".lnk"))
	}
	if !create {
		for _, l := range links {
			os.Remove(l)
		}
		return nil
	}
	if refreshOnly {
		kept := links[:0]
		for _, l := range links {
			if _, err := os.Stat(l); err == nil {
				kept = append(kept, l)
			}
		}
		links = kept
		if len(links) == 0 {
			return nil
		}
	}
	const script = `$s = New-Object -ComObject WScript.Shell
foreach ($p in $env:MG_LINKS.Split('|')) {
  $l = $s.CreateShortcut($p)
  $l.TargetPath = $env:MG_TARGET
  $l.WorkingDirectory = Split-Path $env:MG_TARGET
  $l.IconLocation = $env:MG_TARGET + ',0'
  $l.Description = 'Magnetar BitTorrent client'
  $l.Save()
}`
	cmd := hidden("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "MG_LINKS="+strings.Join(links, "|"), "MG_TARGET="+target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func knownDesktop() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Desktop")
	if err != nil {
		return "", err
	}
	return registry.ExpandString(v)
}

// startUninstall copies the uninstaller to %TEMP% and runs it from there so
// the install folder (which contains this executable) can be deleted.
func startUninstall(silent bool) error {
	dir, err := installDir()
	if err != nil {
		return err
	}
	if !silent && message("Uninstall "+appName,
		"Remove Magnetar from this computer?\n\nYour downloads and settings are kept.", mbYesNo|mbIconQ) != idYes {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("magnetar-uninstall-%d.exe", time.Now().UnixNano()))
	if err := copyFile(self, tmp); err != nil {
		return err
	}
	args := []string{"/uninstall-run", dir}
	if silent {
		args = append(args, "/S")
	}
	// Exit right away so this executable (inside the install folder) is no
	// longer locked when the temporary copy deletes the folder.
	return exec.Command(tmp, args...).Start()
}

func runUninstall(dir string, silent bool) error {
	time.Sleep(500 * time.Millisecond) // let the launcher exit
	expected, err := installDir()
	if err != nil {
		return err
	}
	// Only ever remove our own install folder.
	if !strings.EqualFold(filepath.Clean(dir), filepath.Clean(expected)) {
		return fmt.Errorf("refusing to uninstall from unexpected folder %q", dir)
	}
	stopRunning()
	exe := filepath.Join(dir, "magnetar.exe")
	if _, err := os.Stat(exe); err == nil {
		hidden(exe, "unregister").Run()
	}
	shortcuts(exe, false, false)
	// Remove autostart entry if the user enabled it.
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		k.DeleteValue(appName)
		k.Close()
	}
	registry.DeleteKey(registry.CURRENT_USER, uninstKey)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Magnetar`)
	// Notification app identity and icon registered by Magnetar.
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\Magnetar.BitTorrent`)
	os.RemoveAll(filepath.Join(os.Getenv("LOCALAPPDATA"), "Magnetar"))

	var lastErr error
	for range 30 {
		if lastErr = os.RemoveAll(dir); lastErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	procNotify.Call(0x08000000, 0, 0, 0)
	// Delete this temporary copy on next reboot.
	if self, err := os.Executable(); err == nil {
		p, _ := syscall.UTF16PtrFromString(self)
		procMoveEx.Call(uintptr(unsafe.Pointer(p)), 0, 4) // MOVEFILE_DELAY_UNTIL_REBOOT
	}
	if lastErr != nil {
		return lastErr
	}
	if !silent {
		message(appName, "Magnetar was removed.", mbOK|mbIconInfo)
	}
	return nil
}

// readPayload returns the embedded, gzip-compressed magnetar.exe.
func readPayload() ([]byte, error) {
	f, err := payload.Open("payload/magnetar.exe.gz")
	if err != nil {
		return nil, errors.New("this installer was built without magnetar.exe")
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, 256<<20))
}

var _ fs.FS = payload
