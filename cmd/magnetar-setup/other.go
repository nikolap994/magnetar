//go:build !windows

package main

import "errors"

var errWindowsOnly = errors.New("magnetar-setup only runs on Windows; on Linux use the .deb package")

const (
	mbOK        = 0
	mbIconError = 0
)

func install(bool, bool) error           { return errWindowsOnly }
func startUninstall(bool) error          { return errWindowsOnly }
func runUninstall(string, bool) error    { return errWindowsOnly }
func message(string, string, uint32) int { return 0 }
