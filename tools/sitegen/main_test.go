package main

import (
	"strings"
	"testing"
)

const sample = "# Changelog\n\n## [Unreleased]\n\n### Added\n- Not yet.\n\n" +
	"## [1.2.0] - 2026-12-01\n\n### Added\n- Queue with `max active`\n  downloads.\n- <b>escaped</b>\n\n### Fixed\n- A bug.\n\n" +
	"## [1.1.0] - 2026-11-01\n\n### Added\n- Old.\n"

func TestLatest(t *testing.T) {
	rel, err := Latest(sample)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.2.0" || rel.Date != "2026-12-01" || len(rel.Groups) != 2 {
		t.Fatalf("got %+v", rel)
	}
	if got := rel.Groups[0].Items[0]; got != "Queue with `max active` downloads." {
		t.Fatalf("continuation lines not joined: %q", got)
	}
	html := Notes(rel)
	if !strings.Contains(html, "<code>max active</code>") || strings.Contains(html, "<b>") || strings.Contains(html, "Old.") || strings.Contains(html, "Not yet") {
		t.Fatalf("bad notes: %s", html)
	}
}

func TestFillNeedsMarkers(t *testing.T) {
	rel, _ := Latest(sample)
	if _, err := Fill("<html></html>", rel); err == nil {
		t.Fatal("expected an error without markers")
	}
	out, err := Fill("v<!-- release:version --> <!-- release:notes -->", rel)
	if err != nil || !strings.HasPrefix(out, "v1.2.0 ") {
		t.Fatalf("%q %v", out, err)
	}
}
