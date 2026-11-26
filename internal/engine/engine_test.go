package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestDeleteTorrentDataRefusesUnsafeNames(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(filepath.Dir(base), "victim-"+filepath.Base(base))
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(victim)
	for _, name := range []string{"", ".", "..", "../" + filepath.Base(victim), "a/../../b"} {
		if err := deleteTorrentData(base, &metainfo.Info{Name: name, Length: 1}); err == nil {
			t.Errorf("name %q: expected refusal", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("file outside save path was deleted")
	}
}

func TestDeleteTorrentDataSingleAndMulti(t *testing.T) {
	base := t.TempDir()
	single := filepath.Join(base, "movie.mkv")
	os.WriteFile(single, []byte("x"), 0o600)
	if err := deleteTorrentData(base, &metainfo.Info{Name: "movie.mkv", Length: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(single); !os.IsNotExist(err) {
		t.Fatal("single file not deleted")
	}

	dir := filepath.Join(base, "album")
	os.MkdirAll(filepath.Join(dir, "cd1"), 0o755)
	os.WriteFile(filepath.Join(dir, "cd1", "t.flac"), []byte("x"), 0o600)
	info := &metainfo.Info{Name: "album", Files: []metainfo.FileInfo{{Path: []string{"cd1", "t.flac"}, Length: 1}}}
	if err := deleteTorrentData(base, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("torrent dir not deleted")
	}
}

func TestParseBareHash(t *testing.T) {
	if _, ok := parseBareHash("DD8255ECDC7CA55FB0BBF81323D87062DB1F6D1C"); !ok {
		t.Fatal("valid hash rejected")
	}
	for _, s := range []string{"", "xyz", "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1", "zz8255ecdc7ca55fb0bbf81323d87062db1f6d1c"} {
		if _, ok := parseBareHash(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestRatio(t *testing.T) {
	if r := ratio(100, 200, 0); r != 2 {
		t.Fatalf("ratio = %v", r)
	}
	if r := ratio(0, 50, 100); r != 0.5 {
		t.Fatalf("ratio with no download = %v", r)
	}
	if r := ratio(0, 0, 0); r != 0 {
		t.Fatalf("zero ratio = %v", r)
	}
}

func TestSharesData(t *testing.T) {
	e := &Engine{dataDir: t.TempDir(), torrents: map[string]*managed{
		"a": {rec: &record{InfoHash: "a", Name: "Sintel", SavePath: "/dl"}},
		"b": {rec: &record{InfoHash: "b", Name: "Other", SavePath: "/dl"}},
		"c": {rec: &record{InfoHash: "c", Name: "Sintel", SavePath: "/elsewhere"}},
	}}
	if got := e.sharesDataLocked("/dl", "Sintel"); got != "Sintel" {
		t.Fatalf("expected conflict, got %q", got)
	}
	if got := e.sharesDataLocked("/dl", "Unique"); got != "" {
		t.Fatalf("unexpected conflict %q", got)
	}
	if got := e.sharesDataLocked("/other", "Sintel"); got != "" {
		t.Fatalf("different path reported as conflict: %q", got)
	}
}
