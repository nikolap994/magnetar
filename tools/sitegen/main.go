// Command sitegen fills the website with the latest released version and its
// release notes, taken from CHANGELOG.md, so the site is updated by every
// release without editing HTML by hand.
//
//	sitegen -changelog CHANGELOG.md -page _site/index.html
//
// The page contains the markers <!-- release:version --> and
// <!-- release:notes -->, which are replaced in place.
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
	page := flag.String("page", "", "HTML page to fill in place")
	flag.Parse()
	if err := run(*changelog, *page); err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

func run(changelog, page string) error {
	if page == "" {
		return errors.New("-page is required")
	}
	cl, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}
	rel, err := Latest(string(cl))
	if err != nil {
		return err
	}
	src, err := os.ReadFile(page)
	if err != nil {
		return err
	}
	out, err := Fill(string(src), rel)
	if err != nil {
		return err
	}
	fmt.Printf("sitegen: %s (%s), %d groups\n", rel.Version, rel.Date, len(rel.Groups))
	return os.WriteFile(page, []byte(out), 0o644)
}

// Latest returns the newest released (not Unreleased) section of a Keep a Changelog file.
func Latest(changelog string) (Release, error) {
	var rel Release
	in := false
	var item *strings.Builder
	flush := func() {
		if item != nil && len(rel.Groups) > 0 {
			g := &rel.Groups[len(rel.Groups)-1]
			g.Items = append(g.Items, strings.TrimSpace(item.String()))
		}
		item = nil
	}
	sc := bufio.NewScanner(strings.NewReader(changelog))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \r")
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			if m := releaseHead.FindStringSubmatch(line); m != nil {
				in, rel.Version, rel.Date = true, m[1], m[2]
			}
			continue
		}
		if !in {
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			flush()
			rel.Groups = append(rel.Groups, Group{Title: strings.TrimSpace(line[4:])})
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
	if rel.Version == "" {
		return rel, errors.New("no released version found in the changelog")
	}
	return rel, nil
}

// inline renders changelog text as safe HTML (only `code` spans are kept).
func inline(s string) string {
	s = html.EscapeString(s)
	return codeSpan.ReplaceAllString(s, "<code>$1</code>")
}

// Notes renders a release as HTML for the website.
func Notes(rel Release) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<p class=\"rel-meta\">Version %s, released %s. <a href=\"https://github.com/nikolap994/magnetar/releases/tag/v%s\">Full release notes</a></p>\n",
		rel.Version, rel.Date, rel.Version)
	b.WriteString("<div class=\"rel-groups\">\n")
	for _, g := range rel.Groups {
		if len(g.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "<div class=\"rel-group\"><h3>%s</h3><ul>\n", html.EscapeString(g.Title))
		for _, it := range g.Items {
			fmt.Fprintf(&b, "<li>%s</li>\n", inline(it))
		}
		b.WriteString("</ul></div>\n")
	}
	b.WriteString("</div>")
	return b.String()
}

// Fill replaces the release markers in the page.
func Fill(page string, rel Release) (string, error) {
	for _, m := range []string{"<!-- release:version -->", "<!-- release:notes -->"} {
		if !strings.Contains(page, m) {
			return "", fmt.Errorf("marker %s not found in the page", m)
		}
	}
	page = strings.ReplaceAll(page, "<!-- release:version -->", rel.Version)
	return strings.ReplaceAll(page, "<!-- release:notes -->", Notes(rel)), nil
}
