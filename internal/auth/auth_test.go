package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong password") {
		t.Fatal("invalid password accepted")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("garbage hash accepted")
	}
}

func TestPasswordTooShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error for short password")
	}
}

func TestLaunchTokenSingleUse(t *testing.T) {
	k, err := LoadKeyring(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tok := k.NewLaunchToken()
	if !k.ConsumeLaunchToken(tok) {
		t.Fatal("fresh token rejected")
	}
	if k.ConsumeLaunchToken(tok) {
		t.Fatal("token reused")
	}
	if k.ConsumeLaunchToken(tok[:len(tok)-2] + "AA") {
		t.Fatal("tampered token accepted")
	}
}

func TestTokensBoundToSecret(t *testing.T) {
	a, _ := LoadKeyring(t.TempDir())
	b, _ := LoadKeyring(t.TempDir())
	if b.ConsumeLaunchToken(a.NewLaunchToken()) {
		t.Fatal("token from another keyring accepted")
	}
	if b.ValidSession(a.NewSession()) {
		t.Fatal("session from another keyring accepted")
	}
}

func TestSession(t *testing.T) {
	k, _ := LoadKeyring(t.TempDir())
	s := k.NewSession()
	if !k.ValidSession(s) {
		t.Fatal("valid session rejected")
	}
	for _, bad := range []string{"", ".", "abc.def", s + "x", "x" + s} {
		if k.ValidSession(bad) {
			t.Fatalf("invalid session %q accepted", bad)
		}
	}
}

func TestKeyringPersists(t *testing.T) {
	dir := t.TempDir()
	a, _ := LoadKeyring(dir)
	b, _ := LoadKeyring(dir)
	if !b.ValidSession(a.NewSession()) {
		t.Fatal("keyring not persisted")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	for range maxFailures {
		if !l.Allowed("1.2.3.4") {
			t.Fatal("blocked too early")
		}
		l.Fail("1.2.3.4")
	}
	if l.Allowed("1.2.3.4") {
		t.Fatal("not blocked after max failures")
	}
	if !l.Allowed("5.6.7.8") {
		t.Fatal("other address blocked")
	}
	l.Reset("1.2.3.4")
	if !l.Allowed("1.2.3.4") {
		t.Fatal("reset did not unblock")
	}
}
