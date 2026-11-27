// Package tlsutil creates the self-signed certificate Magnetar uses for HTTPS
// phone access.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

const (
	certFile = "https-cert.pem"
	keyFile  = "https-key.pem"
	validFor = 10 * 365 * 24 * time.Hour
)

// Load returns the certificate in dir, creating one for the given host
// addresses when none exists or it is about to expire.
func Load(dir string, hosts []string) (tls.Certificate, error) {
	cp, kp := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	if c, err := tls.LoadX509KeyPair(cp, kp); err == nil && c.Leaf != nil && time.Until(c.Leaf.NotAfter) > 30*24*time.Hour {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Magnetar", Organization: []string{"Magnetar (self-signed)"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, hn, strings.ToLower(hn)+".local")
	}
	for _, h := range append(hosts, "127.0.0.1", "::1") {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := config.WriteFileAtomic(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := config.WriteFileAtomic(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(cp, kp)
}

// Fingerprint is the SHA-256 fingerprint of the certificate, as browsers show it.
func Fingerprint(c tls.Certificate) string {
	if len(c.Certificate) == 0 {
		return ""
	}
	sum := sha256.Sum256(c.Certificate[0])
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// Config is a modern TLS server configuration for the certificate.
func Config(c tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{c}, MinVersion: tls.VersionTLS12}
}
