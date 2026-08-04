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
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	ingestionAdvisorMaxRounds    = 4
	ingestionAdvisorTimeout      = 8 * time.Minute
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
	chatModel, err := a.modelService.GetChatModel(ctx, request.ModelID)
	if err != nil {
		return nil, wrapIngestionAdvisorRunError(
			ingestionAdvisorErrorModelUnavailable, "加载文档分析模型失败", err,
		)
	}

	session := newIngestionAgentSession(request.Content, request.ChunkingConstraints)
	query, err := buildIngestionAgentQuery(session.profile)
	if err != nil {
		return nil, fmt.Errorf("构建文档分析请求失败: %w", err)
	}
	webState := a.newIngestionWebSearchState(request, runtime)
	registry := agenttools.NewToolRegistry()
	registerIngestionCoreTools(registry, session)
	config := buildIngestionAgentConfig(request)
	warnings := a.registerOptionalTools(ctx, registry, ingestionOptionalToolOptions{
		config: config, chatModel: chatModel, request: request,
		webSearchKnowledge: runtime.WebSearchKnowledge, webSearchState: webState,
	})
	config.AllowedTools = registry.ListTools()
	run := newIngestionAgentRun(config.AllowedTools, warnings)

	engine := agent.NewAgentEngine(
		config, chatModel, registry, event.NewEventBus(),
		ingestionKnowledgeBaseInfo(request), nil, ingestionAgentSessionID(request), "",
	)
	if a.readOnlyTools != nil {
		engine.SetAppConfig(a.readOnlyTools.params.Config)
		engine.SetSkillsManager(a.readOnlyTools.loadedSkillsManager())
	}
	state, executeErr := executeIngestionAgent(ctx, engine, request, query)
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

func executeIngestionAgent(
	ctx context.Context,
	engine interfaces.AgentEngine,
	request types.IngestionAdvisorRequest,
	query string,
) (*types.AgentState, error) {
	callCtx, cancel := context.WithTimeout(ctx, ingestionAdvisorTimeout)
	defer cancel()
	callCtx = types.WithLLMCallMetadata(callCtx, "document_analysis", "")
	return engine.ExecuteTask(callCtx, interfaces.AgentTaskRequest{
		SessionID: ingestionAgentSessionID(request),
		MessageID: request.KnowledgeID,
		Query:     query,
		Options: interfaces.AgentTaskOptions{
			SystemPrompt:      ingestionAgentSystemPrompt,
			MaxIterations:     ingestionAdvisorMaxRounds,
			TerminationTool:   submitIngestionDecisionTool,
			FinalRoundTool:    submitIngestionDecisionTool,
			SkipFinalAnswer:   true,
			StructuredEventFn: ingestionProgressReceiver(request.ProgressFn),
		},
	})
}

func validateIngestionAdvisorRequest(a *modelIngestionAdvisor, request types.IngestionAdvisorRequest) error {
	if strings.TrimSpace(request.ModelID) == "" {
		return newIngestionAdvisorRunError(
			ingestionAdvisorErrorModelUnavailable, "知识库未配置摘要模型，无法执行文档智能分析",
		)
	}
	if request.PromptVersion != types.IngestionPromptVersionV1 {
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

const ingestionAgentSystemPrompt = `你是智能文档入库 Agent。你的唯一目标是为当前文档选择经过真实预览验证的切分候选。

必须遵循：
1. 需要原文时用 inspect_ingestion_document 按 rune 偏移查看，每次最多 8000 字符。
2. 必须调用 preview_ingestion_chunking 生成并比较候选；最多可保存 3 个不同候选，重复配置会复用结果。工具成功输出中的 candidate_id 是提交决策所需的唯一标识。
3. 可并行预览候选。观察真实 diagnostics、块长度、结构保持与五维评分后再修正。saved_candidate_count 达到 candidate_limit 后严禁继续预览，下一轮必须提交。
4. 最终必须调用 submit_ingestion_decision，并且 candidate_id 必须来自成功预览且通过硬校验的候选。第 4 轮是最后一轮；只要已有成功候选，第 4 轮必须直接提交，不能再次调用预览或其他工具。
5. 可以选择非最高分候选，但 reason_codes 和 summary 必须明确解释与文档画像相关的取舍。
6. 不要输出聊天式最终答案；成功提交工具会立即结束运行。
7. Web 或 MCP 工具均为外部系统。只有在工具列表中出现时才表示用户已允许向其传输你提供的查询或原文内容。`
