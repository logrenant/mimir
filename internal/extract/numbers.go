package extract

import (
	"regexp"
	"strconv"
	"strings"
)

var floatRe = regexp.MustCompile(`\d[\d,]*\.?\d*`)

// ParseFloatLoose parses the first loosely-formatted number found in s,
// stripping thousands-separator commas and any surrounding text/currency
// symbols — e.g. "$1,299.99" → 1299.99, "4.5 out of 5 stars" → 4.5.
func ParseFloatLoose(s string) (float64, bool) {
	m := floatRe.FindString(s)
	if m == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

var compactRe = regexp.MustCompile(`(?i)([\d,]*\.?\d+)\s*([kmb])?`)

// ParseCompactInt parses a loosely-formatted, possibly-compact integer:
// "12,345" → 12345, "1.2M" → 1200000, "340" → 340. Common on social-media
// follower/like/view counts.
func ParseCompactInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	m := compactRe.FindStringSubmatch(s)
	if m == nil || m[1] == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(m[2]) {
	case "k":
		f *= 1_000
	case "m":
		f *= 1_000_000
	case "b":
		f *= 1_000_000_000
	}
	return int(f), true
}
