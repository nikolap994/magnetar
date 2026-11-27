package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuery(t *testing.T) {
	for in, want := range map[string]string{
		"Show.Name.S01E02.1080p.WEB-DL.x264-GRP.mkv": "Show Name S01E02",
		"Big_Buck_Bunny_2008.mp4":                    "Big Buck Bunny 2008",
	} {
		if got := Query(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
	if SafeName("pt-BR/../x", 42) != "pt-brx-42.srt" {
		t.Fatal(SafeName("pt-BR/../x", 42))
	}
}

func TestSearchAndDownload(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && (r.Header.Get("Api-Key") != "key" || r.Header.Get("User-Agent") == "") {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v1/subtitles":
			if r.URL.Query().Get("query") != "Show S01E02" || r.URL.Query().Get("languages") != "en,sr" {
				t.Errorf("query %v", r.URL.Query())
			}
			io.WriteString(w, `{"data":[{"attributes":{"language":"en","release":"Show.S01E02.WEB","download_count":900,"files":[{"file_id":7,"file_name":"x.srt"}]}}]}`)
		case "/api/v1/download":
			var b map[string]int
			json.NewDecoder(r.Body).Decode(&b)
			if b["file_id"] != 7 {
				t.Errorf("file id %v", b)
			}
			io.WriteString(w, `{"link":"`+srv.URL+`/file.srt"}`)
		case "/file.srt":
			io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nHello\n")
		}
	}))
	defer srv.Close()
	BaseURL = srv.URL + "/api/v1"
	c := &Client{HTTP: srv.Client(), APIKey: "key", UserAgent: "Magnetar test"}
	res, err := c.Search(context.Background(), "Show S01E02", "en, sr", "")
	if err != nil || len(res) != 1 || res[0].FileID != 7 || res[0].Downloads != 900 {
		t.Fatalf("%+v %v", res, err)
	}
	data, err := c.Download(context.Background(), 7)
	if err != nil || !strings.Contains(string(data), "Hello") {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := (&Client{HTTP: srv.Client()}).Search(context.Background(), "x", "", ""); err == nil {
		t.Fatal("missing API key accepted")
	}
}

func TestMovieHash(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 200000)
	h, err := MovieHash(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(h) != 16 {
		t.Fatalf("%q %v", h, err)
	}
}
