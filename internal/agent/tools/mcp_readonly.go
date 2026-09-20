package tools

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
)

// MCPRegistrationDiagnostic is a payload-free per-service discovery failure.
// Message is fixed by the backend and never includes upstream errors.
type MCPRegistrationDiagnostic struct {
	Code    string
	Service string
	Message string
}

// RegisterReadOnlyMCPToolsWithDiagnostics wraps origin RegisterMCPTools for
// the fork's ingestion/read-only factory. Origin no longer reports per-service
// diagnostics here; callers still receive a count and a top-level error.
func RegisterReadOnlyMCPToolsWithDiagnostics(
	ctx context.Context,
	registry *ToolRegistry,
	services []*types.MCPService,
	mcpManager *mcp.MCPManager,
	gate approval.MCPApproval,
	_ *MCPOAuthSession,
) (int, []MCPRegistrationDiagnostic, error) {
	registered, err := RegisterMCPTools(ctx, registry, services, mcpManager, gate, 0, nil, nil)
	return registered, nil, err
}
