//go:build !windows

package main

func hasConsole() bool { return true }

func showFatal(error) {}
