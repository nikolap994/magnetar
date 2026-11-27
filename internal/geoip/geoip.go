// Package geoip maps peer IP addresses to their country using a compact,
// public-domain database downloaded on demand. Only the two-letter country
// code is stored; the UI turns it into a flag. No per-lookup network calls
// are made, so peers' addresses never leave the computer.
package geoip

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

// SourceURL is the public-domain IP-to-country database (geo-whois-asn,
// CC0). Replaced in tests.
var SourceURL = "https://raw.githubusercontent.com/sapics/ip-location-db/main/dbip-country/dbip-country-ipv4.csv"

const (
	fileName    = "ip-country.csv"
	maxDownload = 32 << 20
	refreshAge  = 30 * 24 * time.Hour
)

type rangeV4 struct {
	start, end uint32
	cc         [2]byte
}

// DB is an in-memory IPv4 country lookup.
type DB struct {
	mu     sync.RWMutex
	ranges []rangeV4
}

// Lookup returns the ISO country code for an IP, or "" if unknown.
func (d *DB) Lookup(ip net.IP) string {
	v4 := ip.To4()
	if v4 == nil {
		return ""
	}
	n := binary.BigEndian.Uint32(v4)
	d.mu.RLock()
	defer d.mu.RUnlock()
	r := d.ranges
	i := sort.Search(len(r), func(i int) bool { return r[i].end >= n })
	if i < len(r) && r[i].start <= n && n <= r[i].end {
		cc := r[i].cc
		if cc[0] == 0 {
			return ""
		}
		return string(cc[:])
	}
	return ""
}

// Ready reports whether a database is loaded.
func (d *DB) Ready() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.ranges) > 0
}

// parse reads CSV rows of "start_ip,end_ip,CC".
func parse(r io.Reader) ([]rangeV4, error) {
	var out []rangeV4
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		a, rest, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		b, cc, ok := strings.Cut(rest, ",")
		if !ok {
			continue
		}
		cc = strings.TrimSpace(cc)
		start, end := net.ParseIP(a), net.ParseIP(b)
		if start == nil || end == nil || start.To4() == nil || end.To4() == nil || len(cc) != 2 {
			continue
		}
		var e rangeV4
		e.start = binary.BigEndian.Uint32(start.To4())
		e.end = binary.BigEndian.Uint32(end.To4())
		if e.end < e.start {
			continue
		}
		e.cc[0], e.cc[1] = upper(cc[0]), upper(cc[1])
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].end < out[j].end })
	return out, nil
}

func upper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}

// Load reads the cached database from dir, if present.
func (d *DB) Load(dir string) error {
	f, err := os.Open(filepath.Join(dir, fileName))
	if err != nil {
		return err
	}
	defer f.Close()
	ranges, err := parse(f)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.ranges = ranges
	d.mu.Unlock()
	return nil
}

// Stale reports whether the cached database is missing or older than a month.
func Stale(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, fileName))
	return err != nil || time.Since(fi.ModTime()) > refreshAge
}

// Download fetches the database into dir and loads it.
func (d *DB) Download(ctx context.Context, client *http.Client, dir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Magnetar")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("could not download the country database")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("could not download the country database")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxDownload {
		return errors.New("the country database is too large")
	}
	ranges, err := parse(strings.NewReader(string(data)))
	if err != nil || len(ranges) == 0 {
		return errors.New("the country database could not be read")
	}
	if err := config.WriteFileAtomic(filepath.Join(dir, fileName), data, 0o644); err != nil {
		return err
	}
	d.mu.Lock()
	d.ranges = ranges
	d.mu.Unlock()
	return nil
}
