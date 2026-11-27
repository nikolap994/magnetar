// Package logsafe cleans untrusted text (torrent names, feed titles, error
// messages) before it is written to the log, so it cannot forge log lines.
package logsafe

import "strings"

// String replaces line breaks and drops other control characters.
func String(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

// Err is String for an error message ("" for nil).
func Err(err error) string {
	if err == nil {
		return ""
	}
	return String(err.Error())
}
