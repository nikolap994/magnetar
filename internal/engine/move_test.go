package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMoveTorrentDataFolder(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "Album", "cd1", "a.flac"), "a")
	write(t, filepath.Join(src, "Album", "b.flac.part"), "b")
	write(t, filepath.Join(src, "Other", "keep.txt"), "k")
	info := &metainfo.Info{Name: "Album", Files: []metainfo.FileInfo{{Path: []string{"cd1", "a.flac"}, Length: 1}}}
	if err := moveTorrentData(src, dst, info); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "Album", "cd1", "a.flac")); string(b) != "a" {
		t.Fatal("file not moved")
	}
	if _, err := os.Stat(filepath.Join(dst, "Album", "b.flac.part")); err != nil {
		t.Fatal("partial file not moved")
	}
	if _, err := os.Stat(filepath.Join(src, "Album")); !os.IsNotExist(err) {
		t.Fatal("source folder left behind")
	}
	if _, err := os.Stat(filepath.Join(src, "Other", "keep.txt")); err != nil {
		t.Fatal("unrelated data was touched")
	}
}

func TestMoveTorrentDataSingleFileWithPart(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "movie.mkv.part"), "partial")
	info := &metainfo.Info{Name: "movie.mkv", Length: 10}
	if err := moveTorrentData(src, dst, info); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "movie.mkv.part")); string(b) != "partial" {
		t.Fatal(".part file not moved")
	}
}

func TestMoveTorrentDataRefusesOverwrite(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "movie.mkv"), "new")
	write(t, filepath.Join(dst, "movie.mkv"), "existing")
	if err := moveTorrentData(src, dst, &metainfo.Info{Name: "movie.mkv", Length: 3}); err == nil {
		t.Fatal("overwrote an existing file")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "movie.mkv")); string(b) != "existing" {
		t.Fatal("destination file changed")
	}
}

func TestMoveTorrentDataRefusesTraversal(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := moveTorrentData(src, dst, &metainfo.Info{Name: "..", Length: 1}); err == nil {
		t.Fatal("moved with an unsafe name")
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "d", "x.bin"), "x")
	to := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, to); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(to, "d", "x.bin")); string(b) != "x" {
		t.Fatal("copy incomplete")
	}
}
