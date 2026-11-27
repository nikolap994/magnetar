package main

import (
	"strings"
	"testing"
)

const sample = "# Changelog\n\n## [Unreleased]\n\n### Added\n- Not yet.\n\n" +
	"## [1.2.0] - 2026-12-01\n\n### Added\n- Queue with `max active`\n  downloads.\n- <b>escaped</b>\n\n### Fixed\n- A bug.\n\n" +
	"## [1.1.0] - 2026-11-01\n\n### Added\n- Old.\n"

func TestAll(t *testing.T) {
	rels, err := All(sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 || rels[0].Version != "1.2.0" || rels[1].Version != "1.1.0" || len(rels[0].Groups) != 2 {
		t.Fatalf("got %+v", rels)
	}
	if got := rels[0].Groups[0].Items[0]; got != "Queue with `max active` downloads." {
		t.Fatalf("continuation lines not joined: %q", got)
	}
	if got := Summary(rels[0]); got != "2 new features and 1 fix" {
		t.Fatalf("summary %q", got)
	}
}

func TestFillPages(t *testing.T) {
	rels, _ := All(sample)
	home, err := Fill("v<!-- release:version --> <!-- release:date --> <!-- release:summary -->", rels[0])
	if err != nil || home != "v1.2.0 1 December 2026 2 new features and 1 fix" {
		t.Fatalf("%q %v", home, err)
	}
	if _, err := Fill("<html></html>", rels[0]); err == nil {
		t.Fatal("expected an error without markers")
	}
	page, err := FillChangelog("<!-- changelog:index -->|<!-- changelog:releases -->", rels)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="#v1.2.0"`, `id="v1.1.0"`, "<code>max active</code>", "Latest", "Old."} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(page, "<b>") || strings.Contains(page, "Not yet") {
		t.Fatalf("unsafe or unreleased content: %s", page)
	}
}
