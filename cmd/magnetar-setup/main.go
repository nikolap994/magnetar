// Command magnetar-setup is Magnetar's Windows installer. It embeds
// magnetar.exe and installs it for the current user (no administrator rights):
//
//   - %LOCALAPPDATA%\Programs\Magnetar\magnetar.exe
//   - Start Menu and desktop shortcuts
//   - magnet: and .torrent associations (optional)
//   - an entry in Settings → Apps with a working uninstaller
//
// Flags: /S silent install, /launch start Magnetar afterwards (used by the
// in-app updater), /uninstall to remove (also /S for silent).
package main

import (
	"embed"
	"fmt"
	"os"
)

//go:embed payload
var payload embed.FS //nolint:unused // used by the Windows installer

// Version is set at build time.
var Version = "dev"

func main() {
	silent, launch, uninstallMode, uninstallRun := false, false, false, ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "/S", "/s", "--silent":
			silent = true
		case "/launch", "--launch":
			launch = true
		case "/uninstall", "--uninstall":
			uninstallMode = true
		case "/uninstall-run":
			if i+1 < len(args) {
				uninstallRun = args[i+1]
				i++
			}
		}
	}
	var err error
	switch {
	case uninstallRun != "":
		err = runUninstall(uninstallRun, silent)
	case uninstallMode:
		err = startUninstall(silent)
	default:
		err = install(silent, launch)
	}
	if err != nil {
		if !silent {
			message("Magnetar Setup", fmt.Sprintf("Setup failed:\n\n%v", err), mbIconError|mbOK)
		}
		os.Exit(1)
	}
}
