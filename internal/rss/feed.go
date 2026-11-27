// Package rss reads RSS and Atom feeds and downloads new torrents that match
// the user's rules, like qBittorrent's RSS downloader.
package rss

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxFeedSize limits how much of a feed is read.
const MaxFeedSize = 8 << 20

// Item is one article of a feed.
type Item struct {
	Key       string    `json:"key"` // stable identity: guid, else torrent link, else title
	Title     string    `json:"title"`
	Torrent   string    `json:"torrent"` // magnet link or http(s) URL of a .torrent file
	Page      string    `json:"page,omitempty"`
	Published time.Time `json:"published,omitempty"`
	Size      int64     `json:"size,omitempty"`
}

type rssDoc struct {
	Channel struct {
		Title string    `xml:"title"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
	// Atom
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
	PubDate   string `xml:"pubDate"`
	Enclosure []struct {
		URL    string `xml:"url,attr"`
		Type   string `xml:"type,attr"`
		Length int64  `xml:"length,attr"`
	} `xml:"enclosure"`
	// <torrent:magnetURI>, <torrent:contentLength> (ezRSS/Nyaa style), any namespace.
	MagnetURI     string `xml:"magnetURI"`
	ContentLength int64  `xml:"contentLength"`
	Size          int64  `xml:"size"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Updated string `xml:"updated"`
	Links   []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
}

// Parse reads an RSS 2.0 or Atom document.
func Parse(data []byte) (title string, items []Item, err error) {
	var doc rssDoc
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&doc); err != nil {
		return "", nil, fmt.Errorf("not a valid RSS or Atom feed: %w", err)
	}
	for _, it := range doc.Channel.Items {
		v := Item{Title: clean(it.Title), Page: strings.TrimSpace(it.Link), Size: max(it.ContentLength, it.Size)}
		switch {
		case isMagnet(it.MagnetURI):
			v.Torrent = strings.TrimSpace(it.MagnetURI)
		case isMagnet(it.Link):
			v.Torrent = strings.TrimSpace(it.Link)
		}
		for _, enc := range it.Enclosure {
			if v.Torrent == "" && (isMagnet(enc.URL) || isTorrentURL(enc.URL, enc.Type)) {
				v.Torrent = strings.TrimSpace(enc.URL)
				if v.Size == 0 {
					v.Size = enc.Length
				}
			}
		}
		if v.Torrent == "" && isTorrentURL(it.Link, "") {
			v.Torrent = strings.TrimSpace(it.Link)
		}
		if v.Torrent == "" && isMagnet(it.GUID) {
			v.Torrent = strings.TrimSpace(it.GUID)
		}
		v.Published = parseTime(it.PubDate)
		v.Key = firstNonEmpty(strings.TrimSpace(it.GUID), v.Torrent, v.Title)
		if v.Torrent != "" && v.Key != "" {
			items = append(items, v)
		}
	}
	for _, en := range doc.Entries {
		v := Item{Title: clean(en.Title), Published: parseTime(en.Updated)}
		for _, l := range en.Links {
			switch {
			case isMagnet(l.Href) || isTorrentURL(l.Href, l.Type) || (l.Rel == "enclosure" && v.Torrent == ""):
				if v.Torrent == "" {
					v.Torrent = strings.TrimSpace(l.Href)
				}
			case l.Rel == "" || l.Rel == "alternate":
				v.Page = l.Href
			}
		}
		v.Key = firstNonEmpty(strings.TrimSpace(en.ID), v.Torrent, v.Title)
		if v.Torrent != "" && v.Key != "" {
			items = append(items, v)
		}
	}
	title = clean(firstNonEmpty(doc.Channel.Title, doc.Title))
	if items == nil && title == "" {
		return "", nil, errors.New("not a valid RSS or Atom feed")
	}
	return title, items, nil
}

func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func isMagnet(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "magnet:?")
}

func isTorrentURL(s, ctype string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return ctype == "application/x-bittorrent" || strings.HasSuffix(strings.ToLower(u.Path), ".torrent")
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Fetch downloads a URL (feed or .torrent file) with a size limit. Only
// http and https are allowed.
func Fetch(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("only http and https links are supported")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Magnetar RSS")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response is too large")
	}
	return data, nil
}
