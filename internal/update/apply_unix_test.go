//go:build linux

package update

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyInPlaceReplacesAndRestarts(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "magnetar")
	os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	marker := filepath.Join(dir, "started")
	newBin := []byte("#!/bin/sh\necho \"$@\" > " + marker + "\n")
	archive := filepath.Join(dir, "update.tar.gz")
	f, _ := os.Create(archive)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "magnetar_9.9.9_linux_amd64/magnetar", Mode: 0o755, Size: int64(len(newBin)), Typeflag: tar.TypeReg})
	tw.Write(newBin)
	tw.Close()
	gz.Close()
	f.Close()

	restarted := make(chan struct{})
	RestartArgs = []string{"-dir=/data"}
	u := &Updater{exe: exe, method: MethodLinuxPortable, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		restart: func() { close(restarted) }}
	if err := u.applyInPlace(archive); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newBin) {
		t.Fatal("executable was not replaced")
	}
	if _, err := os.Stat(exe + ".old"); err != nil {
		t.Fatal("previous executable not kept for rollback/cleanup")
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("old instance was not asked to shut down")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if b, err := os.ReadFile(marker); err == nil {
			if got := string(b); got != "--after-update -dir=/data\n" {
				t.Fatalf("new version started with %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new version was not started")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
