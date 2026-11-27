package tlsutil

import (
	"strings"
	"testing"
)

func TestLoadCreatesAndReuses(t *testing.T) {
	dir := t.TempDir()
	c1, err := Load(dir, []string{"192.168.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(c1) != Fingerprint(c2) || strings.Count(Fingerprint(c1), ":") != 31 {
		t.Fatalf("fingerprint %q vs %q", Fingerprint(c1), Fingerprint(c2))
	}
}
