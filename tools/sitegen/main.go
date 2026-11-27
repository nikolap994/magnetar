// Command sitegen fills the website from CHANGELOG.md, so every release
// updates the site without editing HTML by hand.
//
//	sitegen -changelog CHANGELOG.md -page _site/index.html -changelog-page _site/changelog.html
//
// The home page contains the markers <!-- release:version -->,
// <!-- release:date --> and <!-- release:summary --> (a one-line summary of
// the newest release). The changelog page contains <!-- changelog:index -->
// and <!-- changelog:releases --> (every release in full). Markers are
// replaced in place.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	releaseHead = regexp.MustCompile(`^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})`)
	codeSpan    = regexp.MustCompile("`([^`]+)`")
)

// Release is one released section of the changelog.
type Release struct {
	Version, Date string
	Groups        []Group
}

// Group is a "### Added" style subsection.
type Group struct {
	Title string
	Items []string
}

func main() {
	changelog := flag.String("changelog", "CHANGELOG.md", "changelog to read")
	page := flag.String("page", "", "home page to fill in place")
	clPage := flag.String("changelog-page", "", "changelog page to fill in place (optional)")
	flag.Parse()
	if err := run(*changelog, *page, *clPage); err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

func run(changelog, page, clPage string) error {
	if page == "" {
		return errors.New("-page is required")
	}
	cl, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}
	rels, err := All(string(cl))
	if err != nil {
		return err
	}
	if err := fillFile(page, func(s string) (string, error) { return Fill(s, rels[0]) }); err != nil {
		return err
	}
	if clPage != "" {
		if err := fillFile(clPage, func(s string) (string, error) { return FillChangelog(s, rels) }); err != nil {
			return err
		}
	}
	fmt.Printf("sitegen: latest %s (%s), %d releases\n", rels[0].Version, rels[0].Date, len(rels))
	return nil
}

func fillFile(path string, fill func(string) (string, error)) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := fill(string(src))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// All returns every released (not Unreleased) section, newest first.
func All(changelog string) ([]Release, error) {
	var rels []Release
	var cur *Release
	var item *strings.Builder
	flush := func() {
		if item != nil && cur != nil && len(cur.Groups) > 0 {
			g := &cur.Groups[len(cur.Groups)-1]
			g.Items = append(g.Items, strings.TrimSpace(item.String()))
		}
		item = nil
	}
	sc := bufio.NewScanner(strings.NewReader(changelog))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \r")
		if strings.HasPrefix(line, "## ") {
			flush()
			cur = nil
			if m := releaseHead.FindStringSubmatch(line); m != nil {
				rels = append(rels, Release{Version: m[1], Date: m[2]})
				cur = &rels[len(rels)-1]
			}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			flush()
			cur.Groups = append(cur.Groups, Group{Title: strings.TrimSpace(line[4:])})
		case strings.HasPrefix(line, "- "):
			flush()
			item = &strings.Builder{}
			item.WriteString(line[2:])
		case item != nil && strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "":
			item.WriteString(" " + strings.TrimSpace(line))
		default:
			flush()
		}
	}
	flush()
	if len(rels) == 0 {
		return nil, errors.New("no released version found in the changelog")
	}
	return rels, nil
}

// Latest returns the newest released section.
func Latest(changelog string) (Release, error) {
	rels, err := All(changelog)
	if err != nil {
		return Release{}, err
	}
	return rels[0], nil
}

// inline renders changelog text as safe HTML (only `code` spans are kept).
func inline(s string) string {
	s = html.EscapeString(s)
	return codeSpan.ReplaceAllString(s, "<code>$1</code>")
}

// prettyDate turns 2026-11-27 into "27 November 2026".
func prettyDate(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("2 January 2006")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Summary describes a release in one line, e.g. "18 new features, 2
// performance improvements and 1 fix".
func Summary(rel Release) string {
	var parts []string
	for _, g := range rel.Groups {
		n := len(g.Items)
		if n == 0 {
			continue
		}
		switch strings.ToLower(g.Title) {
		case "added":
			parts = append(parts, plural(n, "new feature", "new features"))
		case "changed":
			parts = append(parts, plural(n, "improvement", "improvements"))
		case "performance":
			parts = append(parts, plural(n, "performance improvement", "performance improvements"))
		case "fixed":
			parts = append(parts, plural(n, "fix", "fixes"))
		case "security":
			parts = append(parts, plural(n, "security update", "security updates"))
		default:
			parts = append(parts, plural(n, "change", "changes"))
		}
	}
	switch len(parts) {
	case 0:
		return "Maintenance release"
	case 1:
		return upperFirst(parts[0])
	}
	return upperFirst(strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1])
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Fill replaces the home page markers.
func Fill(page string, rel Release) (string, error) {
	for _, m := range []string{"<!-- release:version -->", "<!-- release:summary -->"} {
		if !strings.Contains(page, m) {
			return "", fmt.Errorf("marker %s not found", m)
		}
	}
	page = strings.ReplaceAll(page, "<!-- release:version -->", rel.Version)
	page = strings.ReplaceAll(page, "<!-- release:date -->", html.EscapeString(prettyDate(rel.Date)))
	return strings.ReplaceAll(page, "<!-- release:summary -->", html.EscapeString(Summary(rel))), nil
}

// FillChangelog replaces the changelog page markers with every release.
func FillChangelog(page string, rels []Release) (string, error) {
	for _, m := range []string{"<!-- changelog:index -->", "<!-- changelog:releases -->"} {
		if !strings.Contains(page, m) {
			return "", fmt.Errorf("marker %s not found", m)
		}
	}
	var idx, body strings.Builder
	for i, r := range rels {
		id := "v" + r.Version
		fmt.Fprintf(&idx, "<li><a href=\"#%s\">%s</a><span>%s</span></li>\n", id, r.Version, html.EscapeString(prettyDate(r.Date)))
		latest := ""
		if i == 0 {
			latest = ` <span class="latest">Latest</span>`
		}
		fmt.Fprintf(&body, "<article class=\"release\" id=\"%s\">\n<header><h2>Magnetar %s%s</h2><p class=\"rel-date\">%s · %s</p></header>\n",
			id, r.Version, latest, html.EscapeString(prettyDate(r.Date)), html.EscapeString(Summary(r)))
		for _, g := range r.Groups {
			if len(g.Items) == 0 {
				continue
			}
			fmt.Fprintf(&body, "<section class=\"rel-group %s\"><h3>%s</h3><ul>\n", groupClass(g.Title), html.EscapeString(g.Title))
			for _, it := range g.Items {
				fmt.Fprintf(&body, "<li>%s</li>\n", inline(it))
			}
			body.WriteString("</ul></section>\n")
		}
		fmt.Fprintf(&body, "<p class=\"rel-links\"><a href=\"https://github.com/nikolap994/magnetar/releases/tag/v%s\">Downloads for %s</a></p>\n</article>\n", r.Version, r.Version)
	}
	page = strings.ReplaceAll(page, "<!-- changelog:index -->", idx.String())
	return strings.ReplaceAll(page, "<!-- changelog:releases -->", body.String()), nil
}

func groupClass(title string) string {
	switch strings.ToLower(title) {
	case "added", "changed", "performance", "fixed", "security":
		return strings.ToLower(title)
	}
	return "other"
}
