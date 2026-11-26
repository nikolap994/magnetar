package update

import (
	"strconv"
	"strings"
)

type version struct {
	major, minor, patch int
	pre                 string // e.g. "rc.1"; empty for a final release
}

// parseVersion parses MAJOR.MINOR.PATCH[-PRERELEASE] (an optional "v" prefix is allowed).
func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, _ := strings.Cut(s, "-")
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return version{}, false
		}
		n[i] = v
	}
	return version{n[0], n[1], n[2], pre}, true
}

// newer reports whether candidate is a newer version than current.
// Development builds (unparseable versions) never receive updates.
func newer(candidate, current string) bool {
	c, ok1 := parseVersion(candidate)
	r, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	if c.major != r.major {
		return c.major > r.major
	}
	if c.minor != r.minor {
		return c.minor > r.minor
	}
	if c.patch != r.patch {
		return c.patch > r.patch
	}
	// Same core version: a final release is newer than its pre-release.
	return r.pre != "" && c.pre == ""
}
