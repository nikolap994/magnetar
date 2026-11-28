//go:build windows

package desktop

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows/registry"
)

const webView2Ready = "MAGNETAR_WEBVIEW2_READY"

var (
	procFindWindowW           = user32.NewProc("FindWindowW")
	procDwmSetWindowAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

func openWebView2Window(target, profileDir, size string) (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	cmd := exec.Command(exe, "webview2-window", target, profileDir, size)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return false, err
	}

	ready := make(chan string, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		if readErr != nil {
			ready <- fmt.Sprintf("WebView2 helper exited before startup: %v", readErr)
			return
		}
		ready <- line
	}()

	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case message := <-ready:
		<-readDone
		if message == webView2Ready+"\n" {
			track(cmd)
			return true, nil
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return false, errors.New(strings.TrimSpace(message))
	case <-timer.C:
		_ = cmd.Process.Kill()
		_ = stdout.Close()
		<-readDone
		_ = cmd.Wait()
		return false, errors.New("timed out waiting for the WebView2 runtime")
	}
}

// RunWebView2Window runs the isolated WebView2 host command on Windows.
func RunWebView2Window(target, profileDir, size string) error {
	u, err := url.ParseRequestURI(target)
	if err != nil || u.Scheme != "http" {
		return errors.New("WebView2 only accepts Magnetar's local HTTP URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("WebView2 only accepts Magnetar's local HTTP URL")
	}
	if profileDir == "" {
		return errors.New("WebView2 profile directory is required")
	}
	width, height := 1320, 840
	if w, h, ok := strings.Cut(size, ","); ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(w)); err == nil && parsed > 0 {
			width = parsed
		}
		if parsed, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && parsed > 0 {
			height = parsed
		}
	}
	dataPath := filepath.Join(profileDir, "webview2")
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		return fmt.Errorf("create WebView2 profile: %w", err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	view := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "Magnetar",
			IconId: 1,
			Width:  uint(width),
			Height: uint(height),
			Center: true,
		},
	})
	if view == nil {
		return errors.New("create WebView2 window")
	}
	if err := applyModernWindowChrome(); err != nil {
		fmt.Fprintf(os.Stderr, "magnetar: apply Windows window styling: %v\n", err)
	}
	view.SetTitle("Magnetar")
	if err := view.Bind("magnetarOpenExternal", openExternalURL); err != nil {
		view.Destroy()
		return fmt.Errorf("bind external link handler: %w", err)
	}
	view.Init(`(() => {
  const openExternal = (raw) => {
    let url;
    try { url = new URL(raw, location.href); } catch { return; }
    if (url.protocol !== "http:" && url.protocol !== "https:") return;
    if (url.origin === location.origin) return;
    window.magnetarOpenExternal(url.href).catch((error) => console.error("Magnetar could not open link:", error));
  };
  document.addEventListener("click", (event) => {
    if (event.defaultPrevented || event.button !== 0) return;
    const anchor = event.target instanceof Element ? event.target.closest("a[href]") : null;
    if (!anchor || anchor.hasAttribute("download")) return;
    const url = new URL(anchor.href, location.href);
    if (url.origin === location.origin || (url.protocol !== "http:" && url.protocol !== "https:")) return;
    event.preventDefault();
    openExternal(url.href);
  }, true);
  const nativeOpen = window.open.bind(window);
  window.open = (raw, target, features) => {
    const url = new URL(raw, location.href);
    if (url.origin !== location.origin && (url.protocol === "http:" || url.protocol === "https:")) {
      openExternal(url.href);
      return null;
    }
    return nativeOpen(raw, target, features);
  };
  // No page zoom, so the window behaves like a native app, not a browser tab.
  window.addEventListener("wheel", (event) => { if (event.ctrlKey) event.preventDefault(); }, { passive: false });
  window.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && ["=", "-", "+", "0"].includes(event.key)) event.preventDefault();
  });
})()`)
	view.Navigate(target)
	if _, err := fmt.Fprintln(os.Stdout, webView2Ready); err != nil {
		view.Destroy()
		return fmt.Errorf("signal WebView2 startup: %w", err)
	}
	view.Run()
	return nil
}

func openExternalURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("invalid external link: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errors.New("external links must use HTTP or HTTPS")
	}
	if err := OpenBrowser(u.String()); err != nil {
		return fmt.Errorf("open external link: %w", err)
	}
	return nil
}

func applyModernWindowChrome() error {
	className, _ := syscall.UTF16PtrFromString("webview")
	title, _ := syscall.UTF16PtrFromString("Magnetar")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)))
	runtime.KeepAlive(className)
	runtime.KeepAlive(title)
	if hwnd == 0 {
		return errors.New("find WebView2 window")
	}
	var pid uint32
	if threadID, _, _ := procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid))); threadID == 0 || pid != uint32(os.Getpid()) {
		return errors.New("WebView2 window belongs to another process")
	}

	build, err := windowsBuildNumber()
	if err != nil {
		return err
	}
	lightTheme, err := windowsUsesLightTheme()
	if err != nil {
		return err
	}
	dark := uint32(0)
	if !lightTheme {
		dark = 1
	}
	darkAttribute := uint32(19)
	if build >= 22000 {
		darkAttribute = 20
	}
	if err := setDWMWindowAttribute(hwnd, darkAttribute, dark); err != nil {
		return err
	}
	if build >= 22000 {
		if err := setDWMWindowAttribute(hwnd, 33, 2); err != nil { // DWMWA_WINDOW_CORNER_PREFERENCE: round
			return err
		}
	}
	if build >= 22621 {
		if err := setDWMWindowAttribute(hwnd, 38, 2); err != nil { // DWMWA_SYSTEMBACKDROP_TYPE: main window (Mica)
			return err
		}
	}
	return nil
}

func setDWMWindowAttribute(hwnd uintptr, attribute, value uint32) error {
	result, _, _ := procDwmSetWindowAttribute.Call(
		hwnd,
		uintptr(attribute),
		uintptr(unsafe.Pointer(&value)),
		unsafe.Sizeof(value),
	)
	runtime.KeepAlive(&value)
	if int32(result) < 0 {
		return fmt.Errorf("set DWM window attribute %d: HRESULT 0x%08x", attribute, uint32(result))
	}
	return nil
}

func windowsBuildNumber() (int, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return 0, fmt.Errorf("read Windows build number: %w", err)
	}
	defer k.Close()
	build, _, err := k.GetStringValue("CurrentBuildNumber")
	if err != nil {
		return 0, fmt.Errorf("read Windows build number: %w", err)
	}
	n, err := strconv.Atoi(build)
	if err != nil {
		return 0, fmt.Errorf("parse Windows build number %q: %w", build, err)
	}
	return n, nil
}

func windowsUsesLightTheme() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Errorf("read Windows theme preference: %w", err)
	}
	defer k.Close()
	light, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false, fmt.Errorf("read Windows theme preference: %w", err)
	}
	return light != 0, nil
}
