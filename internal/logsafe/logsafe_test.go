package logsafe

import (
	"errors"
	"testing"
)

func TestString(t *testing.T) {
	if got := String("a\nlevel=ERROR msg=fake\r\x1b[31mb"); got != "a level=ERROR msg=fake [31mb" {
		t.Fatalf("%q", got)
	}
	if Err(nil) != "" || Err(errors.New("x\ny")) != "x y" {
		t.Fatal("Err")
	}
}
