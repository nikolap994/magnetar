package fsutil

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func abs(t *testing.T, parts ...string) string {
	t.Helper()
	root := "/"
	if runtime.GOOS == "windows" {
		root = `C:\`
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func TestCleanAbs(t *testing.T) {
	good := abs(t, "home", "me", "Downloads")
	if got, err := CleanAbs("  " + good + string(filepath.Separator) + " "); err != nil || got != good {
		t.Fatalf("CleanAbs(good) = %q, %v", got, err)
	}
	if got, err := CleanAbs(abs(t, "a..b", "c")); err != nil || got != abs(t, "a..b", "c") {
		t.Fatalf("names containing dots must be allowed: %q, %v", got, err)
	}
	cases := map[string]error{
		"":                      ErrEmpty,
		"relative/dir":          ErrNotAbsolute,
		abs(t, "x") + "\x00y":   ErrInvalid,
		abs(t, "a") + "/../etc": ErrTraversal,
		`\\.\PhysicalDrive0`:    ErrDevice,
		`\\?\C:\Windows`:        ErrDevice,
	}
	for in, want := range cases {
		if _, err := CleanAbs(in); !errors.Is(err, want) {
			t.Errorf("CleanAbs(%q) error = %v, want %v", in, err, want)
		}
	}
}

func TestWithin(t *testing.T) {
	base := abs(t, "dl")
	if got, err := Within(base, "Movie", "file.mkv"); err != nil || got != filepath.Join(base, "Movie", "file.mkv") {
		t.Fatalf("Within(ok) = %q, %v", got, err)
	}
	if got, err := Within(base); err != nil || got != base {
		t.Fatalf("Within(base) = %q, %v", got, err)
	}
	for _, elems := range [][]string{
		{".."},
		{"..", "etc", "passwd"},
		{"Movie", "..", "..", "secret"},
		{"a\x00b"},
	} {
		if _, err := Within(base, elems...); err == nil {
			t.Errorf("Within(%q) should fail", elems)
		}
	}
	// A sibling folder that merely shares the prefix must be rejected.
	if _, err := Within(base, "..", "dl-other", "x"); err == nil {
		t.Error("sibling with shared prefix accepted")
	}
}
