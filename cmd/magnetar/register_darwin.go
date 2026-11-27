//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreServices -framework CoreFoundation
#include <CoreServices/CoreServices.h>
#include <stdlib.h>

static int mgSetHandlers(const char *bundle) {
	CFStringRef id = CFStringCreateWithCString(NULL, bundle, kCFStringEncodingUTF8);
	OSStatus a = LSSetDefaultHandlerForURLScheme(CFSTR("magnet"), id);
	OSStatus b = LSSetDefaultRoleHandlerForContentType(CFSTR("org.bittorrent.torrent"), kLSRolesAll, id);
	CFRelease(id);
	return a != noErr ? (int)a : (int)b;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const bundleID = "io.github.nikolap994.magnetar"

// register makes Magnetar.app the default app for magnet links and .torrent
// files. macOS knows the app once it is in the Applications folder.
func register() error {
	cs := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cs))
	if rc := C.mgSetHandlers(cs); rc != 0 {
		return fmt.Errorf("macOS refused to set Magnetar as the default app (error %d); open Magnetar from the Applications folder once and try again", int(rc))
	}
	return nil
}

// unregister: macOS has no "no handler" state; another app takes over when
// the user picks it, so there is nothing to remove.
func unregister() error { return nil }
