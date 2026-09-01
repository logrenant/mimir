package refine

import (
	"errors"
	"strings"
)

var ErrRefineRejected = errors.New("refine: output failed safety/size checks")

// clampOutput ensures the text doesn't exceed maxTokens and hasn't hallucinated too much.
// It trims to the nearest boundary.
func clampOutput(text string, maxTokens int, inputLen int) (string, bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, ErrRefineRejected
	}

	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "ignore previous") || strings.HasPrefix(lowerText, "as an ai") {
		return "", false, ErrRefineRejected
	}

	if len(text) > inputLen && inputLen > 0 {
		return "", false, ErrRefineRejected
	}

	maxChars := maxTokens * 4
	truncated := false

	if len(text) > maxChars && maxTokens > 0 {
		text = text[:maxChars]
		truncated = true

		// Attempt to trim back to a logical boundary like a bullet point or newline
		lastNewline := strings.LastIndex(text, "\n")
		lastBullet1 := strings.LastIndex(text, "- ")
		lastBullet2 := strings.LastIndex(text, "* ")

		bestBoundary := -1
		if lastNewline > bestBoundary {
			bestBoundary = lastNewline
		}
		if lastBullet1 > bestBoundary {
			bestBoundary = lastBullet1
		}
		if lastBullet2 > bestBoundary {
			bestBoundary = lastBullet2
		}

		// If we found a reasonable boundary in the second half of the trimmed text, use it.
		if bestBoundary > len(text)/2 {
			text = strings.TrimSpace(text[:bestBoundary])
		}
	}

	if text == "" {
		return "", false, ErrRefineRejected
	}

	return text, truncated, nil
}
