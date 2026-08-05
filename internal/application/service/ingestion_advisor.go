package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	ingestionAdvisorMaxRounds    = 6
	ingestionAdvisorTimeout      = appconfig.DefaultIngestionAdvisorTimeout
	ingestionWebCleanupTimeout   = time.Minute
	ingestionWebSearchMaxResults = 5
)

type modelIngestionAdvisor struct {
	modelService  interfaces.ModelService
	readOnlyTools *readOnlyAgentToolFactory
}

func NewIngestionAdvisor(
	modelService interfaces.ModelService,
	readOnlyTools *readOnlyAgentToolFactory,
) interfaces.IngestionAdvisor {
	return &modelIngestionAdvisor{modelService: modelService, readOnlyTools: readOnlyTools}
}

func (a *modelIngestionAdvisor) Analyze(
	ctx context.Context,
	request types.IngestionAdvisorRequest,
	runtime interfaces.IngestionAdvisorRuntime,
) (*types.IngestionAdvisorResult, error) {
	if err := validateIngestionAdvisorRequest(a, request); err != nil {
		return nil, err
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = ingestionAdvisorTimeout
	}
	analysisCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := a.analyze(analysisCtx, request, runtime)
	if analysisCtx.Err() == context.DeadlineExceeded {
		return result, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文分析与入库决策超过总超时 %s", timeout,
		)
	}
	return result, err
}

func (a *modelIngestionAdvisor) analyze(
	ctx context.Context,
	request types.IngestionAdvisorRequest,
	runtime interfaces.IngestionAdvisorRuntime,
) (*types.IngestionAdvisorResult, error) {
	chatModel, err := a.modelService.GetChatModel(ctx, request.ModelID)
	if err != nil {
		return nil, wrapIngestionAdvisorRunError(
			ingestionAdvisorErrorModelUnavailable, "加载文档分析模型失败", err,
		)
	}

	session := newIngestionAgentSession(request.Content, request.ChunkingConstraints)
	preparation, err := prepareIngestionAgent(ctx, ingestionAgentPreparationRequest{
		Model: chatModel, Request: request, Session: session,
	})
	if err != nil {
		return nil, err
	}
	webState := a.newIngestionWebSearchState(request, runtime)
	config := buildIngestionAgentConfig(request)
	warnings := a.registerOptionalTools(ctx, preparation.Registry, ingestionOptionalToolOptions{
		config: config, chatModel: chatModel, request: request,
		webSearchKnowledge: runtime.WebSearchKnowledge, webSearchState: webState,
	})
	config.AllowedTools = preparation.Registry.ListTools()
	run := newIngestionAgentRun(config.AllowedTools, warnings)

	engine := agent.NewAgentEngine(
		config, chatModel, preparation.Registry, event.NewEventBus(),
		ingestionKnowledgeBaseInfo(request), nil, ingestionAgentSessionID(request), "",
	)
	if a.readOnlyTools != nil {
		engine.SetAppConfig(a.readOnlyTools.params.Config)
		engine.SetSkillsManager(a.readOnlyTools.loadedSkillsManager())
	}
	state, executeErr := executeIngestionAgent(ctx, executeIngestionAgentRequest{
		Engine: engine, Request: request, Query: preparation.Query,
		SystemPrompt: preparation.SystemPrompt,
	})
	run = buildIngestionAgentRun(run, state)
	result := buildIngestionAdvisorResult(session, run)
	cleanupErr := cleanupIngestionWebSearch(ctx, webState, ingestionAgentSessionID(request))
	if executeErr != nil {
		return result, classifyIngestionAgentExecutionError(errors.Join(executeErr, cleanupErr))
	}
	if cleanupErr != nil {
		return result, wrapIngestionAdvisorRunError(
			ingestionAdvisorErrorExecution, "清理 Web 搜索临时知识库失败", cleanupErr,
		)
	}
	if err := validateIngestionAgentOutcome(state, session); err != nil {
		return result, err
	}
	return buildIngestionAdvisorResult(session, run), nil
}

func (a *modelIngestionAdvisor) newIngestionWebSearchState(
	request types.IngestionAdvisorRequest,
	runtime interfaces.IngestionAdvisorRuntime,
) interfaces.WebSearchStateService {
	if !request.AllowWebAccess || a.readOnlyTools == nil || runtime.WebSearchKnowledge == nil {
		return nil
	}
	return newIngestionWebSearchState(
		a.readOnlyTools.params.KnowledgeBaseService,
		runtime.WebSearchKnowledge,
	)
}

func cleanupIngestionWebSearch(
	ctx context.Context,
	state interfaces.WebSearchStateService,
	sessionID string,
) error {
	if state == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ingestionWebCleanupTimeout)
	defer cancel()
	return state.DeleteWebSearchTempKBState(cleanupCtx, sessionID)
}

type executeIngestionAgentRequest struct {
	Engine       interfaces.AgentEngine
	Request      types.IngestionAdvisorRequest
	Query        string
	SystemPrompt string
}

func executeIngestionAgent(ctx context.Context, request executeIngestionAgentRequest) (*types.AgentState, error) {
	callCtx := types.WithLLMCallMetadata(ctx, "document_analysis", "")
	callCtx = logger.WithSuppressedOutput(callCtx)
	return request.Engine.ExecuteTask(callCtx, interfaces.AgentTaskRequest{
		SessionID: ingestionAgentSessionID(request.Request),
		MessageID: request.Request.KnowledgeID,
		Query:     request.Query,
		Options: interfaces.AgentTaskOptions{
			SystemPrompt:      request.SystemPrompt,
			MaxIterations:     ingestionAdvisorMaxRounds,
			TerminationTool:   submitIngestionDecisionTool,
			SkipFinalAnswer:   true,
			StructuredEventFn: ingestionProgressReceiver(request.Request.ProgressFn),
		},
	})
}

func validateIngestionAdvisorRequest(a *modelIngestionAdvisor, request types.IngestionAdvisorRequest) error {
	if strings.TrimSpace(request.ModelID) == "" {
		return newIngestionAdvisorRunError(
			ingestionAdvisorErrorModelUnavailable, "知识库未配置摘要模型，无法执行文档智能分析",
		)
	}
	if a == nil || a.modelService == nil {
		return fmt.Errorf("文档智能分析服务未配置")
	}
	if request.KnowledgeBaseID == "" || request.TenantID == 0 {
		return fmt.Errorf("文档智能分析缺少知识库或租户作用域")
	}
	return nil
}

func registerIngestionDecisionTools(registry *agenttools.ToolRegistry, session *ingestionAgentSession) {
	registry.RegisterTool(newPreviewIngestionChunking(session))
	registry.RegisterTool(newSubmitIngestionDecision(session))
}

func buildIngestionAgentConfig(request types.IngestionAdvisorRequest) *types.AgentConfig {
	return &types.AgentConfig{
		MaxIterations:       ingestionAdvisorMaxRounds,
		Temperature:         0,
		ParallelToolCalls:   true,
		WebSearchEnabled:    request.AllowWebAccess,
		WebSearchMaxResults: ingestionWebSearchMaxResults,
		MCPSelectionMode:    "none",
		SearchTargets: types.SearchTargets{{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: request.KnowledgeBaseID,
			TenantID:        request.TenantID,
		}},
	}
}

func ingestionKnowledgeBaseInfo(request types.IngestionAdvisorRequest) []*agent.KnowledgeBaseInfo {
	capabilities := make([]string, 0, 2)
	if request.WikiEnabled {
		capabilities = append(capabilities, "wiki")
	}
	if request.VectorEnabled || request.KeywordEnabled {
		capabilities = append(capabilities, "chunks")
	}
	return []*agent.KnowledgeBaseInfo{{
		ID: request.KnowledgeBaseID, Name: request.KnowledgeBaseName,
		Type: request.KnowledgeBaseType, Capabilities: capabilities,
	}}
}

func ingestionAgentSessionID(request types.IngestionAdvisorRequest) string {
	if request.KnowledgeID == "" {
		return "ingestion-advisor"
	}
	return "ingestion-" + request.KnowledgeID
}
