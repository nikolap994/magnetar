package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nikolap994/magnetar/internal/config"
)

const torznab = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
<item><title>Linux Distro 24.04</title><link>%s/dl/1</link><size>1000</size><pubDate>Mon, 02 Nov 2026 10:00:00 +0000</pubDate>
<torznab:attr name="seeders" value="12"/><torznab:attr name="peers" value="20"/><torznab:attr name="infohash" value="ABCDEF"/></item>
<item><title>Linux Distro 24.04 Server</title><link>%s/dl/2</link><torznab:attr name="seeders" value="80"/>
<torznab:attr name="magneturl" value="magnet:?xt=urn:btih:0000000000000000000000000000000000000002"/></item>
</channel></rss>`

func TestSearchAndFetch(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			if r.URL.Query().Get("apikey") != "k" || r.URL.Query().Get("q") != "linux" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, sprintf(torznab, srv.URL, srv.URL))
		case "/dl/1":
			io.WriteString(w, "d8:announce0:e")
		case "/dl/3":
			http.Redirect(w, r, "magnet:?xt=urn:btih:0000000000000000000000000000000000000003", http.StatusFound)
		}
	}))
	defer srv.Close()
	ix := []config.Indexer{{Name: "Local", URL: srv.URL + "/api", APIKey: "k", Enabled: true}, {Name: "Off", URL: "http://x", Enabled: false}}
	res := Search(context.Background(), srv.Client(), ix, "linux")
	if len(res.Errors) != 0 || len(res.Results) != 2 {
		t.Fatalf("%+v", res)
	}
	if res.Results[0].Seeders != 80 || res.Results[0].Link[:7] != "magnet:" || res.Results[1].Leechers != 8 || res.Results[1].InfoHash != "abcdef" {
		t.Fatalf("results %+v", res.Results)
	}
	data, mag, err := Fetch(context.Background(), srv.Client(), srv.URL+"/dl/1")
	if err != nil || mag != "" || string(data) != "d8:announce0:e" {
		t.Fatalf("fetch: %q %q %v", data, mag, err)
	}
	_, mag, err = Fetch(context.Background(), srv.Client(), srv.URL+"/dl/3")
	if err != nil || mag != "magnet:?xt=urn:btih:0000000000000000000000000000000000000003" {
		t.Fatalf("redirect to magnet: %q %v", mag, err)
	}
	if _, _, err := Fetch(context.Background(), srv.Client(), "file:///etc/passwd"); err == nil {
		t.Fatal("file link accepted")
	}
}

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }
