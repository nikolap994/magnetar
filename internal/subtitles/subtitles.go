// Package subtitles finds and downloads subtitles from OpenSubtitles
// (api.opensubtitles.com, REST API v1) with the user's own API key.
package subtitles

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BaseURL is the API root (replaced in tests).
var BaseURL = "https://api.opensubtitles.com/api/v1"

// Client talks to OpenSubtitles.
type Client struct {
	HTTP      *http.Client
	APIKey    string
	Username  string
	Password  string
	UserAgent string

	mu      sync.Mutex
	token   string
	tokenAt time.Time
}

// Result is one subtitle file found.
type Result struct {
	FileID          int    `json:"fileId"`
	Language        string `json:"language"`
	Release         string `json:"release"`
	Downloads       int    `json:"downloads"`
	HearingImpaired bool   `json:"hearingImpaired"`
	MovieHashMatch  bool   `json:"hashMatch"`
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, body any, out any) error {
	if c.APIKey == "" {
		return errors.New("add your OpenSubtitles API key in Settings first")
	}
	u := BaseURL + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Api-Key", c.APIKey)
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.mu.Lock()
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.mu.Unlock()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("OpenSubtitles is not reachable")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
			Errors  []string
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Message
		if msg == "" && len(e.Errors) > 0 {
			msg = e.Errors[0]
		}
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("OpenSubtitles: %s", msg)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// login gets a token when an account is configured; it allows more downloads.
func (c *Client) login(ctx context.Context) error {
	if c.Username == "" || c.Password == "" {
		return nil
	}
	c.mu.Lock()
	fresh := c.token != "" && time.Since(c.tokenAt) < 12*time.Hour
	c.mu.Unlock()
	if fresh {
		return nil
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/login", nil, map[string]string{"username": c.Username, "password": c.Password}, &out); err != nil {
		return err
	}
	c.mu.Lock()
	c.token, c.tokenAt = out.Token, time.Now()
	c.mu.Unlock()
	return nil
}

var (
	junk   = regexp.MustCompile(`(?i)\b(1080p|2160p|720p|480p|x26[45]|h\.?26[45]|hevc|web-?dl|webrip|bluray|brrip|hdtv|aac|ac3|dts|hdr|10bit|proper|repack)\b.*$`)
	spaces = regexp.MustCompile(`[._\s]+`)
)

// Query turns a video file name into a search query ("Show S01E02").
func Query(fileName string) string {
	name := strings.TrimSuffix(fileName, path.Ext(fileName))
	name = spaces.ReplaceAllString(name, " ")
	name = junk.ReplaceAllString(name, "")
	return strings.TrimSpace(strings.Trim(name, "-[]() "))
}

// Search finds subtitles by name and, when known, the OpenSubtitles hash of
// the video file.
func (c *Client) Search(ctx context.Context, query, languages, movieHash string) ([]Result, error) {
	q := url.Values{}
	q.Set("query", query)
	if languages != "" {
		q.Set("languages", strings.ToLower(strings.ReplaceAll(languages, " ", "")))
	}
	if movieHash != "" {
		q.Set("moviehash", movieHash)
	}
	var out struct {
		Data []struct {
			Attributes struct {
				Language        string `json:"language"`
				Release         string `json:"release"`
				DownloadCount   int    `json:"download_count"`
				HearingImpaired bool   `json:"hearing_impaired"`
				MovieHashMatch  bool   `json:"moviehash_match"`
				Files           []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/subtitles", q, nil, &out); err != nil {
		return nil, err
	}
	res := []Result{}
	for _, d := range out.Data {
		a := d.Attributes
		for _, f := range a.Files {
			rel := a.Release
			if rel == "" {
				rel = f.FileName
			}
			res = append(res, Result{FileID: f.FileID, Language: a.Language, Release: rel, Downloads: a.DownloadCount,
				HearingImpaired: a.HearingImpaired, MovieHashMatch: a.MovieHashMatch})
		}
	}
	return res, nil
}

// Download fetches one subtitle file.
func (c *Client) Download(ctx context.Context, fileID int) ([]byte, error) {
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	var out struct {
		Link string `json:"link"`
	}
	if err := c.do(ctx, http.MethodPost, "/download", nil, map[string]int{"file_id": fileID}, &out); err != nil {
		return nil, err
	}
	u, err := url.Parse(out.Link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("OpenSubtitles returned an unexpected download link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("the subtitle could not be downloaded")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the subtitle could not be downloaded (%s)", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20+1))
	if err != nil || len(data) > 5<<20 {
		return nil, errors.New("the subtitle file is too large")
	}
	return data, nil
}

// MovieHash computes the OpenSubtitles hash: the file size plus the 64-bit
// little-endian sums of the first and last 64 KiB.
func MovieHash(r io.ReaderAt, size int64) (string, error) {
	const chunk = 64 << 10
	if size < chunk*2 {
		return "", errors.New("file too small")
	}
	h := uint64(size)
	buf := make([]byte, chunk)
	for _, off := range []int64{0, size - chunk} {
		if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
			return "", err
		}
		for i := 0; i < chunk; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", h), nil
}

// SafeName is a file name for a saved subtitle.
func SafeName(lang string, fileID int) string {
	l := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' {
			return r
		}
		return -1
	}, lang)
	if l == "" {
		l = "sub"
	}
	return strings.ToLower(l) + "-" + strconv.Itoa(fileID) + ".srt"
}
