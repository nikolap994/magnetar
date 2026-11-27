//go:build darwin

package desktop

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
)

const launchAgentLabel = "io.github.nikolap994.magnetar"

// SetAutostart starts Magnetar (without opening a window) when the user signs
// in, using a LaunchAgent.
func SetAutostart(enable bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	if !enable {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var esc bytes.Buffer
	xml.EscapeText(&esc, []byte(exe))
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + launchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array><string>` + esc.String() + `</string><string>--no-browser</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(plist), 0o644)
}
