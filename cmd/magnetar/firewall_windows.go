//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// configureFirewall allows Magnetar through Windows Defender Firewall so peers
// and other devices on the network can reach it. It must run elevated: it
// removes any stale rules for this executable (including a block rule left by
// declining the first-run firewall prompt) and adds an allow rule.
func configureFirewall() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Delete every existing rule for this program, then allow it inbound. The
	// delete can fail when no rule exists yet, which is fine.
	run := func(args ...string) error {
		cmd := exec.Command("netsh", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s", args[0], out)
		}
		return nil
	}
	_ = run("advfirewall", "firewall", "delete", "rule", "name=all", "program="+exe)
	if err := run("advfirewall", "firewall", "add", "rule", "name=Magnetar",
		"dir=in", "action=allow", "program="+exe, "enable=yes", "profile=private,domain"); err != nil {
		return err
	}
	return nil
}
