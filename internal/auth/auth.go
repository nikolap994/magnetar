// Package auth implements web UI authentication: signed session cookies,
// short-lived launch tokens and Argon2id password hashing.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/nikolap994/magnetar/internal/config"
)

const (
	SessionTTL     = 30 * 24 * time.Hour
	LaunchTokenTTL = 2 * time.Minute
)

// Keyring holds the instance secret used to sign tokens.
type Keyring struct {
	secret []byte

	mu   sync.Mutex
	used map[string]time.Time // spent launch tokens
}

// LoadKeyring reads or creates dir/secret.key (0600).
func LoadKeyring(dir string) (*Keyring, error) {
	path := filepath.Join(dir, "secret.key")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		b = make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if err := config.WriteFileAtomic(path, b, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("%s is corrupt; delete it to regenerate", path)
	}
	return &Keyring{secret: b, used: map[string]time.Time{}}, nil
}

func (k *Keyring) mac(parts ...string) []byte {
	h := hmac.New(sha256.New, k.secret)
	h.Write([]byte(strings.Join(parts, "\x00")))
	return h.Sum(nil)
}

var b64 = base64.RawURLEncoding

// NewLaunchToken returns a single-use token that logs the browser in.
// Any process that can read the secret file (i.e. the same OS user) can mint one.
func (k *Keyring) NewLaunchToken() string {
	nonce := make([]byte, 12)
	rand.Read(nonce)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	n := b64.EncodeToString(nonce)
	return ts + "." + n + "." + b64.EncodeToString(k.mac("launch", ts, n))
}

// ConsumeLaunchToken validates and spends a launch token.
func (k *Keyring) ConsumeLaunchToken(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, k.mac("launch", parts[0], parts[1])) {
		return false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(ts, 0))
	if age > LaunchTokenTTL || age < -30*time.Second {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	for t, exp := range k.used {
		if now.After(exp) {
			delete(k.used, t)
		}
	}
	if _, spent := k.used[tok]; spent {
		return false
	}
	k.used[tok] = now.Add(LaunchTokenTTL + time.Minute)
	return true
}

// NewSession returns a signed session value.
func (k *Keyring) NewSession() string {
	payload := make([]byte, 8+16)
	binary.BigEndian.PutUint64(payload, uint64(time.Now().Add(SessionTTL).Unix()))
	rand.Read(payload[8:])
	p := b64.EncodeToString(payload)
	return p + "." + b64.EncodeToString(k.mac("session", p))
}

// ValidSession checks a session value's signature and expiry.
func (k *Keyring) ValidSession(v string) bool {
	p, s, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	sig, err := b64.DecodeString(s)
	if err != nil || !hmac.Equal(sig, k.mac("session", p)) {
		return false
	}
	payload, err := b64.DecodeString(p)
	if err != nil || len(payload) != 24 {
		return false
	}
	exp := time.Unix(int64(binary.BigEndian.Uint64(payload)), 0)
	return time.Now().Before(exp)
}

// Argon2id parameters (OWASP recommended minimums, with headroom).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns an encoded Argon2id hash.
func HashPassword(pw string) (string, error) {
	if len(pw) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	if len(pw) > 1024 {
		return "", errors.New("password is too long")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against an encoded Argon2id hash in constant time.
func VerifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || len(pw) > 1024 {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	if m > 1<<20 || t > 20 || p == 0 {
		return false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Limiter throttles failed login attempts per client address.
type Limiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func NewLimiter() *Limiter { return &Limiter{attempts: map[string][]time.Time{}} }

const (
	maxFailures   = 5
	failureWindow = 5 * time.Minute
)

// Allowed reports whether addr may try again.
func (l *Limiter) Allowed(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-failureWindow)
	var keep []time.Time
	for _, t := range l.attempts[addr] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.attempts, addr)
	} else {
		l.attempts[addr] = keep
	}
	return len(keep) < maxFailures
}

// Fail records a failed attempt.
func (l *Limiter) Fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[addr] = append(l.attempts[addr], time.Now())
}

// Reset clears failures after a success.
func (l *Limiter) Reset(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, addr)
}
