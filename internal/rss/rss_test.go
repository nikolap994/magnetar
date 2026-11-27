package rss

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
)

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torrent="http://xmlns.ezrss.it/0.1/">
<channel><title>Shows</title>
<item><title>My Show S01E02 1080p WEB</title><guid>a1</guid><torrent:magnetURI>magnet:?xt=urn:btih:%040x</torrent:magnetURI><pubDate>Mon, 02 Nov 2026 10:00:00 +0000</pubDate></item>
<item><title>My Show S01E02 720p WEB</title><guid>a2</guid><link>magnet:?xt=urn:btih:%040x</link></item>
<item><title>My Show S01E03 1080p WEB</title><guid>a3</guid><enclosure url="https://example.invalid/x.torrent" type="application/x-bittorrent" length="123"/></item>
<item><title>Other Show 1x05 1080p</title><guid>a4</guid><link>magnet:?xt=urn:btih:%040x</link></item>
<item><title>No torrent here</title><link>https://example.com/page</link></item>
</channel></rss>`

func TestParseRSS(t *testing.T) {
	title, items, err := Parse([]byte(fmt.Sprintf(sampleRSS, 1, 2, 4)))
	if err != nil {
		t.Fatal(err)
	}
	if title != "Shows" || len(items) != 4 {
		t.Fatalf("title %q, %d items", title, len(items))
	}
	if items[0].Published.IsZero() || items[2].Torrent != "https://example.invalid/x.torrent" || items[2].Size != 123 {
		t.Fatalf("items: %+v", items)
	}
}

func TestParseAtom(t *testing.T) {
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>A</title><entry><title>Linux ISO</title><id>x</id>
<link rel="alternate" href="https://example.com/p"/><link rel="enclosure" type="application/x-bittorrent" href="https://example.com/l.torrent"/></entry></feed>`
	_, items, err := Parse([]byte(atom))
	if err != nil || len(items) != 1 || items[0].Torrent != "https://example.com/l.torrent" || items[0].Page != "https://example.com/p" {
		t.Fatalf("%+v %v", items, err)
	}
}

func TestRuleMatching(t *testing.T) {
	r := Rule{Name: "x", MustContain: "my show 1080p|my show 2160p", MustNotContain: "720p"}
	for title, want := range map[string]bool{
		"My.Show.S01E02.1080p": true, // words are matched one by one, so dotted names work
		"My Show S01E02 1080p": true,
		"My Show S01E02 2160p": true,
		"My Show S01E02 720p":  false,
		"Other 1080p":          false,
	} {
		if got := r.Matches(title); got != want {
			t.Errorf("%q: got %v", title, got)
		}
	}
	rx := Rule{Name: "rx", UseRegex: true, MustContain: `my[ .]show.*1080p`}
	if !rx.Matches("My.Show.S01E02.1080p") {
		t.Error("regex should match")
	}
	if (&Rule{Name: "bad", UseRegex: true, MustContain: "("}).Validate() == nil {
		t.Error("invalid regex accepted")
	}
	if (&Rule{Name: "empty"}).Matches("anything") {
		t.Error("an empty filter must not match everything")
	}
	if Episode("Show.S1E2.x") != "S01E02" || Episode("Show 2x10") != "S02E10" || Episode("Show 1080p") != "" {
		t.Error("episode parsing")
	}
}

func TestAutoDownloadSmartEpisodes(t *testing.T) {
	feed := fmt.Sprintf(sampleRSS, 1, 2, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feed) }))
	defer srv.Close()

	s := config.Defaults()
	s.DownloadDir, s.ListenPort = t.TempDir(), 0
	s.EnableDHT, s.EnableUPnP, s.EnableUTP, s.QueueEnabled = false, false, false, false
	eng, err := engine.New(s, t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	dir := t.TempDir()
	m := New(dir, eng, func() *http.Client { return srv.Client() }, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	f, err := m.AddFeed(ctx, srv.URL)
	if err != nil || len(f.Items) != 4 || f.Title != "Shows" {
		t.Fatalf("feed: %+v %v", f, err)
	}
	if err := m.SetRules(ctx, []Rule{{Name: "my show", Enabled: true, MustContain: "my show", SmartEpisodes: true, Paused: true}}); err != nil {
		t.Fatal(err)
	}
	// S01E02 once (two qualities), S01E03 is a .torrent URL that cannot be fetched.
	if n := len(eng.List()); n != 1 {
		t.Fatalf("want 1 torrent, got %d", n)
	}
	// Reload from disk: nothing is downloaded twice.
	m2 := New(dir, eng, func() *http.Client { return srv.Client() }, slog.New(slog.DiscardHandler))
	m2.Refresh(ctx, "")
	if n := len(eng.List()); n != 1 {
		t.Fatalf("downloaded again after restart: %d", n)
	}
	if got := m2.Get().Rules[0].Episodes; len(got) != 1 || got[0] != "S01E02" {
		t.Fatalf("episodes %v", got)
	}
	if titles, _ := m2.Matches(Rule{Name: "t", MustContain: "other"}); len(titles) != 1 {
		t.Fatalf("matches %v", titles)
	}
}
