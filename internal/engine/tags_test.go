package engine

import (
	"fmt"
	"testing"

	"github.com/nikolap994/magnetar/internal/config"
)

func TestTags(t *testing.T) {
	if _, err := CleanTags([]string{"a,b"}); err == nil {
		t.Fatal("comma accepted")
	}
	got, _ := CleanTags([]string{" 4K ", "anime", "4k", ""})
	if len(got) != 2 || got[0] != "4K" || got[1] != "anime" {
		t.Fatalf("clean: %v", got)
	}
	e := offlineEngine(t, config.Defaults())
	h1, _ := e.Add(AddOptions{Magnet: fmt.Sprintf("magnet:?xt=urn:btih:%040x", 1), Paused: true, Tags: []string{"movies"}})
	h2, _ := e.Add(AddOptions{Magnet: fmt.Sprintf("magnet:?xt=urn:btih:%040x", 2), Paused: true})
	if err := e.EditTags([]string{"keep", "4K"}, []string{"movies"}, h1, h2); err != nil {
		t.Fatal(err)
	}
	if tags := e.Tags(); len(tags) != 2 || tags[0] != "4K" || tags[1] != "keep" {
		t.Fatalf("all tags %v", tags)
	}
	for _, s := range e.List() {
		if len(s.Tags) != 2 {
			t.Fatalf("%s tags %v", s.Hash, s.Tags)
		}
	}
}
