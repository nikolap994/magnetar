package finish

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	tor := Torrent{Name: `My "Show"; rm -rf ~`, Hash: "abc", SavePath: "/dl", Category: "TV", Tags: []string{"a", "b"}}
	args, err := Expand(`notify-send 'Done: %N' "%F" --tags=%G 100%%`, tor)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"notify-send", `Done: My "Show"; rm -rf ~`, filepath.Join("/dl", `My "Show"; rm -rf ~`), "--tags=a,b", "100%"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", args)
	}
	if _, err := Expand(`echo "open`, tor); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}

func TestCancel(t *testing.T) {
	ran := ""
	r := New(slog.New(slog.DiscardHandler), func() { ran = "quit" })
	r.execute = func(a string) error { ran = a; return nil }
	r.Schedule("shutdown")
	if a, until := r.Pending(); a != "shutdown" || until.IsZero() {
		t.Fatalf("pending %q", a)
	}
	r.Cancel()
	if a, _ := r.Pending(); a != "" || ran != "" {
		t.Fatalf("after cancel: pending %q ran %q", a, ran)
	}
}
