package service

import (
	"context"
	"errors"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type inertWebSearchService struct {
	interfaces.WebSearchService
}

type inertWebSearchStateService struct {
	interfaces.WebSearchStateService
}

type inertKnowledgeService struct {
	interfaces.KnowledgeService
}

func TestIngestionReadOnlyToolPolicyAllowsReadsAndRejectsSideEffects(t *testing.T) {
	allowed := []string{
		agenttools.ToolThinking,
		agenttools.ToolKnowledgeSearch,
		agenttools.ToolGrepChunks,
		agenttools.ToolListKnowledgeChunks,
		agenttools.ToolQueryKnowledgeGraph,
		agenttools.ToolGetDocumentInfo,
		agenttools.ToolDatabaseQuery,
		agenttools.ToolDataAnalysis,
		agenttools.ToolDataSchema,
		agenttools.ToolWebSearch,
		agenttools.ToolWebFetch,
		agenttools.ToolReadSkill,
		agenttools.ToolWikiReadPage,
		agenttools.ToolWikiSearch,
		agenttools.ToolWikiReadSourceDoc,
		agenttools.ToolWikiReadIssue,
	}
	for _, toolName := range allowed {
		require.True(t, isSharedReadOnlyAgentTool(toolName), toolName)
	}

	forbidden := []string{
		agenttools.ToolTodoWrite,
		agenttools.ToolExecuteSkillScript,
		agenttools.ToolWikiWritePage,
		agenttools.ToolWikiReplaceText,
		agenttools.ToolWikiRenamePage,
		agenttools.ToolWikiDeletePage,
		agenttools.ToolWikiFlagIssue,
		agenttools.ToolWikiUpdateIssue,
	}
	for _, toolName := range forbidden {
		require.False(t, isSharedReadOnlyAgentTool(toolName), toolName)
	}
}

func TestIngestionReadOnlyToolNamesRespectExplicitCapabilities(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.GraphEnabled = true
	request.WikiEnabled = true
	request.AllowWebAccess = true

	names, wikiKBIDs := ingestionReadOnlyToolNames(request)

	require.Contains(t, names, agenttools.ToolReadSkill)
	require.Contains(t, names, agenttools.ToolQueryKnowledgeGraph)
	require.Contains(t, names, agenttools.ToolWikiReadPage)
	require.Contains(t, names, agenttools.ToolWebSearch)
	require.NotContains(t, names, agenttools.ToolExecuteSkillScript)
	require.Equal(t, []string{request.KnowledgeBaseID}, wikiKBIDs)
}

func TestIngestionAgentConfigPinsTenantAndKnowledgeBaseScope(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.TenantID = 42
	request.KnowledgeBaseID = "kb-scoped"
	request.AllowWebAccess = true

	config := buildIngestionAgentConfig(request)

	require.Len(t, config.SearchTargets, 1)
	require.Equal(t, types.SearchTargetTypeKnowledgeBase, config.SearchTargets[0].Type)
	require.Equal(t, uint64(42), config.SearchTargets[0].TenantID)
	require.Equal(t, "kb-scoped", config.SearchTargets[0].KnowledgeBaseID)
	require.True(t, config.WebSearchEnabled)
	require.True(t, config.ParallelToolCalls)
}

func TestWebSearchToolFactorySeparatesReadOnlyAndCompressionDependencies(t *testing.T) {
	factory := NewReadOnlyAgentToolFactory(readOnlyAgentToolFactoryParams{
		WebSearchService: &inertWebSearchService{},
	})
	config := &types.AgentConfig{WebSearchMaxResults: 3}

	readOnlyTool, err := factory.Build(context.Background(), agenttools.ToolWebSearch, readOnlyAgentToolOptions{
		Config: config, ReadOnlyWeb: true,
	})
	require.NoError(t, err)
	require.NotNil(t, readOnlyTool)

	_, err = factory.Build(context.Background(), agenttools.ToolWebSearch, readOnlyAgentToolOptions{
		Config: config, WebSearchState: &inertWebSearchStateService{},
	})
	require.EqualError(t, err, "Web 搜索 RAG 压缩服务未配置")

	regularTool, err := factory.Build(context.Background(), agenttools.ToolWebSearch, readOnlyAgentToolOptions{
		Config:           config,
		KnowledgeService: &inertKnowledgeService{},
		WebSearchState:   &inertWebSearchStateService{},
	})
	require.NoError(t, err)
	require.NotNil(t, regularTool)
}

func TestIngestionMCPWarningsRetainPartialServiceDiagnostics(t *testing.T) {
	warnings := ingestionMCPWarnings(1, []agenttools.MCPRegistrationDiagnostic{
		{Code: "connection_failed", Service: "offline_service", Message: "MCP 服务连接失败"},
		{Code: "tool_list_failed", Service: "broken_catalog", Message: "MCP 服务工具列表不可用"},
	}, nil)

	require.Len(t, warnings, 2)
	require.Equal(t, "readonly_mcp_service_connection_failed", warnings[0].Code)
	require.Equal(t, "offline_service", warnings[0].Tool)
	require.Equal(t, "readonly_mcp_service_tool_list_failed", warnings[1].Code)
	require.NotContains(t, warnings[0].Message+warnings[1].Message, "credential")
}

func TestIngestionMCPWarningsReportGlobalAndEmptyFailures(t *testing.T) {
	global := ingestionMCPWarnings(0, nil, errors.New("Bearer secret-registry-response"))
	require.Equal(t, "readonly_mcp_unavailable", global[0].Code)
	require.NotContains(t, global[0].Message, "secret")

	empty := ingestionMCPWarnings(0, nil, nil)
	require.Equal(t, "readonly_mcp_unavailable", empty[0].Code)
	require.Contains(t, empty[0].Message, "readOnlyHint=true")

	require.Empty(t, ingestionMCPWarnings(2, nil, nil))
}
