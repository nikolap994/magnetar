//go:build darwin

package desktop

import "os/exec"

// Notify shows a notification in Notification Center. The texts are passed
// as arguments, never as script source.
func Notify(title, body string) {
	go exec.Command("osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		title, body).Run()
}
