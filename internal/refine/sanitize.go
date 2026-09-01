package refine

import (
	"strings"
)

// sanitizePage strips boilerplate and truncates large documents to a byte budget.
// It also escapes <DATA_BLOCK> to neutralize injection attempts breaking out of the fence.
func sanitizePage(md string, byteBudget int) (string, bool) {
	// Simple boilerplate stripping: we will process line by line.
	// If a block of lines looks heavily like navigation or footer links, we can drop them.
	// For MVP, we will do some very basic heuristic filtering:
	// - Drop lines that are exactly "Accept Cookies" or similar exact matches.
	// - We want to be very conservative so we don't drop real content.
	lines := strings.Split(md, "\n")
	var cleanedLines []string

	for _, line := range lines {
		tLine := strings.TrimSpace(line)
		tLower := strings.ToLower(tLine)
		
		// Drop extremely common boilerplate exact matches
		if tLower == "accept cookies" || tLower == "privacy policy" || tLower == "terms of service" {
			continue
		}
		// If line is just social sharing buttons
		if tLower == "share this:" || tLower == "share on facebook" || tLower == "share on twitter" {
			continue
		}

		cleanedLines = append(cleanedLines, line)
	}

	cleanMd := strings.Join(cleanedLines, "\n")

	// Escape fence markers so user content cannot break out of our <DATA_BLOCK>
	cleanMd = strings.ReplaceAll(cleanMd, "<DATA_BLOCK>", "&lt;DATA_BLOCK&gt;")
	cleanMd = strings.ReplaceAll(cleanMd, "</DATA_BLOCK>", "&lt;/DATA_BLOCK&gt;")

	truncated := false
	if len(cleanMd) > byteBudget && byteBudget > 0 {
		truncated = true
		// Keep head and tail, drop the middle
		half := byteBudget / 2
		head := cleanMd[:half]
		tail := cleanMd[len(cleanMd)-half:]
		cleanMd = head + "\n\n... [TRUNCATED] ...\n\n" + tail
	}

	return cleanMd, truncated
}
