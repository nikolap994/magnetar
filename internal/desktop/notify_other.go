//go:build !windows && !darwin

package desktop

import (
	"github.com/godbus/dbus/v5"
)

// Notify shows a desktop notification through the freedesktop.org
// notification service (GNOME, KDE, Cinnamon, XFCE, ...). It is a no-op when
// no session bus or notification daemon is available.
func Notify(title, body string) {
	go func() {
		conn, err := dbus.SessionBus()
		if err != nil {
			return
		}
		obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
		obj.Call("org.freedesktop.Notifications.Notify", 0,
			"Magnetar", uint32(0), "magnetar", title, body, []string{},
			map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant("magnetar")}, int32(-1))
	}()
}
