// Package fsutil validates file-system paths that come from users or from
// torrent metadata before they reach the operating system.
package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrEmpty       = errors.New("path is empty")
	ErrNotAbsolute = errors.New("path must be absolute")
	ErrInvalid     = errors.New("path contains invalid characters")
	ErrDevice      = errors.New("device and raw namespace paths are not allowed")
	ErrTraversal   = errors.New("path must not contain '..' elements")
	ErrOutside     = errors.New("path escapes its base folder")
)

// CleanAbs validates a user-supplied local folder or file path. It must be
// absolute, free of NUL bytes and of "..", and must not use the Windows
// device or raw namespaces (\\.\ and \\?\). It returns the cleaned path.
//
// Any ".." is rejected - not only whole ".." elements - so the check is the
// plain, widely recognised guard; folder names merely containing two dots
// (rare) are refused too.
func CleanAbs(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", ErrEmpty
	}
	if strings.ContainsRune(p, 0) {
		return "", ErrInvalid
	}
	if runtime.GOOS == "windows" || strings.HasPrefix(p, `\\`) {
		if strings.HasPrefix(p, `\\.\`) || strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `//./`) || strings.HasPrefix(p, `//?/`) {
			return "", ErrDevice
		}
	}
	if !filepath.IsAbs(p) {
		return "", ErrNotAbsolute
	}
	// Check the raw input: cleaning first would turn "/a/../etc" into "/etc".
	if strings.Contains(p, "..") {
		return "", ErrTraversal
	}
	return filepath.Clean(p), nil
}

// Within joins elems onto base and guarantees the result is base itself or
// lies inside it. Use it for paths built from untrusted names, such as file
// paths in torrent metadata.
func Within(base string, elems ...string) (string, error) {
	base = filepath.Clean(base)
	for _, e := range elems {
		if strings.ContainsRune(e, 0) {
			return "", ErrInvalid
		}
	}
	joined := filepath.Join(append([]string{base}, elems...)...)
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", ErrOutside, joined)
	}
	if strings.Contains(joined, "..") {
		return "", fmt.Errorf("%w: %q", ErrOutside, joined)
	}
	if joined == base {
		return joined, nil
	}
	prefix := base
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(joined, prefix) {
		return "", fmt.Errorf("%w: %q", ErrOutside, joined)
	}
	return joined, nil
}

// RegularFileExists reports whether p exists and is a regular file.
func RegularFileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// FreeSpace returns the bytes available to the user on the volume holding
// path. If path does not exist yet, its nearest existing parent is used.
func FreeSpace(path string) (uint64, error) {
	p, err := CleanAbs(path)
	if err != nil {
		return 0, err
	}
	for {
		if _, err := os.Stat(p); err == nil {
			return freeSpace(p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, os.ErrNotExist
		}
		p = parent
	}
}
