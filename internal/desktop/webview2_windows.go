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
	"time"

	"github.com/jchv/go-webview2"
)

const webView2Ready = "MAGNETAR_WEBVIEW2_READY"

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
	view.SetTitle("Magnetar")
	view.Navigate(target)
	if _, err := fmt.Fprintln(os.Stdout, webView2Ready); err != nil {
		view.Destroy()
		return fmt.Errorf("signal WebView2 startup: %w", err)
	}
	view.Run()
	return nil
}
