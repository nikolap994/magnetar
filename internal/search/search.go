// Package search queries the user's own indexers through the Torznab API
// (served by Prowlarr, Jackett and many indexers) and merges the results.
package search

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

// Result is one search hit.
type Result struct {
	Title    string    `json:"title"`
	Indexer  string    `json:"indexer"`
	Size     int64     `json:"size"`
	Seeders  int       `json:"seeders"`
	Leechers int       `json:"leechers"`
	Date     time.Time `json:"date,omitzero"`
	Link     string    `json:"link"`              // magnet or http(s) link to the .torrent
	Page     string    `json:"page,omitempty"`    // details page on the indexer
	InfoHash string    `json:"infoHash,omitempty"`
}

// Response is the merged answer of all indexers.
type Response struct {
	Results []Result `json:"results"`
	Errors  []string `json:"errors"`
}

type rss struct {
	Channel struct {
		Items []item `xml:"item"`
	} `xml:"channel"`
	Error *struct {
		Description string `xml:"description,attr"`
	} `xml:"error"`
}

type item struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Comments  string `xml:"comments"`
	PubDate   string `xml:"pubDate"`
	Size      int64  `xml:"size"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

const maxResponse = 16 << 20

// Search asks every enabled indexer at once and merges the results, best
// seeded first. Indexers that fail are reported in Errors.
func Search(ctx context.Context, client *http.Client, indexers []config.Indexer, query string) Response {
	query = strings.TrimSpace(query)
	resp := Response{Results: []Result{}, Errors: []string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ix := range indexers {
		if !ix.Enabled {
			continue
		}
		wg.Add(1)
		go func(ix config.Indexer) {
			defer wg.Done()
			ictx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			res, err := query1(ictx, client, ix, query)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				resp.Errors = append(resp.Errors, ix.Name+": "+err.Error())
				return
			}
			resp.Results = append(resp.Results, res...)
		}(ix)
	}
	wg.Wait()
	sort.SliceStable(resp.Results, func(i, j int) bool { return resp.Results[i].Seeders > resp.Results[j].Seeders })
	if len(resp.Results) > 500 {
		resp.Results = resp.Results[:500]
	}
	return resp
}

func query1(ctx context.Context, client *http.Client, ix config.Indexer, q string) ([]Result, error) {
	u, err := url.Parse(ix.URL)
	if err != nil {
		return nil, err
	}
	v := u.Query()
	v.Set("t", "search")
	v.Set("q", q)
	if ix.APIKey != "" {
		v.Set("apikey", ix.APIKey)
	}
	u.RawQuery = v.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Magnetar")
	r, err := client.Do(req)
	if err != nil {
		return nil, errors.New("not reachable")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered %s", r.Status)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxResponse))
	if err != nil {
		return nil, err
	}
	return Parse(body, ix.Name)
}

// Parse reads a Torznab response.
func Parse(body []byte, indexer string) ([]Result, error) {
	var doc rss
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.New("not a Torznab response")
	}
	if doc.Error != nil {
		return nil, errors.New(doc.Error.Description)
	}
	out := make([]Result, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		r := Result{Title: clean(it.Title), Indexer: indexer, Size: max(it.Size, it.Enclosure.Length), Page: it.Comments}
		peers := -1
		for _, a := range it.Attrs {
			switch a.Name {
			case "seeders":
				r.Seeders, _ = strconv.Atoi(a.Value)
			case "peers":
				peers, _ = strconv.Atoi(a.Value)
			case "leechers":
				r.Leechers, _ = strconv.Atoi(a.Value)
			case "magneturl":
				if strings.HasPrefix(strings.ToLower(a.Value), "magnet:?") {
					r.Link = a.Value
				}
			case "infohash":
				r.InfoHash = strings.ToLower(a.Value)
			case "size":
				if r.Size == 0 {
					r.Size, _ = strconv.ParseInt(a.Value, 10, 64)
				}
			}
		}
		if peers >= 0 && r.Leechers == 0 {
			r.Leechers = max(peers-r.Seeders, 0)
		}
		if r.Link == "" {
			r.Link = firstWeb(it.Enclosure.URL, it.Link)
		}
		if r.Link == "" && r.InfoHash != "" {
			r.Link = "magnet:?xt=urn:btih:" + r.InfoHash
		}
		for _, layout := range []string{time.RFC1123Z, time.RFC1123} {
			if t, err := time.Parse(layout, strings.TrimSpace(it.PubDate)); err == nil {
				r.Date = t.UTC()
				break
			}
		}
		if r.Title != "" && r.Link != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

func firstWeb(urls ...string) string {
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(strings.ToLower(u), "magnet:?") {
			return u
		}
	}
	return ""
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}

// errMagnet carries a magnet link an indexer redirected to.
type errMagnet struct{ link string }

func (e errMagnet) Error() string { return "redirected to a magnet link" }

// Fetch downloads a result's .torrent file. Indexers often answer with a
// redirect to a magnet link instead; then that link is returned.
func Fetch(ctx context.Context, base *http.Client, link string) (torrent []byte, magnet string, err error) {
	if strings.HasPrefix(strings.ToLower(link), "magnet:?") {
		return nil, link, nil
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, "", errors.New("not a download link")
	}
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme == "magnet" {
			return errMagnet{req.URL.String()}
		}
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		var em errMagnet
		if errors.As(err, &em) {
			return nil, em.link, nil
		}
		return nil, "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download: server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > 50<<20 {
		return nil, "", errors.New("the .torrent file is too large")
	}
	return data, "", nil
}
