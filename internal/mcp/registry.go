package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Registry manages the registration and dispatching of tools.
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]Tool
	server *mcp.Server
	// skills is nil until SetSkills is called. Nil is not "skills are off":
	// a tool that declares one still fails closed, because a wiring bug must
	// not read as a process where the mandate happens not to apply.
	skills *skillGate
}

// newRegistry creates a new Registry.
func newRegistry(server *mcp.Server) *Registry {
	return &Registry{
		tools:  make(map[string]Tool),
		server: server,
	}
}

// SetSkills gives the registry the source its skilled tools need. Called once
// at wiring time, before any tool is registered.
func (r *Registry) SetSkills(src SkillSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills = newSkillGate(src)
}

// skillGateRef reads the gate under the lock, so a registry configured on one
// goroutine and dispatched on another is not a race.
func (r *Registry) skillGateRef() *skillGate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skills
}

// Names returns the registered tool names, sorted.
//
// The canonical tool set is assembled conditionally — a tool whose credential
// is absent is not registered (tools.RegisterAll) — so "which tools does this
// process actually offer" has to be answerable without standing up a
// transport and a client session.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Register adds a new tool to the registry and wires it to the MCP SDK server.
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := t.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = t

	// Compile JSON schema to validate arguments later
	schemaStr := string(t.InputSchema())
	compiler := jsonschema.NewCompiler()
	schemaURL := fmt.Sprintf("%s.json", name)
	if err := compiler.AddResource(schemaURL, strings.NewReader(schemaStr)); err != nil {
		return fmt.Errorf("invalid json schema for tool %s: %w", name, err)
	}
	compiledSchema, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("failed to compile json schema for tool %s: %w", name, err)
	}

	r.server.AddTool(&mcp.Tool{
		Name:        name,
		Description: t.Description(),
		InputSchema: t.InputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		reqID := generateRequestID()
		ctx = WithLogger(ctx, reqID, name)
		LogToolStart(ctx)
		start := time.Now()
		var toolErr error
		defer func() {
			LogToolEnd(ctx, time.Since(start).Milliseconds(), toolErr)
		}()

		var argsMap any
		argsBytes, err := json.Marshal(req.Params.Arguments)
		if err != nil {
			toolErr = fmt.Errorf("invalid arguments format: %w", err)
			return errorResult(toolErr), nil
		}

		// If arguments are empty/nil but schema expects object, handling is required.
		if string(argsBytes) == "null" {
			argsBytes = []byte("{}")
		}

		if err := json.Unmarshal(argsBytes, &argsMap); err != nil {
			toolErr = fmt.Errorf("invalid arguments format: %w", err)
			return errorResult(toolErr), nil
		}

		if err := compiledSchema.Validate(argsMap); err != nil {
			toolErr = fmt.Errorf("schema validation failed: %w", err)
			return errorResult(toolErr), nil
		}

		resText, err := invokeHandle(ctx, t, argsBytes, r.skillGateRef())
		if err != nil {
			toolErr = err
			return errorResult(err), nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: resText,
				},
			},
		}, nil
	})

	return nil
}

func invokeHandle(ctx context.Context, t Tool, args []byte, gate *skillGate) (res string, err error) {
	logger := LoggerFrom(ctx)
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in tool handler", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("internal error")
		}
	}()

	v, err := t.Handle(ctx, json.RawMessage(args))
	if err != nil {
		return "", err
	}

	finalV, err := finalizeWithSkills(t, v, gate)
	if err != nil {
		return "", err
	}

	bytes, err := json.Marshal(finalV)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
