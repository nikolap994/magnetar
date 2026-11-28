// Package dlna makes Magnetar's downloads playable on smart TVs and media
// players on the home network: a small UPnP AV MediaServer (SSDP discovery,
// ContentDirectory browsing and HTTP streaming, also while downloading).
package dlna

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/logsafe"
)

// Media types shared with TVs (no subtitles, documents or programs).
var mediaTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mkv": "video/x-matroska", ".avi": "video/x-msvideo",
	".mov": "video/quicktime", ".webm": "video/webm", ".ts": "video/mp2t", ".m2ts": "video/mp2t",
	".mpg": "video/mpeg", ".mpeg": "video/mpeg", ".wmv": "video/x-ms-wmv",
	".mp3": "audio/mpeg", ".flac": "audio/flac", ".m4a": "audio/mp4", ".aac": "audio/aac", ".ogg": "audio/ogg",
	".opus": "audio/ogg", ".wav": "audio/wav",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
}

// MediaType returns the MIME type of a shareable file, or "".
func MediaType(name string) string { return mediaTypes[strings.ToLower(path.Ext(name))] }

func upnpClass(mime string) string {
	switch {
	case strings.HasPrefix(mime, "video/"):
		return "object.item.videoItem"
	case strings.HasPrefix(mime, "audio/"):
		return "object.item.audioItem.musicTrack"
	default:
		return "object.item.imageItem.photo"
	}
}

// Server is the media server. Start it with Run.
type Server struct {
	eng  *engine.Engine
	log  *slog.Logger
	port int
	uuid string
	name string

	castMu    sync.Mutex
	renderers map[string]Renderer // last discovered TVs, by device id
}

// New creates a media server announced as name on port.
func New(eng *engine.Engine, log *slog.Logger, port int, name string) *Server {
	host, _ := os.Hostname()
	sum := sha256.Sum256([]byte(fmt.Sprintf("magnetar-dlna-%s-%d", host, port)))
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	return &Server{eng: eng, log: log, port: port, uuid: uuid, name: name}
}

// Run serves until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(s.port)))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dlna/desc.xml", s.handleDesc)
	mux.HandleFunc("GET /dlna/cds.xml", func(w http.ResponseWriter, r *http.Request) { writeXML(w, cdsSCPD) })
	mux.HandleFunc("GET /dlna/cms.xml", func(w http.ResponseWriter, r *http.Request) { writeXML(w, cmsSCPD) })
	mux.HandleFunc("POST /dlna/control/cds", s.handleCDS)
	mux.HandleFunc("POST /dlna/control/cms", s.handleCMS)
	mux.HandleFunc("GET /dlna/media/{hash}/{index}", s.handleMedia)
	mux.HandleFunc("HEAD /dlna/media/{hash}/{index}", s.handleMedia)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go s.ssdp(ctx)
	go func() { //nolint:gosec // runs after ctx is done; shutdown needs its own deadline
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	s.log.Info("media server for TVs is on", "port", s.port)
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	_, _ = io.WriteString(w, body)
}

func (s *Server) handleDesc(w http.ResponseWriter, r *http.Request) {
	writeXML(w, fmt.Sprintf(deviceDesc, xmlEscape(s.name), s.uuid))
}

func xmlEscape(v string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(v))
	return b.String()
}

// handleMedia streams a torrent file to the TV, with ranges for seeking.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx < 0 {
		http.NotFound(w, r)
		return
	}
	f, err := s.eng.OpenFile(r.Context(), r.PathValue("hash"), idx)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	mime := MediaType(f.Name)
	if mime == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0")
	http.ServeContent(w, r, f.Name, time.Time{}, f)
}

// --- ContentDirectory ---

type soapBrowse struct {
	Body struct {
		Browse struct {
			ObjectID       string `xml:"ObjectID"`
			BrowseFlag     string `xml:"BrowseFlag"`
			StartingIndex  int    `xml:"StartingIndex"`
			RequestedCount int    `xml:"RequestedCount"`
		} `xml:",any"`
	} `xml:"Body"`
}

func (s *Server) handleCDS(w http.ResponseWriter, r *http.Request) {
	action := r.Header.Get("SOAPACTION")
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	switch {
	case strings.Contains(action, "#Browse"):
	case strings.Contains(action, "#GetSystemUpdateID"):
		soapReply(w, "GetSystemUpdateID", "urn:schemas-upnp-org:service:ContentDirectory:1", "<Id>"+strconv.FormatInt(time.Now().Unix()/60, 10)+"</Id>")
		return
	case strings.Contains(action, "#GetSearchCapabilities"):
		soapReply(w, "GetSearchCapabilities", "urn:schemas-upnp-org:service:ContentDirectory:1", "<SearchCaps></SearchCaps>")
		return
	case strings.Contains(action, "#GetSortCapabilities"):
		soapReply(w, "GetSortCapabilities", "urn:schemas-upnp-org:service:ContentDirectory:1", "<SortCaps></SortCaps>")
		return
	default:
		http.Error(w, "unsupported action", http.StatusInternalServerError)
		return
	}
	var req soapBrowse
	if err := xml.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	b := req.Body.Browse
	didl, n, total := s.browse(r.Host, b.ObjectID, b.BrowseFlag == "BrowseMetadata", b.StartingIndex, b.RequestedCount)
	var res strings.Builder
	res.WriteString("<Result>")
	_ = xml.EscapeText(&res, []byte(didl))
	fmt.Fprintf(&res, "</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>%d</UpdateID>", n, total, time.Now().Unix()/60)
	soapReply(w, "Browse", "urn:schemas-upnp-org:service:ContentDirectory:1", res.String())
}

type entry struct {
	id, parent, title, class, mime, url string
	size                                int64
	container                           bool
	children                            int
}

func (e entry) didl() string {
	if e.container {
		return fmt.Sprintf(`<container id="%s" parentID="%s" restricted="1" childCount="%d"><dc:title>%s</dc:title><upnp:class>object.container.storageFolder</upnp:class></container>`,
			xmlEscape(e.id), xmlEscape(e.parent), e.children, xmlEscape(e.title))
	}
	return fmt.Sprintf(`<item id="%s" parentID="%s" restricted="1"><dc:title>%s</dc:title><upnp:class>%s</upnp:class><res size="%d" protocolInfo="http-get:*:%s:DLNA.ORG_OP=01;DLNA.ORG_CI=0">%s</res></item>`,
		xmlEscape(e.id), xmlEscape(e.parent), xmlEscape(e.title), e.class, e.size, e.mime, xmlEscape(e.url))
}

// children lists the root (one folder per torrent with media) or a torrent's
// media files.
func (s *Server) children(host, id string) (self entry, kids []entry, ok bool) {
	if id == "0" {
		self = entry{id: "0", parent: "-1", title: s.name, container: true}
		for _, t := range s.eng.List() {
			d, err := s.eng.Detail(t.Hash)
			if err != nil {
				continue
			}
			n := 0
			for _, f := range d.Files {
				if MediaType(f.Path) != "" && f.Priority != engine.PrioritySkip {
					n++
				}
			}
			if n > 0 && t.State != engine.StatePaused && t.State != engine.StateQueued {
				kids = append(kids, entry{id: "t:" + t.Hash, parent: "0", title: t.Name, container: true, children: n})
			}
		}
		self.children = len(kids)
		return self, kids, true
	}
	hash, ok := strings.CutPrefix(id, "t:")
	if !ok {
		return self, nil, false
	}
	d, err := s.eng.Detail(hash)
	if err != nil {
		return self, nil, false
	}
	self = entry{id: id, parent: "0", title: d.Name, container: true}
	for _, f := range d.Files {
		mime := MediaType(f.Path)
		if mime == "" || f.Priority == engine.PrioritySkip {
			continue
		}
		kids = append(kids, entry{id: fmt.Sprintf("f:%s:%d", hash, f.Index), parent: id, title: path.Base(f.Path),
			class: upnpClass(mime), mime: mime, size: f.Size, url: fmt.Sprintf("http://%s/dlna/media/%s/%d", host, hash, f.Index)})
	}
	self.children = len(kids)
	return self, kids, true
}

func (s *Server) browse(host, id string, metadata bool, start, count int) (didl string, n, total int) {
	var items []entry
	if metadata {
		if strings.HasPrefix(id, "f:") {
			parts := strings.Split(id, ":")
			if len(parts) == 3 {
				_, kids, _ := s.children(host, "t:"+parts[1])
				for _, k := range kids {
					if k.id == id {
						items = []entry{k}
					}
				}
			}
		} else if self, _, ok := s.children(host, id); ok {
			items = []entry{self}
		}
	} else {
		_, items, _ = s.children(host, id)
	}
	total = len(items)
	if start > total {
		start = total
	}
	items = items[start:]
	if count > 0 && count < len(items) {
		items = items[:count]
	}
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`)
	for _, it := range items {
		b.WriteString(it.didl())
	}
	b.WriteString("</DIDL-Lite>")
	return b.String(), len(items), total
}

func (s *Server) handleCMS(w http.ResponseWriter, r *http.Request) {
	action := r.Header.Get("SOAPACTION")
	switch {
	case strings.Contains(action, "#GetProtocolInfo"):
		var src []string
		seen := map[string]bool{}
		for _, m := range mediaTypes {
			if !seen[m] {
				seen[m] = true
				src = append(src, "http-get:*:"+m+":*")
			}
		}
		soapReply(w, "GetProtocolInfo", "urn:schemas-upnp-org:service:ConnectionManager:1",
			"<Source>"+xmlEscape(strings.Join(src, ","))+"</Source><Sink></Sink>")
	case strings.Contains(action, "#GetCurrentConnectionIDs"):
		soapReply(w, "GetCurrentConnectionIDs", "urn:schemas-upnp-org:service:ConnectionManager:1", "<ConnectionIDs>0</ConnectionIDs>")
	default:
		http.Error(w, "unsupported action", http.StatusInternalServerError)
	}
}

func soapReply(w http.ResponseWriter, action, service, inner string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("EXT", "")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:%sResponse xmlns:u="%s">%s</u:%sResponse></s:Body></s:Envelope>`,
		action, service, inner, action)
}

// --- SSDP discovery ---

var ssdpAddr = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}

var searchTargets = []string{"upnp:rootdevice", "urn:schemas-upnp-org:device:MediaServer:1",
	"urn:schemas-upnp-org:service:ContentDirectory:1", "urn:schemas-upnp-org:service:ConnectionManager:1"}

func (s *Server) usn(st string) string {
	if st == "uuid:"+s.uuid {
		return st
	}
	return "uuid:" + s.uuid + "::" + st
}

// localIP picks this computer's address on the network of remote.
func localIP(remote net.IP) net.IP {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: remote, Port: 1900})
	if err != nil {
		return nil
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP
}

func (s *Server) location(ip net.IP) string {
	return fmt.Sprintf("http://%s/dlna/desc.xml", net.JoinHostPort(ip.String(), strconv.Itoa(s.port)))
}

func (s *Server) ssdp(ctx context.Context) {
	conn, err := net.ListenMulticastUDP("udp4", nil, ssdpAddr)
	if err != nil {
		s.log.Warn("TV discovery (SSDP) unavailable", "err", err)
		return
	}
	go func() { <-ctx.Done(); _ = conn.Close() }()
	s.notify("ssdp:alive")
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				s.notify("ssdp:byebye")
				return
			case <-t.C:
				s.notify("ssdp:alive")
			}
		}
	}()
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg := string(buf[:n])
		if !strings.HasPrefix(msg, "M-SEARCH") || !strings.Contains(strings.ToLower(msg), "ssdp:discover") {
			continue
		}
		st := header(msg, "ST")
		var targets []string
		switch st {
		case "ssdp:all":
			targets = append([]string{"uuid:" + s.uuid}, searchTargets...)
		case "uuid:" + s.uuid:
			targets = []string{st}
		default:
			for _, t := range searchTargets {
				if t == st {
					targets = []string{st}
				}
			}
		}
		ip := localIP(from.IP)
		if ip == nil {
			continue
		}
		for _, t := range targets {
			resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nDATE: %s\r\nEXT:\r\nLOCATION: %s\r\nSERVER: Magnetar/%s UPnP/1.0 DLNADOC/1.50\r\nST: %s\r\nUSN: %s\r\n\r\n",
				time.Now().UTC().Format(http.TimeFormat), s.location(ip), engine.Version, t, s.usn(t))
			if _, err := conn.WriteToUDP([]byte(resp), from); err != nil {
				s.log.Debug("ssdp reply", "err", logsafe.Err(err))
			}
		}
	}
}

func (s *Server) notify(nts string) {
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagMulticast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			c, err := net.DialUDP("udp4", &net.UDPAddr{IP: ipn.IP}, ssdpAddr)
			if err != nil {
				continue
			}
			for _, t := range append([]string{"uuid:" + s.uuid}, searchTargets...) {
				msg := fmt.Sprintf("NOTIFY * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: %s\r\nNT: %s\r\nNTS: %s\r\nSERVER: Magnetar/%s UPnP/1.0 DLNADOC/1.50\r\nUSN: %s\r\n\r\n",
					s.location(ipn.IP), t, nts, engine.Version, s.usn(t))
				_, _ = c.Write([]byte(msg))
			}
			_ = c.Close()
		}
	}
}

func header(msg, name string) string {
	for _, line := range strings.Split(msg, "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}
