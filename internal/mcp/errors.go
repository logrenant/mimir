package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Sentinel-to-actionable-message mapping is owned by each tool handler (see
// internal/tools/*.go), keeping internal/mcp free of dependencies on the data
// packages (crawl/refine/pipeline) — the dependency direction in
// docs/ARCHITECTURE.md §2. The tool returns an already-actionable error; the
// registry passes it straight through.

// errorResult returns an MCP CallToolResult structured as an error.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: err.Error(),
			},
		},
	}
}
