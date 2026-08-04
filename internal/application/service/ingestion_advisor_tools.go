package service

import (
	"context"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func (a *modelIngestionAdvisor) registerOptionalTools(
	ctx context.Context,
	registry *agenttools.ToolRegistry,
	config *types.AgentConfig,
	chatModel chat.Chat,
	request types.IngestionAdvisorRequest,
) []types.IngestionAgentWarning {
	warnings := []types.IngestionAgentWarning{skillReadWarning()}
	if a.readOnlyTools == nil {
		return append(warnings, types.IngestionAgentWarning{
			Code: "readonly_tools_unavailable", Message: "只读 Agent 工具工厂未配置",
		})
	}

	names, wikiKBIDs := ingestionReadOnlyToolNames(request)
	options := ingestionReadOnlyToolOptions(config, chatModel, request, wikiKBIDs)
	for _, name := range names {
		tool, err := a.readOnlyTools.Build(ctx, name, options)
		if err != nil {
			warnings = append(warnings, types.IngestionAgentWarning{
				Code: "readonly_tool_unavailable", Tool: name, Message: err.Error(),
			})
			continue
		}
		registry.RegisterTool(tool)
	}
	if request.AllowReadOnlyMCP {
		warnings = append(warnings, a.registerIngestionMCP(ctx, registry, request.TenantID)...)
	}
	return sortedWarningCopy(warnings)
}

func ingestionReadOnlyToolNames(request types.IngestionAdvisorRequest) ([]string, []string) {
	names := []string{agenttools.ToolThinking}
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
	config *types.AgentConfig,
	chatModel chat.Chat,
	request types.IngestionAdvisorRequest,
	wikiKBIDs []string,
) readOnlyAgentToolOptions {
	wikiRoutes := agenttools.NewWikiRouteResolver()
	return readOnlyAgentToolOptions{
		Config: config, ChatModel: chatModel, SessionID: ingestionAgentSessionID(request),
		ReadOnlyWeb: true,
		WikiKBIDs:   wikiKBIDs, WikiRoutes: wikiRoutes,
		WikiScopes: agenttools.NewWikiScopesFromSearchTargets(config.SearchTargets, wikiKBIDs),
	}
}

func (a *modelIngestionAdvisor) registerIngestionMCP(
	ctx context.Context,
	registry *agenttools.ToolRegistry,
	tenantID uint64,
) []types.IngestionAgentWarning {
	registered, err := a.readOnlyTools.RegisterMCP(ctx, registry, tenantID)
	if err != nil {
		return []types.IngestionAgentWarning{{
			Code: "readonly_mcp_unavailable", Message: err.Error(),
		}}
	}
	if registered == 0 {
		return []types.IngestionAgentWarning{{
			Code:    "readonly_mcp_unavailable",
			Message: "没有可用且声明 readOnlyHint=true 的 MCP 工具",
		}}
	}
	return nil
}

func skillReadWarning() types.IngestionAgentWarning {
	return types.IngestionAgentWarning{
		Code: "skill_read_unavailable", Tool: agenttools.ToolReadSkill,
		Message: "入库任务未配置可读取的 skill 目录",
	}
}
