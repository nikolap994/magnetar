//go:build !darwin

package desktop

// InstallOpenHandlers is only needed on macOS, where links and files arrive
// as Apple Events; elsewhere they are command-line arguments.
func InstallOpenHandlers() {}

// OpenRequests never delivers anything outside macOS.
func OpenRequests() <-chan string { return nil }
