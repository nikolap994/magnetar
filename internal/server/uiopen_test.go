package server

import (
	"bytes"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
)

// Opening a .torrent file from the desktop must show the add dialog with a
// preview, never add the torrent straight away.
func TestUIOpenTorrentFileShowsPreview(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := store.Get()
	s.DownloadDir, s.ListenPort = t.TempDir(), 0
	s.EnableDHT, s.EnableUPnP, s.EnableUTP = false, false, false
	eng, err := engine.New(s, t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	keys, err := auth.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	var shown []string
	var static fs.FS = fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	srv := New(eng, store, keys, static, slog.New(slog.DiscardHandler), Options{Port: 8899, Shutdown: func() {},
		ShowUI: func(target string) { shown = append(shown, target) }})

	info := metainfo.Info{Name: "file.bin", Length: 1 << 14, PieceLength: 1 << 14, Pieces: make([]byte, 20)}
	ib, _ := bencode.Marshal(info)
	mi := metainfo.MetaInfo{InfoBytes: ib}
	var tf bytes.Buffer
	if err := mi.Write(&tf); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("torrent", "file.torrent")
	fw.Write(tf.Bytes())
	mw.Close()

	req := httptest.NewRequest("POST", "/api/ui/open", &body)
	req.Host = "127.0.0.1:8899"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Magnetar-Request", "1")
	req.Header.Set("Authorization", "Launch "+keys.NewLaunchToken())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	want := PreviewTarget(mi.HashInfoBytes().HexString())
	if len(shown) != 1 || shown[0] != want {
		t.Fatalf("showUI got %v, want %s", shown, want)
	}
	if n := len(eng.List()); n != 0 {
		t.Fatalf("torrent was added without asking (%d in list)", n)
	}
}
