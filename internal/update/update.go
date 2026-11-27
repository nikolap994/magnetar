// Package update checks GitHub Releases for new Magnetar versions, downloads
// the right artifact for this installation and verifies it before installing.
//
// Trust model: every release publishes SHA256SUMS and SHA256SUMS.sig, an
// Ed25519 signature made in CI with a key whose public half is compiled into
// Magnetar (see key.go). A download is used only if the signature over
// SHA256SUMS verifies with that key and the file's SHA-256 matches the signed
// list, so neither a compromised download host nor a modified release asset
// can make Magnetar run foreign code.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	repo          = "nikolap994/magnetar"
	latestURL     = "https://api.github.com/repos/" + repo + "/releases/latest"
	checkInterval = 24 * time.Hour
	firstCheck    = 2 * time.Minute
	maxAssetSize  = 256 << 20
	maxSmallFile  = 1 << 20
)

// Installation methods.
const (
	MethodWindowsSetup    = "windows-setup"    // installed with magnetar-setup.exe
	MethodWindowsPortable = "windows-portable" // unpacked .zip
	MethodDeb             = "deb"              // installed from the .deb package
	MethodLinuxPortable   = "linux-portable"   // unpacked .tar.gz
	MethodFlatpak         = "flatpak"          // updated by Flathub / the software center
	MethodContainer       = "container"        // updated by pulling a new image
	MethodUnsupported     = "unsupported"
)

// Status is reported to the UI.
type Status struct {
	Current     string    `json:"current"`
	Latest      string    `json:"latest,omitempty"`
	Available   bool      `json:"available"`
	Notes       string    `json:"notes,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"publishedAt,omitzero"`
	CheckedAt   time.Time `json:"checkedAt,omitzero"`
	State       string    `json:"state"` // idle, checking, downloading, installing, error
	Error       string    `json:"error,omitempty"`
	Method      string    `json:"method"`
	CanInstall  bool      `json:"canInstall"`
	AutoCheck   bool      `json:"autoCheck"`
	// Paused: the user paused updates; no automatic checks and no offers.
	Paused      bool      `json:"paused"`
	PausedUntil time.Time `json:"pausedUntil,omitzero"`
}

type release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Updater runs periodic checks and performs installs.
type Updater struct {
	current string
	method  string
	exe     string
	log     *slog.Logger
	client  *http.Client
	pubKey  ed25519.PublicKey
	// latestURL and allowURL are fixed in production and replaced in tests.
	latestURL string
	allowURL  func(*url.URL) error
	// restart shuts Magnetar down after an update has been staged.
	restart func()
	// pausedUntil reports when paused updates resume (zero = not paused).
	pausedUntil func() time.Time

	mu      sync.Mutex
	status  Status
	release *release
	auto    func() bool
}

// New creates an updater for the running version. autoCheck reports whether
// periodic checks are enabled; restart stops the app so the new version can start.
func New(current string, logger *slog.Logger, autoCheck func() bool, restart func()) *Updater {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	u := &Updater{
		current:   current,
		exe:       exe,
		log:       logger,
		pubKey:    publicKey(),
		latestURL: latestURL,
		allowURL:  checkURL,
		restart:   restart,
		auto:      autoCheck,
	}
	u.client = &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return u.allowURL(req.URL)
		},
	}
	u.method = detectMethod(exe)
	u.status = Status{Current: current, State: "idle", Method: u.method}
	return u
}

// checkURL only allows HTTPS downloads from GitHub's own hosts.
func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("refusing non-HTTPS URL %s", u.Redacted())
	}
	h := strings.ToLower(u.Hostname())
	if h == "github.com" || h == "api.github.com" || strings.HasSuffix(h, ".githubusercontent.com") {
		return nil
	}
	return fmt.Errorf("refusing download from unexpected host %q", h)
}

// Run performs periodic checks until ctx is cancelled.
func (u *Updater) Run(ctx context.Context) {
	if _, ok := parseVersion(u.current); !ok {
		u.log.Info("update checks disabled for development build", "version", u.current)
		return
	}
	if u.method == MethodFlatpak || u.method == MethodContainer {
		return // Flathub or the container image delivers updates
	}
	t := time.NewTimer(firstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if u.auto() && !u.paused() {
			if _, err := u.Check(ctx); err != nil {
				u.log.Warn("update check failed", "err", err)
			}
		}
		t.Reset(checkInterval)
	}
}

// SetPause tells the updater when paused updates resume (zero = not paused).
func (u *Updater) SetPause(until func() time.Time) { u.pausedUntil = until }

func (u *Updater) paused() bool {
	if u.pausedUntil == nil {
		return false
	}
	t := u.pausedUntil()
	return !t.IsZero() && time.Now().Before(t)
}

// Status returns the current update status.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.status
	s.AutoCheck = u.auto()
	if u.paused() {
		s.Paused, s.PausedUntil = true, u.pausedUntil()
	}
	s.CanInstall = s.Available && u.method != MethodUnsupported && u.method != MethodFlatpak && u.method != MethodContainer && len(u.pubKey) == ed25519.PublicKeySize &&
		s.State != "downloading" && s.State != "installing"
	return s
}

func (u *Updater) setState(state, errMsg string) {
	u.mu.Lock()
	u.status.State = state
	u.status.Error = errMsg
	u.mu.Unlock()
}

// Check asks GitHub for the latest release.
func (u *Updater) Check(ctx context.Context) (Status, error) {
	u.setState("checking", "")
	rel, err := u.fetchLatest(ctx)
	u.mu.Lock()
	u.status.CheckedAt = time.Now().UTC()
	if err != nil {
		u.status.State = "error"
		u.status.Error = err.Error()
		u.mu.Unlock()
		return u.Status(), err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	u.release = rel
	u.status.Latest = latest
	u.status.Notes = rel.Body
	u.status.URL = rel.HTMLURL
	u.status.PublishedAt = rel.PublishedAt
	u.status.Available = newer(latest, u.current)
	u.status.State = "idle"
	u.status.Error = ""
	u.mu.Unlock()
	if u.status.Available {
		u.log.Info("update available", "current", u.current, "latest", latest)
	}
	return u.Status(), nil
}

func (u *Updater) fetchLatest(ctx context.Context) (*release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.latestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Magnetar-Updater/"+u.current)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSmallFile)).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.Draft || rel.Prerelease {
		return nil, errors.New("latest release is not a stable release")
	}
	if _, ok := parseVersion(strings.TrimPrefix(rel.TagName, "v")); !ok {
		return nil, fmt.Errorf("unexpected release tag %q", rel.TagName)
	}
	return &rel, nil
}

// Install downloads, verifies and installs the available update, then
// restarts Magnetar. It returns once the update is staged.
func (u *Updater) Install(ctx context.Context) error {
	u.mu.Lock()
	rel, avail := u.release, u.status.Available
	busy := u.status.State == "downloading" || u.status.State == "installing"
	u.mu.Unlock()
	if busy {
		return errors.New("an update is already in progress")
	}
	if rel == nil || !avail {
		return errors.New("no update available")
	}
	if len(u.pubKey) != ed25519.PublicKeySize {
		return errors.New("this build cannot verify updates; download the new version from GitHub")
	}
	if u.method == MethodFlatpak {
		return errors.New("this copy is installed from Flathub; update it in your software center")
	}
	if u.method == MethodContainer {
		return errors.New("this copy runs in a container; pull the new image to update")
	}
	if u.method == MethodUnsupported {
		return errors.New("automatic updates are not supported for this installation; download the new version from GitHub")
	}
	u.setState("downloading", "")
	path, err := u.download(ctx, rel)
	if err != nil {
		u.setState("error", err.Error())
		return err
	}
	u.setState("installing", "")
	if err := u.apply(path); err != nil {
		u.setState("error", err.Error())
		return err
	}
	return nil
}

// assetName is the release file for this installation.
func (u *Updater) assetName(version string) string {
	arch := runtime.GOARCH
	switch u.method {
	case MethodWindowsSetup:
		return fmt.Sprintf("magnetar_%s_windows_%s_setup.exe", version, arch)
	case MethodWindowsPortable:
		return fmt.Sprintf("magnetar_%s_windows_%s.zip", version, arch)
	case MethodDeb:
		return fmt.Sprintf("magnetar_%s_%s.deb", version, arch)
	case MethodLinuxPortable:
		return fmt.Sprintf("magnetar_%s_linux_%s.tar.gz", version, arch)
	}
	return ""
}

func (u *Updater) download(ctx context.Context, rel *release) (string, error) {
	version := strings.TrimPrefix(rel.TagName, "v")
	want := u.assetName(version)
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.URL
	}
	for _, n := range []string{want, "SHA256SUMS", "SHA256SUMS.sig"} {
		if urls[n] == "" {
			return "", fmt.Errorf("release %s has no %s", rel.TagName, n)
		}
	}

	sums, err := u.get(ctx, urls["SHA256SUMS"], maxSmallFile)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	sig, err := u.get(ctx, urls["SHA256SUMS.sig"], 4096)
	if err != nil {
		return "", fmt.Errorf("download signature: %w", err)
	}
	if err := verifySignature(u.pubKey, sums, sig); err != nil {
		return "", err
	}
	expected, err := lookupSum(sums, want)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "magnetar-update-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, want)
	if err := u.getToFile(ctx, urls[want], path, expected); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	u.log.Info("update downloaded and verified", "file", want)
	return path, nil
}

func (u *Updater) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	resp, err := u.open(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file is unexpectedly large")
	}
	return b, nil
}

func (u *Updater) open(ctx context.Context, rawURL string) (*http.Response, error) {
	pu, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := u.allowURL(pu); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Magnetar-Updater/"+u.current)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	return resp, nil
}

func (u *Updater) getToFile(ctx context.Context, rawURL, path, expectedHex string) error {
	resp, err := u.open(ctx, rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAssetSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxAssetSize {
		return errors.New("update file is unexpectedly large")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != expectedHex {
		return fmt.Errorf("checksum mismatch for %s: refusing to install", filepath.Base(path))
	}
	return nil
}

// verifySignature checks the base64 Ed25519 signature over SHA256SUMS.
func verifySignature(pub ed25519.PublicKey, sums, sigFile []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("updater has no signing key configured")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigFile)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("malformed update signature")
	}
	if !ed25519.Verify(pub, sums, sig) {
		return errors.New("update signature is invalid: refusing to install")
	}
	return nil
}

// lookupSum finds name's SHA-256 in a sha256sum-formatted list.
func lookupSum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			sum := strings.ToLower(fields[0])
			if _, err := hex.DecodeString(sum); err != nil || len(sum) != 64 {
				return "", fmt.Errorf("malformed checksum for %s", name)
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s is not listed in the signed checksums", name)
}

var errUnsupported = errors.New("not supported on this platform")
