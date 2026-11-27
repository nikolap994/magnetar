// Package finish runs what the user asked for when downloads complete: a
// command per finished download, and quitting, sleeping or shutting down once
// nothing is left to download (after a cancellable countdown).
package finish

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/logsafe"
)

// Countdown is how long the user has to cancel a power action.
const Countdown = 60 * time.Second

// Torrent describes a finished download for the command placeholders.
type Torrent struct {
	Name, Hash, SavePath, Category string
	Tags                           []string
}

// Expand splits a command line into arguments (double or single quotes
// group words) and fills in the placeholders:
//
//	%N name  %F content path (file or folder)  %D save folder
//	%I info hash  %L category  %G tags (comma separated)
//
// No shell is involved, so names cannot inject commands.
func Expand(cmdline string, t Torrent) ([]string, error) {
	args, err := split(cmdline)
	if err != nil {
		return nil, err
	}
	r := strings.NewReplacer("%N", t.Name, "%F", filepath.Join(t.SavePath, t.Name), "%D", t.SavePath,
		"%I", t.Hash, "%L", t.Category, "%G", strings.Join(t.Tags, ","), "%%", "%")
	for i := range args {
		args[i] = r.Replace(args[i])
	}
	return args, nil
}

func split(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inArg := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inArg = r, true
		case r == ' ' || r == '\t':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote in the command")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

// RunCommand starts the user's command for a finished download without
// waiting for it; it is stopped after an hour.
func RunCommand(log *slog.Logger, cmdline string, t Torrent) {
	if strings.TrimSpace(cmdline) == "" {
		return
	}
	args, err := Expand(cmdline, t)
	if err != nil || len(args) == 0 {
		log.Warn("command after download", "err", err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // the user's own command, set on this computer only
		cmd.Dir = t.SavePath
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Warn("command after download failed", "name", logsafe.String(t.Name), "err", logsafe.Err(err),
				"output", logsafe.String(string(out[:min(len(out), 500)])))
			return
		}
		log.Info("command after download finished", "name", logsafe.String(t.Name))
	}()
}

// Runner holds the pending power action.
type Runner struct {
	mu      sync.Mutex
	action  string
	until   time.Time
	cancel  context.CancelFunc
	quit    func()
	log     *slog.Logger
	execute func(action string) error // replaced in tests
}

// New creates a runner; quit stops Magnetar.
func New(log *slog.Logger, quit func()) *Runner {
	return &Runner{log: log, quit: quit, execute: powerAction}
}

// Pending reports the scheduled action and when it happens ("" if none).
func (r *Runner) Pending() (string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.action, r.until
}

// Schedule starts the countdown for action ("quit", "sleep", "shutdown").
func (r *Runner) Schedule(action string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil || action == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.action, r.until, r.cancel = action, time.Now().Add(Countdown), cancel
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(Countdown):
		}
		r.mu.Lock()
		r.action, r.cancel = "", nil
		r.mu.Unlock()
		r.log.Info("downloads finished", "action", action)
		if action == "quit" {
			r.quit()
			return
		}
		if err := r.execute(action); err != nil {
			r.log.Error("could not "+action, "err", err)
		}
	}()
}

// Cancel stops a pending countdown.
func (r *Runner) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	r.action, r.cancel = "", nil
}

// powerAction puts the computer to sleep or shuts it down.
func powerAction(action string) error {
	var cmd []string
	switch runtime.GOOS + "/" + action {
	case "windows/shutdown":
		cmd = []string{"shutdown", "/s", "/t", "0"}
	case "windows/sleep":
		cmd = []string{"rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0"}
	case "darwin/shutdown":
		cmd = []string{"osascript", "-e", `tell application "System Events" to shut down`}
	case "darwin/sleep":
		cmd = []string{"pmset", "sleepnow"}
	case "linux/shutdown":
		cmd = []string{"systemctl", "poweroff"}
	case "linux/sleep":
		cmd = []string{"systemctl", "suspend"}
	default:
		return errors.New("not supported on this system")
	}
	if os.Getenv("MAGNETAR_CONTAINER") != "" {
		return errors.New("power actions are not available in a container")
	}
	return exec.Command(cmd[0], cmd[1:]...).Run()
}
