package mcp

import (
	"context"
	"encoding/json"
)

// Tool represents an MCP tool definition.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Handle(ctx context.Context, args json.RawMessage) (any, error)
}


