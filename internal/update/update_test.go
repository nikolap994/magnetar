package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cand, cur string
		want      bool
	}{
		{"0.1.2", "0.1.1", true},
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.1", "0.1.1", false},
		{"0.1.0", "0.1.1", false},
		{"0.1.1", "0.1.1-rc.1", true},
		{"0.1.1-rc.2", "0.1.1", false},
		{"v0.3.0", "0.2.0", true},
		{"0.2.0", "0.1.0-dev", true},
		{"0.2.0", "dev", false}, // dev builds never update
		{"garbage", "0.1.0", false},
	}
	for _, c := range cases {
		if got := newer(c.cand, c.cur); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.cand, c.cur, got, c.want)
		}
	}
}

func TestSignatureVerification(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte("abc  magnetar_0.2.0_amd64.deb\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)) + "\n")

	if err := verifySignature(pub, sums, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifySignature(otherPub, sums, sig); err == nil {
		t.Fatal("signature from a different key accepted")
	}
	tampered := append([]byte(nil), sums...)
	tampered[0] = 'x'
	if err := verifySignature(pub, tampered, sig); err == nil {
		t.Fatal("tampered checksums accepted")
	}
	if err := verifySignature(pub, sums, []byte("not base64!")); err == nil {
		t.Fatal("malformed signature accepted")
	}
	if err := verifySignature(nil, sums, sig); err == nil {
		t.Fatal("verification without a key must fail")
	}
}

func TestLookupSum(t *testing.T) {
	sum := "930006620f3c831993e90f29570742f4b592ad03b3c247c65847b6c9b149e185"
	list := []byte(sum + "  magnetar_0.1.0_amd64.deb\n" +
		"ff94c16e3ceaf1bd6dbd309a34877291b36c0059e033d7a7f6c176a5e3dcdc41 *magnetar_0.1.0_windows_amd64_setup.exe\n")
	if got, err := lookupSum(list, "magnetar_0.1.0_amd64.deb"); err != nil || got != sum {
		t.Fatalf("lookupSum = %q, %v", got, err)
	}
	if _, err := lookupSum(list, "magnetar_0.1.0_windows_amd64_setup.exe"); err != nil {
		t.Fatalf("binary-mode entry not found: %v", err)
	}
	if _, err := lookupSum(list, "evil.exe"); err == nil {
		t.Fatal("unlisted file accepted")
	}
	if _, err := lookupSum([]byte("zz  file\n"), "file"); err == nil {
		t.Fatal("malformed checksum accepted")
	}
}

func TestCheckURL(t *testing.T) {
	ok := []string{
		"https://github.com/nikolap994/magnetar/releases/download/v1/x",
		"https://objects.githubusercontent.com/github-production-release-asset/1",
		"https://release-assets.githubusercontent.com/x",
		"https://api.github.com/repos/nikolap994/magnetar/releases/latest",
	}
	bad := []string{
		"http://github.com/x",
		"https://evil.example/x",
		"https://github.com.evil.example/x",
		"https://githubusercontent.com.evil.example/x",
		"file:///etc/passwd",
	}
	for _, s := range ok {
		u, _ := url.Parse(s)
		if err := checkURL(u); err != nil {
			t.Errorf("%s rejected: %v", s, err)
		}
	}
	for _, s := range bad {
		u, _ := url.Parse(s)
		if err := checkURL(u); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("#!new-binary")

	tgz := filepath.Join(dir, "m.tar.gz")
	f, _ := os.Create(tgz)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "magnetar_0.2.0_linux_amd64/README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.WriteHeader(&tar.Header{Name: "magnetar_0.2.0_linux_amd64/magnetar", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg})
	tw.Write(payload)
	tw.Close()
	gz.Close()
	f.Close()

	out := filepath.Join(dir, "out")
	if err := extractBinary(tgz, "magnetar", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != string(payload) {
		t.Fatalf("tar.gz: got %q", b)
	}

	zp := filepath.Join(dir, "m.zip")
	zf, _ := os.Create(zp)
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("magnetar_0.2.0_windows_amd64/magnetar.exe")
	w.Write(payload)
	zw.Close()
	zf.Close()
	out2 := filepath.Join(dir, "out2")
	if err := extractBinary(zp, "magnetar.exe", out2); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out2); string(b) != string(payload) {
		t.Fatalf("zip: got %q", b)
	}

	if err := extractBinary(zp, "missing.exe", filepath.Join(dir, "x")); err == nil {
		t.Fatal("missing binary not reported")
	}
}

func TestReleaseKeyEmbedded(t *testing.T) {
	if !Configured() {
		t.Fatal("release public key is missing or malformed; updates could not be verified")
	}
}

func TestPauseHidesOffers(t *testing.T) {
	u := New("1.0.0", slog.New(slog.DiscardHandler), func() bool { return true }, func() {})
	if u.Status().Paused {
		t.Fatal("not paused by default")
	}
	until := time.Now().Add(time.Hour)
	u.SetPause(func() time.Time { return until })
	if s := u.Status(); !s.Paused || !s.PausedUntil.Equal(until) {
		t.Fatalf("status %+v", s)
	}
	u.SetPause(func() time.Time { return time.Now().Add(-time.Minute) })
	if u.Status().Paused {
		t.Fatal("a pause in the past must have ended")
	}
}
