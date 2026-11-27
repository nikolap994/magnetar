package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// RestartArgs are passed to the new process after an in-place update.
var RestartArgs []string

func (u *Updater) apply(file string) error {
	switch u.method {
	case MethodWindowsSetup:
		return u.applySetup(file)
	case MethodDeb:
		return u.applyDeb(file)
	case MethodWindowsPortable, MethodLinuxPortable:
		return u.applyInPlace(file)
	}
	return errors.New("unsupported installation")
}

// applyInPlace replaces the running executable with the one from the
// downloaded archive and starts the new version.
func (u *Updater) applyInPlace(archive string) error {
	bin := "magnetar"
	if u.method == MethodWindowsPortable {
		bin = "magnetar.exe"
	}
	newExe := u.exe + ".new"
	if err := extractBinary(archive, bin, newExe); err != nil {
		return fmt.Errorf("extract update: %w", err)
	}
	old := u.exe + ".old"
	os.Remove(old)
	// Windows cannot overwrite a running executable but can rename it.
	if err := os.Rename(u.exe, old); err != nil {
		os.Remove(newExe)
		return fmt.Errorf("replace executable: %w", err)
	}
	if err := os.Rename(newExe, u.exe); err != nil {
		os.Rename(old, u.exe) // roll back
		return fmt.Errorf("replace executable: %w", err)
	}
	if err := startDetached(u.exe, append([]string{"--after-update"}, RestartArgs...)...); err != nil {
		return fmt.Errorf("start new version: %w", err)
	}
	u.log.Info("update installed, restarting")
	go func() { time.Sleep(300 * time.Millisecond); u.restart() }()
	return nil
}

// extractBinary copies the executable named bin from a .zip or .tar.gz
// release archive to dst.
func extractBinary(archive, bin, dst string) error {
	var src io.Reader
	var closer func()
	switch {
	case strings.HasSuffix(archive, ".zip"):
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if path.Base(f.Name) == bin && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				src, closer = rc, func() { rc.Close() }
				break
			}
		}
	case strings.HasSuffix(archive, ".tar.gz"):
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if path.Base(h.Name) == bin && h.Typeflag == tar.TypeReg {
				src = tr
				break
			}
		}
	default:
		return errors.New("unknown archive format")
	}
	if src == nil {
		return fmt.Errorf("%s not found in update archive", bin)
	}
	if closer != nil {
		defer closer()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(src, maxAssetSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxAssetSize {
		err = errors.New("executable is unexpectedly large")
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// CleanupOld removes the previous executable left by an in-place update.
func CleanupOld() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(filepath.Clean(exe) + ".old")
	}
}

// Relaunch starts a new Magnetar process with the same flags, for settings
// that need a restart. The new process waits for this one to exit, so the
// caller should shut down right after.
func Relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return startDetached(exe, append([]string{"--after-update"}, RestartArgs...)...)
}
