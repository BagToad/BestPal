package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/markusmobius/go-dateparser"
)

// Minimal alias table. Treat ST/DT the same → map to one IANA zone.
var tzAliases = map[string]string{
	// North America
	"PT": "America/Los_Angeles", "PST": "America/Los_Angeles", "PDT": "America/Los_Angeles",
	"MT": "America/Denver", "MST": "America/Denver", "MDT": "America/Denver",
	"CT": "America/Chicago", "CST": "America/Chicago", "CDT": "America/Chicago",
	"ET": "America/New_York", "EST": "America/New_York", "EDT": "America/New_York",

	// UTC/GMT
	"UTC": "Etc/UTC", "GMT": "Etc/UTC",

	// Common extras
	"BST": "Europe/London", // UK summer time (DST-aware via Europe/London)
	"IST": "Asia/Kolkata",  // choose India here; note global ambiguity
}

// Extract a trailing/standalone TZ token like "PST", "ET", "America/Los_Angeles".
var tzTokenRe = regexp.MustCompile(`(?i)\b([A-Za-z]{2,4}|[A-Za-z]+\/[A-Za-z_]+)\b`)

// ResolveMessyToLocation returns an IANA *time.Location from messy user input.
func ResolveMessyToLocation(userTZ string) *time.Location {
	if userTZ == "" {
		loc, _ := time.LoadLocation("Etc/UTC")
		return loc
	}
	raw := strings.TrimSpace(userTZ)

	// Try direct IANA
	if loc, err := time.LoadLocation(raw); err == nil {
		return loc
	}

	// Try alias
	if name, ok := tzAliases[strings.ToUpper(raw)]; ok {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}

	// A few human labels
	switch strings.ToLower(raw) {
	case "pacific", "pacific time":
		if loc, err := time.LoadLocation("America/Los_Angeles"); err == nil {
			return loc
		}
	case "mountain", "mountain time":
		if loc, err := time.LoadLocation("America/Denver"); err == nil {
			return loc
		}
	case "central", "central time":
		if loc, err := time.LoadLocation("America/Chicago"); err == nil {
			return loc
		}
	case "eastern", "eastern time":
		if loc, err := time.LoadLocation("America/New_York"); err == nil {
			return loc
		}
	}

	loc, _ := time.LoadLocation("Etc/UTC")
	return loc
}

// FindTZ tries to pull a TZ token out of an arbitrary string.
func FindTZ(s string) (loc *time.Location, token string) {
	// Walk matches from the end so "Jan 1 12:00 PST" prefers the PST at the end.
	matches := tzTokenRe.FindAllString(s, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if l := ResolveMessyToLocation(matches[i]); l != nil {
			return l, matches[i]
		}
	}
	return nil, ""
}

// ResolveDateToUnixTimestamp parses a date/time string and returns a Unix timestamp.
// If the string contains a TZ token (e.g., PST/EDT/ET or an IANA ID),
// we interpret the parsed wall-clock in that zone so DST is chosen correctly.
func ResolveDateToUnixTimestamp(dateString string) (int64, error) {
	dateString = strings.TrimSpace(dateString)
	if dateString == "" {
		return 0, fmt.Errorf("empty date/time")
	}

	// Try to detect a timezone token anywhere in the string.
	loc, _ := FindTZ(dateString)

	// Let dateparser do the heavy lifting for flexible formats.
	dt, err := dateparser.Parse(nil, dateString)
	if err != nil {
		return 0, fmt.Errorf("unable to parse date/time format: %w", err)
	}

	// If we found a timezone token, reinterpret the wall-clock components in that zone.
	if loc != nil {
		// Strip the token to avoid accidental double shifting if dateparser included an offset.
		// Then rebuild the time from components in the target location.
		y, m, d := dt.Time.Date()
		hh, mm, ss := dt.Time.Clock()
		rebased := time.Date(y, m, d, hh, mm, ss, dt.Time.Nanosecond(), loc)

		// Edge case: spring-forward "missing" local times (e.g., 02:30 that never occurs)
		// are normalized by time.Date. If you want to surface an error instead,
		// you could reparse and compare components here.

		return rebased.Unix(), nil
	}

	// No timezone in the string → keep what dateparser decided.
	return dt.Time.Unix(), nil
}
