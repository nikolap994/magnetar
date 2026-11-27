package dlna

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
)

func TestBrowseAndStream(t *testing.T) {
	s := config.Defaults()
	s.DownloadDir, s.ListenPort, s.QueueEnabled = t.TempDir(), 0, false
	s.EnableDHT, s.EnableUPnP, s.EnableUTP = false, false, false
	eng, err := engine.New(s, t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	info := metainfo.Info{Name: "Movie", PieceLength: 1 << 14, Pieces: make([]byte, 40),
		Files: []metainfo.FileInfo{{Path: []string{"movie.mkv"}, Length: 20000}, {Path: []string{"notes.nfo"}, Length: 100}}}
	ib, _ := bencode.Marshal(info)
	var buf bytes.Buffer
	(&metainfo.MetaInfo{InfoBytes: ib}).Write(&buf)
	hash, err := eng.Add(engine.AddOptions{TorrentFile: buf.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(eng, slog.New(slog.DiscardHandler), 8901, "Living room")

	browse := func(id, flag string) string {
		body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><ObjectID>` +
			id + `</ObjectID><BrowseFlag>` + flag + `</BrowseFlag><Filter>*</Filter><StartingIndex>0</StartingIndex><RequestedCount>0</RequestedCount><SortCriteria></SortCriteria></u:Browse></s:Body></s:Envelope>`
		req := httptest.NewRequest("POST", "/dlna/control/cds", strings.NewReader(body))
		req.Host = "192.168.1.5:8901"
		req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`)
		w := httptest.NewRecorder()
		srv.handleCDS(w, req)
		b, _ := io.ReadAll(w.Body)
		return string(b)
	}
	root := browse("0", "BrowseDirectChildren")
	if !strings.Contains(root, "t:"+hash) || !strings.Contains(root, "Movie") {
		t.Fatalf("root: %s", root)
	}
	items := browse("t:"+hash, "BrowseDirectChildren")
	if !strings.Contains(items, "movie.mkv") || strings.Contains(items, "notes.nfo") ||
		!strings.Contains(items, "http://192.168.1.5:8901/dlna/media/"+hash+"/0") || !strings.Contains(items, "videoItem") {
		t.Fatalf("items: %s", items)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/dlna/desc.xml", nil)
	srv.handleDesc(w, r)
	if !strings.Contains(w.Body.String(), "Living room") || !strings.Contains(w.Body.String(), "MediaServer:1") {
		t.Fatal("device description")
	}
	// Only media is streamed: the .nfo file is refused.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dlna/media/{hash}/{index}", srv.handleMedia)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/dlna/media/"+hash+"/1", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("nfo served: %d", w.Code)
	}
}
