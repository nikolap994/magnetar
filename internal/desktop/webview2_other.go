//go:build !windows

package desktop

import "errors"

func openWebView2Window(_, _, _ string) (bool, error) {
	return false, nil
}

// RunWebView2Window is only available on Windows.
func RunWebView2Window(_, _, _ string) error {
	return errors.New("WebView2 is only available on Windows")
}
