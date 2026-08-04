package service

import (
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

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
