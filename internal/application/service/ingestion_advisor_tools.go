package service

import (
	"context"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type ingestionOptionalToolOptions struct {
	config             *types.AgentConfig
	chatModel          chat.Chat
	request            types.IngestionAdvisorRequest
	webSearchKnowledge interfaces.WebSearchTemporaryKnowledgeService
	webSearchState     interfaces.WebSearchStateService
}

func (a *modelIngestionAdvisor) registerOptionalTools(
	ctx context.Context,
	registry *agenttools.ToolRegistry,
	options ingestionOptionalToolOptions,
) []types.IngestionAgentWarning {
	warnings := []types.IngestionAgentWarning{}
	if a.readOnlyTools == nil {
		return append(warnings, types.IngestionAgentWarning{
			Code: "readonly_tools_unavailable", Message: "只读 Agent 工具工厂未配置",
		})
	}

	names, wikiKBIDs := ingestionReadOnlyToolNames(options.request)
	toolOptions := ingestionReadOnlyToolOptions(options, wikiKBIDs)
	for _, name := range names {
		tool, err := a.readOnlyTools.Build(ctx, name, toolOptions)
		if err != nil {
			warnings = append(warnings, types.IngestionAgentWarning{
				Code: "readonly_tool_unavailable", Tool: name, Message: err.Error(),
			})
			continue
		}
		registry.RegisterTool(tool)
	}
	if options.request.AllowReadOnlyMCP {
		warnings = append(warnings, a.registerIngestionMCP(ctx, registry, options.request.TenantID)...)
	}
	return sortedWarningCopy(warnings)
}

func ingestionReadOnlyToolNames(request types.IngestionAdvisorRequest) ([]string, []string) {
	names := []string{agenttools.ToolThinking, agenttools.ToolReadSkill}
	if request.VectorEnabled || request.KeywordEnabled {
		names = append(names,
			agenttools.ToolKnowledgeSearch, agenttools.ToolGrepChunks,
			agenttools.ToolListKnowledgeChunks, agenttools.ToolGetDocumentInfo,
			agenttools.ToolDatabaseQuery, agenttools.ToolDataAnalysis, agenttools.ToolDataSchema,
		)
	}
	if request.GraphEnabled {
		names = append(names, agenttools.ToolQueryKnowledgeGraph)
	}
	wikiKBIDs := []string(nil)
	if request.WikiEnabled {
		wikiKBIDs = []string{request.KnowledgeBaseID}
		names = append(names,
			agenttools.ToolWikiReadPage, agenttools.ToolWikiSearch,
			agenttools.ToolWikiReadSourceDoc, agenttools.ToolWikiReadIssue,
		)
	}
	if request.AllowWebAccess {
		names = append(names, agenttools.ToolWebSearch, agenttools.ToolWebFetch)
	}
	return names, wikiKBIDs
}

func ingestionReadOnlyToolOptions(
	options ingestionOptionalToolOptions,
	wikiKBIDs []string,
) readOnlyAgentToolOptions {
	wikiRoutes := agenttools.NewWikiRouteResolver()
	return readOnlyAgentToolOptions{
		Config: options.config, ChatModel: options.chatModel,
		SessionID:            ingestionAgentSessionID(options.request),
		WebSearchKnowledge:   options.webSearchKnowledge,
		WebSearchState:       options.webSearchState,
		StrictWebCompression: true,
		WikiKBIDs:            wikiKBIDs, WikiRoutes: wikiRoutes,
		WikiScopes: agenttools.NewWikiScopesFromSearchTargets(options.config.SearchTargets, wikiKBIDs),
	}
}

func (a *modelIngestionAdvisor) registerIngestionMCP(
	ctx context.Context,
	registry *agenttools.ToolRegistry,
	tenantID uint64,
) []types.IngestionAgentWarning {
	registered, diagnostics, err := a.readOnlyTools.RegisterMCP(ctx, registry, tenantID)
	return ingestionMCPWarnings(registered, diagnostics, err)
}

func ingestionMCPWarnings(
	registered int,
	diagnostics []agenttools.MCPRegistrationDiagnostic,
	err error,
) []types.IngestionAgentWarning {
	if err != nil {
		return []types.IngestionAgentWarning{{
			Code: "readonly_mcp_unavailable", Message: "MCP 只读工具注册失败",
		}}
	}
	warnings := make([]types.IngestionAgentWarning, 0, len(diagnostics)+1)
	for _, diagnostic := range diagnostics {
		warnings = append(warnings, types.IngestionAgentWarning{
			Code: "readonly_mcp_service_" + diagnostic.Code,
			Tool: diagnostic.Service, Message: diagnostic.Message,
		})
	}
	if registered == 0 {
		warnings = append(warnings, types.IngestionAgentWarning{
			Code:    "readonly_mcp_unavailable",
			Message: "没有可用且声明 readOnlyHint=true 的 MCP 工具",
		})
	}
	return warnings
}
