//go:build !windows

package main

import "errors"

// configureFirewall is only needed on Windows.
func configureFirewall() error { return errors.New("not supported on this system") }
