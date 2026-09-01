package mcp

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the goat binary version, surfaced by the diagnostics tool, by
// /healthz, and by the desktop app's menu-bar status line. It is the release
// this commit ships as, without the tag's leading "v" — the UI adds that. An
// operator comparing a running daemon with a GitHub release must see the same
// number in both places, so a release bumps this constant and tags the same
// commit `v<Version>`.
const Version = "1.1.1"

// SDKVersion is the pinned modelcontextprotocol/go-sdk release (see go.mod).
const SDKVersion = "v1.7.0"

const version = Version

// Server wraps the MCP SDK server.
type Server struct {
	mcpServer *mcp.Server
	cfg       config.Config
	registry  *Registry
}

// NewServer constructs an SDK server with name 'goat-mcp', a version string, and stdio transport.
func NewServer(cfg config.Config) *Server {
	sdkServer := mcp.NewServer(&mcp.Implementation{
		Name:    "goat-mcp",
		Version: version,
	}, nil)

	return &Server{
		mcpServer: sdkServer,
		cfg:       cfg,
		registry:  newRegistry(sdkServer),
	}
}

// Registry returns the server's tool registry.
func (s *Server) Registry() *Registry {
	return s.registry
}

// MCPHandler serves MCP over HTTP against the same server — and therefore the
// same Registry, and therefore the same finalize.go choke-point — that Run
// serves over stdio.
//
// This is what makes "two binaries, one engine" checkable rather than a claim:
// the daemon cannot expose a tool set that has drifted from goat-mcp's, because
// there is only one.
func (s *Server) MCPHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcpServer },
		nil,
	)
}

// CloseMCPSessions ends every live session on this server.
//
// An MCP-over-HTTP session outlives the request that created it — that is the
// point of it — so a client that disappears without deleting its session leaves
// the server half of that conversation alive for the rest of the process. The
// daemon calls this during shutdown; the stdio entrypoint has exactly one
// session and does not need it.
func (s *Server) CloseMCPSessions() {
	for session := range s.mcpServer.Sessions() {
		if err := session.Close(); err != nil {
			slog.Warn("closing MCP session", "error", err)
		}
	}
}

// Run serves until ctx is cancelled, then shuts down cleanly.
func (s *Server) Run(ctx context.Context) error {
	slog.Info("Starting MCP server", "name", "goat-mcp", "version", version)
	defer slog.Info("Shutting down MCP server")

	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}
