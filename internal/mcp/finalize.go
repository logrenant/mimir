package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrIsolationViolation is returned when a response is not properly refined,
	// lacks the required provenance metadata, or contains raw content signatures.
	ErrIsolationViolation = errors.New("mcp: response not refined / contains raw content")

	// ErrResponseTooLarge is returned when a response exceeds its tool's size budget.
	ErrResponseTooLarge = errors.New("mcp: response exceeds tool size budget")
)

// RefinedResponse is implemented by tools that return refined page content.
type RefinedResponse interface {
	IsRefined() bool
	SizeBudgetTokens() int
}

// MetadataResponse is implemented by tools that return metadata only.
type MetadataResponse interface {
	MetadataOnly() bool
	SizeBudgetTokens() int
}

// finalizeResponse is the single fail-closed choke-point (SD-2 / SD-7). Every
// successful tool result passes through here on its way to the MCP transport.
func finalizeResponse(tool Tool, v any) (any, error) {
	name := tool.Name()

	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	budget := 0
	isMetadata := false
	isRefined := false

	if mr, ok := v.(MetadataResponse); ok {
		isMetadata = mr.MetadataOnly()
		budget = mr.SizeBudgetTokens()
	} else if rr, ok := v.(RefinedResponse); ok {
		isRefined = rr.IsRefined()
		budget = rr.SizeBudgetTokens()
	}

	if !isMetadata && !isRefined {
		return nil, fmt.Errorf("%w (tool %q)", ErrIsolationViolation, name)
	}

	// Estimate token size as chars / 4
	estimatedTokens := len(b) / 4
	if budget > 0 && estimatedTokens > budget {
		return nil, fmt.Errorf("%w: tool %q produced ~%d tokens, budget %d", ErrResponseTooLarge, name, estimatedTokens, budget)
	}

	// Scan for raw HTML / Script signatures
	strData := string(b)
	if strings.Contains(strData, "<html") || strings.Contains(strData, "\\u003chtml") ||
		strings.Contains(strData, "<script") || strings.Contains(strData, "\\u003cscript") ||
		containsLongBase64(strData) {
		return nil, fmt.Errorf("%w (tool %q)", ErrIsolationViolation, name)
	}

	return v, nil
}

func containsLongBase64(s string) bool {
	// A simple heuristic for unrefined image payloads: looking for base64 blocks
	// commonly found in raw data URIs. E.g. "data:image/png;base64,iVBORw0K..."
	// 500 characters of unbroken base64-like characters without spaces.
	// Since json marshaling keeps strings intact, we can just look for data:image
	if strings.Contains(s, "data:image/") && strings.Contains(s, ";base64,") {
		return true
	}
	return false
}
