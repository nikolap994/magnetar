package geoip

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const sample = `1.0.0.0,1.0.0.255,AU
8.8.8.0,8.8.8.255,US
81.0.0.0,81.255.255.255,de
203.0.113.0,203.0.113.255,JP
`

func TestParseAndLookup(t *testing.T) {
	db := &DB{}
	ranges, err := parse(stringsReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	db.ranges = ranges
	cases := map[string]string{
		"8.8.8.8":     "US",
		"1.0.0.50":    "AU",
		"81.2.69.142": "DE", // lowercase in data, upper-cased
		"203.0.113.9": "JP",
		"9.9.9.9":     "",
		"2001:db8::1": "",
	}
	for ip, want := range cases {
		if got := db.Lookup(net.ParseIP(ip)); got != want {
			t.Errorf("%s: got %q want %q", ip, got, want)
		}
	}
}

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, sample)
	}))
	defer srv.Close()
	SourceURL = srv.URL
	dir := t.TempDir()
	db := &DB{}
	if err := db.Download(context.Background(), srv.Client(), dir); err != nil {
		t.Fatal(err)
	}
	if !db.Ready() || db.Lookup(net.ParseIP("8.8.8.8")) != "US" {
		t.Fatal("not loaded")
	}
	if Stale(dir) {
		t.Fatal("fresh download reported stale")
	}
	// Reload from the cached file.
	db2 := &DB{}
	if err := db2.Load(dir); err != nil || db2.Lookup(net.ParseIP("1.0.0.1")) != "AU" {
		t.Fatalf("reload: %v", err)
	}
	_ = filepath.Join
}

func stringsReader(s string) *stringReader { return &stringReader{s: s} }

type stringReader struct {
	s string
	i int
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
