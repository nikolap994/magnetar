package dlna

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Renderer is a UPnP media renderer (a TV) that Magnetar can push a video to.
type Renderer struct {
	Name       string
	UDN        string
	IP         net.IP
	controlURL string // absolute AVTransport control URL
}

// Discover finds media renderers (TVs) on the local networks by SSDP, within
// about three seconds. Results are de-duplicated by device id.
func Discover(ctx context.Context) ([]Renderer, error) {
	ctx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	target := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	const st = "urn:schemas-upnp-org:service:AVTransport:1"
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\n" +
		"ST: " + st + "\r\n\r\n"
	for i := 0; i < 2; i++ { // a couple of probes: UDP is lossy
		_, _ = conn.WriteToUDP([]byte(msg), target)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetReadDeadline(deadline)

	seenLoc := map[string]bool{}
	var locs []string
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline reached
		}
		if loc := headerValue(string(buf[:n]), "LOCATION"); loc != "" && !seenLoc[loc] {
			seenLoc[loc] = true
			locs = append(locs, loc)
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	seenUDN := map[string]bool{}
	var out []Renderer
	for _, loc := range locs {
		r, err := describe(ctx, client, loc)
		if err != nil || r.controlURL == "" || seenUDN[r.UDN] {
			continue
		}
		seenUDN[r.UDN] = true
		out = append(out, r)
	}
	return out, nil
}

type xmlService struct {
	Type       string `xml:"serviceType"`
	ControlURL string `xml:"controlURL"`
}

type xmlDevice struct {
	FriendlyName string       `xml:"friendlyName"`
	UDN          string       `xml:"UDN"`
	Services     []xmlService `xml:"serviceList>service"`
	Devices      []xmlDevice  `xml:"deviceList>device"`
}

// describe fetches and parses a device description, returning the renderer with
// its AVTransport control URL resolved to an absolute address.
func describe(ctx context.Context, client *http.Client, loc string) (Renderer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return Renderer{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Renderer{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Renderer{}, err
	}
	var root struct {
		Device xmlDevice `xml:"device"`
	}
	if err := xml.Unmarshal(body, &root); err != nil {
		return Renderer{}, err
	}
	base, _ := url.Parse(loc)
	name, udn, control := findAVTransport(root.Device)
	r := Renderer{Name: strings.TrimSpace(name), UDN: udn}
	if control != "" {
		if cu, err := url.Parse(control); err == nil {
			r.controlURL = base.ResolveReference(cu).String()
		}
	}
	if host, _, err := net.SplitHostPort(base.Host); err == nil {
		r.IP = net.ParseIP(host)
	}
	if r.Name == "" {
		r.Name = "TV"
	}
	return r, nil
}

// findAVTransport walks a device tree for the first AVTransport control URL,
// returning the owning device's name and id.
func findAVTransport(d xmlDevice) (name, udn, control string) {
	for _, s := range d.Services {
		if strings.Contains(s.Type, ":AVTransport:") {
			return d.FriendlyName, d.UDN, s.ControlURL
		}
	}
	for _, sub := range d.Devices {
		if n, u, c := findAVTransport(sub); c != "" {
			if n == "" {
				n = d.FriendlyName
			}
			return n, u, c
		}
	}
	return "", d.UDN, ""
}

func headerValue(resp, name string) string {
	for _, line := range strings.Split(resp, "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Play tells the renderer to load mediaURL and start playing it.
func (r Renderer) Play(ctx context.Context, mediaURL, title, mime string) error {
	meta := didlMetadata(mediaURL, title, mime)
	set := fmt.Sprintf(
		"<InstanceID>0</InstanceID><CurrentURI>%s</CurrentURI><CurrentURIMetaData>%s</CurrentURIMetaData>",
		xmlEscape(mediaURL), xmlEscape(meta))
	if err := r.soap(ctx, "SetAVTransportURI", set); err != nil {
		return err
	}
	return r.soap(ctx, "Play", "<InstanceID>0</InstanceID><Speed>1</Speed>")
}

// Stop tells the renderer to stop playback.
func (r Renderer) Stop(ctx context.Context) error {
	return r.soap(ctx, "Stop", "<InstanceID>0</InstanceID>")
}

func (r Renderer) soap(ctx context.Context, action, inner string) error {
	const svc = "urn:schemas-upnp-org:service:AVTransport:1"
	body := fmt.Sprintf(
		`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:%s xmlns:u="%s">%s</u:%s></s:Body></s:Envelope>`,
		action, svc, inner, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.controlURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+svc+"#"+action+`"`)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		if f := soapFault(msg); f != "" {
			return fmt.Errorf("the TV refused %s (%s)", action, f)
		}
		return fmt.Errorf("the TV refused %s (HTTP %d)", action, resp.StatusCode)
	}
	return nil
}

func didlMetadata(mediaURL, title, mime string) string {
	class := upnpClass(mime)
	// DLNA streaming flags so TVs accept a live, seekable HTTP stream.
	proto := "http-get:*:" + mime + ":DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	return fmt.Sprintf(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`+
		`<item id="0" parentID="-1" restricted="1"><dc:title>%s</dc:title><upnp:class>%s</upnp:class>`+
		`<res protocolInfo="%s">%s</res></item></DIDL-Lite>`,
		xmlEscapeText(title), class, xmlEscapeText(proto), xmlEscapeText(mediaURL))
}

func soapFault(b []byte) string {
	var env struct {
		Desc string `xml:"Body>Fault>detail>UPnPError>errorDescription"`
		Code string `xml:"Body>Fault>detail>UPnPError>errorCode"`
	}
	if xml.Unmarshal(b, &env) == nil && (env.Desc != "" || env.Code != "") {
		return strings.TrimSpace(env.Desc + " " + env.Code)
	}
	return ""
}

func xmlEscapeText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// MediaURL builds a content URL for a file, reachable by a renderer at its IP.
func (s *Server) MediaURL(hash string, index int, renderer net.IP) string {
	ip := localIP(renderer)
	if ip == nil {
		return ""
	}
	return fmt.Sprintf("http://%s/dlna/media/%s/%d", net.JoinHostPort(ip.String(), strconv.Itoa(s.port)), hash, index)
}

// CastDevices discovers TVs and remembers them so a later cast can resolve the
// chosen one.
func (s *Server) CastDevices(ctx context.Context) ([]Renderer, error) {
	rs, err := Discover(ctx)
	if err != nil {
		return nil, err
	}
	s.castMu.Lock()
	s.renderers = make(map[string]Renderer, len(rs))
	for _, r := range rs {
		s.renderers[r.UDN] = r
	}
	s.castMu.Unlock()
	return rs, nil
}

// CastTo plays a torrent file on the renderer with the given id.
func (s *Server) CastTo(ctx context.Context, deviceID, hash string, index int, title string) error {
	s.castMu.Lock()
	r, ok := s.renderers[deviceID]
	s.castMu.Unlock()
	if !ok {
		return fmt.Errorf("that TV is no longer available; search again")
	}
	mediaURL := s.MediaURL(hash, index, r.IP)
	if mediaURL == "" {
		return fmt.Errorf("could not work out this computer's address on the TV's network")
	}
	mime := MediaType(title)
	if mime == "" {
		mime = "video/mpeg"
	}
	if title == "" {
		title = "Magnetar"
	}
	return r.Play(ctx, mediaURL, title, mime)
}
