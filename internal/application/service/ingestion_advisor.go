package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
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
	if effectiveIngestionPromptVersion(request.PromptVersion) == types.IngestionPromptVersionV2 &&
		analysisCtx.Err() == context.DeadlineExceeded {
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

	promptVersion := effectiveIngestionPromptVersion(request.PromptVersion)
	session := newIngestionAgentSessionForPromptVersion(
		request.Content, request.ChunkingConstraints, promptVersion,
	)
	preparation, err := prepareIngestionAgent(ctx, ingestionAgentPreparationRequest{
		Model: chatModel, Request: request, Session: session, PromptVersion: promptVersion,
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
	if request.PromptVersion != "" &&
		request.PromptVersion != types.IngestionPromptVersionV1 &&
		request.PromptVersion != types.IngestionPromptVersionV2 {
		return fmt.Errorf("不支持的文档分析 Prompt 版本 %q", request.PromptVersion)
	}
	if a == nil || a.modelService == nil {
		return fmt.Errorf("文档智能分析服务未配置")
	}
	if request.KnowledgeBaseID == "" || request.TenantID == 0 {
		return fmt.Errorf("文档智能分析缺少知识库或租户作用域")
	}
	return nil
}

func registerIngestionCoreTools(registry *agenttools.ToolRegistry, session *ingestionAgentSession) {
	registry.RegisterTool(newInspectIngestionDocument(session))
	registry.RegisterTool(newPreviewIngestionChunking(session))
	registry.RegisterTool(newSubmitIngestionDecision(session))
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

func buildIngestionAgentQuery(profile types.IngestionDocumentProfile) (string, error) {
	payload, err := json.Marshal(profile.Statistics)
	if err != nil {
		return "", err
	}
	return "请分析当前待入库文档。以下仅为全文结构统计；需要查看内容时调用 inspect_ingestion_document：\n" + string(payload), nil
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

const ingestionAgentV1SystemPrompt = `你是智能文档入库 Agent。你的唯一目标是为当前文档选择经过真实预览验证的切分候选。

必须遵循：
1. 需要原文时用 inspect_ingestion_document 按 rune 偏移查看，每次最多 8000 字符。
2. 必须调用 preview_ingestion_chunking 生成并比较候选；最多可保存 3 个不同候选，重复配置会复用结果。工具成功输出中的 candidate_id 是提交决策所需的唯一标识。
3. 可并行预览候选。观察真实 diagnostics、块长度、结构保持与五维评分后再修正。saved_candidate_count 达到 candidate_limit 后严禁继续预览，下一轮必须提交。
4. 最终必须调用 submit_ingestion_decision，并且 candidate_id 必须来自成功预览且通过硬校验的候选。已有有效候选时不必凑满 3 个；完成必要比较后立即提交。
5. 可以选择非最高分候选，但 reason_codes 和 summary 必须明确解释与文档画像相关的取舍。
6. 不要输出聊天式最终答案；成功提交工具会立即结束运行。
7. Web 或 MCP 工具均为外部系统。只有在工具列表中出现时才表示用户已允许向其传输你提供的查询或原文内容。`
