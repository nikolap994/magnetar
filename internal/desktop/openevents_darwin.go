//go:build darwin

package desktop

/*
#cgo LDFLAGS: -framework Cocoa
void magnetarInstallOpenHandlers(void);
*/
import "C"

var openRequests = make(chan string, 32)

//export magnetarOpenRequest
func magnetarOpenRequest(s *C.char) {
	select {
	case openRequests <- C.GoString(s):
	default: // drop when flooded
	}
}

// InstallOpenHandlers starts receiving magnet links and files opened with
// Magnetar from Finder and browsers. Call it before the tray run loop starts.
func InstallOpenHandlers() { C.magnetarInstallOpenHandlers() }

// OpenRequests delivers magnet links and .torrent file paths.
func OpenRequests() <-chan string { return openRequests }
