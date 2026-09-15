package pipeline

import (
	"strconv"
	"strings"
)

func mergeResults(pages []RefinedPage, gaps []string, maxTokens int) Brief {
	var sources []Source
	var keyPoints []string
	seenPoints := make(map[string]bool)
	var truncated bool

	approxCharsTotal := 0
	maxChars := maxTokens * 4

	for i, p := range pages {
		if p.Truncated {
			truncated = true
		}

		sources = append(sources, Source{
			N:     i + 1,
			Title: p.Title,
			URL:   p.URL,
		})

		// Process lines, assuming the output is mostly bullets or text.
		lines := strings.Split(p.Markdown, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// basic normalization to detect near-duplicates
			norm := normalizeForDedup(line)
			if seenPoints[norm] {
				continue
			}

			// We only count actual content against the budget
			pointLen := len(line)
			if approxCharsTotal+pointLen > maxChars && maxChars > 0 {
				truncated = true
				break
			}

			seenPoints[norm] = true
			keyPoints = append(keyPoints, line)
			approxCharsTotal += pointLen
		}
	}

	summary := "Synthesized findings from " + strconv.Itoa(len(sources)) + " source(s)."

	return Brief{
		Summary:   summary,
		KeyPoints: keyPoints,
		Sources:   sources,
		Gaps:      gaps,
		Refined:   true,
		Truncated: truncated,
	}
}

func normalizeForDedup(s string) string {
	s = strings.ToLower(s)
	// Strip bullets
	s = strings.TrimPrefix(s, "- ")
	s = strings.TrimPrefix(s, "* ")
	s = strings.TrimPrefix(s, "• ")
	return strings.TrimSpace(s)
}
