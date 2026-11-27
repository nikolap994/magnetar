package rss

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Rule decides which feed items are downloaded automatically.
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// MustContain: every word must appear (case-insensitive); "|" separates
	// alternatives, e.g. "1080p|2160p show name". With UseRegex it is a
	// regular expression instead.
	MustContain    string `json:"mustContain"`
	MustNotContain string `json:"mustNotContain"`
	UseRegex       bool   `json:"useRegex"`
	// SmartEpisodes downloads each episode (S01E02 / 1x02) only once, so
	// repacks or other qualities of the same episode are skipped.
	SmartEpisodes bool     `json:"smartEpisodes"`
	Feeds         []string `json:"feeds,omitempty"` // feed IDs; empty = all feeds
	Category      string   `json:"category,omitempty"`
	SavePath      string   `json:"savePath,omitempty"`
	Paused        bool     `json:"paused"`
	// Episodes already downloaded by this rule ("S01E02").
	Episodes []string `json:"episodes,omitempty"`
}

// Validate checks the rule's patterns.
func (r *Rule) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 100 {
		return fmt.Errorf("rule names must be 1-100 characters")
	}
	if len(r.MustContain) > 1000 || len(r.MustNotContain) > 1000 {
		return fmt.Errorf("rule %q: patterns are too long", r.Name)
	}
	if r.UseRegex {
		for _, p := range []string{r.MustContain, r.MustNotContain} {
			if _, err := regexp.Compile("(?i)" + p); err != nil {
				return fmt.Errorf("rule %q: invalid regular expression: %v", r.Name, err)
			}
		}
	}
	return nil
}

// Matches reports whether a title passes the rule's filters.
func (r *Rule) Matches(title string) bool {
	if strings.TrimSpace(r.MustContain) == "" {
		return false // a rule without a filter would download everything
	}
	if r.UseRegex {
		ok, _ := regexp.MatchString("(?i)"+r.MustContain, title)
		if !ok {
			return false
		}
		if r.MustNotContain != "" {
			if bad, _ := regexp.MatchString("(?i)"+r.MustNotContain, title); bad {
				return false
			}
		}
		return true
	}
	t := strings.ToLower(title)
	if !wordsMatch(t, r.MustContain) {
		return false
	}
	if strings.TrimSpace(r.MustNotContain) != "" {
		for _, alt := range strings.Split(r.MustNotContain, "|") {
			for _, w := range strings.Fields(strings.ToLower(alt)) {
				if strings.Contains(t, w) {
					return false
				}
			}
		}
	}
	return true
}

// wordsMatch: any "|" alternative whose words all appear.
func wordsMatch(title, pattern string) bool {
	for _, alt := range strings.Split(pattern, "|") {
		words := strings.Fields(strings.ToLower(alt))
		if len(words) == 0 {
			continue
		}
		all := true
		for _, w := range words {
			if !strings.Contains(title, w) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

var (
	episodeSE = regexp.MustCompile(`(?i)\bS(\d{1,3})[ ._-]?E(\d{1,4})\b`)
	episodeX  = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
)

// Episode returns "S01E02" for titles that name an episode, or "".
func Episode(title string) string {
	m := episodeSE.FindStringSubmatch(title)
	if m == nil {
		m = episodeX.FindStringSubmatch(title)
	}
	if m == nil {
		return ""
	}
	s, _ := strconv.Atoi(m[1])
	e, _ := strconv.Atoi(m[2])
	return fmt.Sprintf("S%02dE%02d", s, e)
}

// AppliesTo reports whether the rule watches the feed.
func (r *Rule) AppliesTo(feedID string) bool {
	if len(r.Feeds) == 0 {
		return true
	}
	for _, f := range r.Feeds {
		if f == feedID {
			return true
		}
	}
	return false
}
