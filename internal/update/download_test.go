package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease serves a GitHub-like "latest release" and its assets.
type fakeRelease struct {
	srv      *httptest.Server
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	asset    []byte // what the server returns for the asset
	signedOf []byte // what the signed SHA256SUMS describes
}

func newFakeRelease(t *testing.T, assetName string) *fakeRelease {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fakeRelease{priv: priv, pub: pub, asset: []byte("new magnetar build"), signedOf: []byte("new magnetar build")}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	base := f.srv.URL
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9", "body": "notes", "html_url": base + "/page",
			"assets": []map[string]any{
				{"name": assetName, "browser_download_url": base + "/asset"},
				{"name": "SHA256SUMS", "browser_download_url": base + "/sums"},
				{"name": "SHA256SUMS.sig", "browser_download_url": base + "/sig"},
			},
		})
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(f.asset) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { w.Write(f.sums(assetName)) })
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, base64.StdEncoding.EncodeToString(ed25519.Sign(f.priv, f.sums(assetName))))
	})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelease) sums(name string) []byte {
	h := sha256.Sum256(f.signedOf)
	return []byte(hex.EncodeToString(h[:]) + "  " + name + "\n")
}

func testUpdater(f *fakeRelease, method string) *Updater {
	u := New("0.1.0", slog.New(slog.NewTextHandler(io.Discard, nil)), func() bool { return true }, func() {})
	u.method = method
	u.pubKey = f.pub
	u.latestURL = f.srv.URL + "/latest"
	u.allowURL = func(*url.URL) error { return nil }
	u.client = f.srv.Client()
	return u
}

func TestDownloadVerifiesSignatureAndChecksum(t *testing.T) {
	u := &Updater{method: MethodLinuxPortable}
	name := u.assetName("9.9.9")
	f := newFakeRelease(t, name)
	up := testUpdater(f, MethodLinuxPortable)

	st, err := up.Check(context.Background())
	if err != nil || !st.Available || st.Latest != "9.9.9" {
		t.Fatalf("check: %+v %v", st, err)
	}
	path, err := up.download(context.Background(), up.release)
	if err != nil {
		t.Fatalf("valid release rejected: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "new magnetar build" {
		t.Fatalf("downloaded %q", b)
	}

	// The asset is swapped after signing: must be refused.
	f.asset = []byte("malicious build")
	if _, err := up.download(context.Background(), up.release); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered asset accepted: %v", err)
	}

	// Checksums signed with a different key: must be refused.
	f.asset = f.signedOf
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	f.priv = other
	if _, err := up.download(context.Background(), up.release); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("foreign signature accepted: %v", err)
	}
}

func TestInstallRefusedWithoutKey(t *testing.T) {
	f := newFakeRelease(t, fmt.Sprintf("magnetar_9.9.9_linux_%s.tar.gz", runtime.GOARCH))
	up := testUpdater(f, MethodLinuxPortable)
	up.pubKey = nil
	if _, err := up.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if up.Status().CanInstall {
		t.Fatal("build without a key must not offer installation")
	}
	if err := up.Install(context.Background()); err == nil {
		t.Fatal("install without a key must fail")
	}
}
