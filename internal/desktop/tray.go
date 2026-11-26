package desktop

import (
	_ "embed"
	"runtime"
	"sync/atomic"

	"github.com/energye/systray"
)

//go:embed icon.ico
var iconICO []byte

//go:embed icon.png
var iconPNG []byte

//go:embed icon-large.png
var iconLargePNG []byte

var trayReady atomic.Bool

// SetTrayTooltip updates the tray icon's tooltip (e.g. live speeds).
func SetTrayTooltip(s string) {
	if trayReady.Load() {
		systray.SetTooltip(s)
	}
}

// TrayActions are the callbacks behind the tray menu.
type TrayActions struct {
	Open      func()
	AltSpeed  func(enable bool) error
	IsAlt     func() bool
	PauseAll  func()
	ResumeAll func()
	Quit      func()
}

// RunTray shows the tray icon and blocks until StopTray is called.
// It must be called from the main goroutine.
func RunTray(a TrayActions) {
	systray.Run(func() { onReady(a) }, nil)
}

// StopTray removes the tray icon and makes RunTray return.
func StopTray() { systray.Quit() }

func onReady(a TrayActions) {
	if runtime.GOOS == "windows" {
		systray.SetIcon(iconICO)
	} else {
		systray.SetIcon(iconPNG)
	}
	systray.SetTitle("Magnetar")
	systray.SetTooltip("Magnetar")
	trayReady.Store(true)
	systray.SetOnClick(func(systray.IMenu) { a.Open() })
	systray.SetOnDClick(func(systray.IMenu) { a.Open() })

	open := systray.AddMenuItem("Open Magnetar", "Show the Magnetar window")
	open.Click(a.Open)
	systray.AddSeparator()
	alt := systray.AddMenuItemCheckbox("Alternative speed limits", "Toggle alternative (slower) speed limits", a.IsAlt())
	alt.Click(func() {
		want := !alt.Checked()
		if err := a.AltSpeed(want); err == nil {
			if want {
				alt.Check()
			} else {
				alt.Uncheck()
			}
		}
	})
	systray.AddMenuItem("Resume all", "Resume all torrents").Click(a.ResumeAll)
	systray.AddMenuItem("Pause all", "Pause all torrents").Click(a.PauseAll)
	systray.AddSeparator()
	systray.AddMenuItem("Quit", "Stop all transfers and exit").Click(a.Quit)
}
