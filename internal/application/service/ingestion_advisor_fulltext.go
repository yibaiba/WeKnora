package service

import (
	"context"
	"encoding/json"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type ingestionAgentPreparation struct {
	Query        string
	SystemPrompt string
	Registry     *agenttools.ToolRegistry
}

type ingestionAgentPreparationRequest struct {
	Model   chat.Chat
	Request types.IngestionAdvisorRequest
	Session *ingestionAgentSession
}

type ingestionAgentContext struct {
	Statistics         types.DocumentStructureStats `json:"statistics"`
	AggregatedEvidence ingestionDocumentEvidence    `json:"aggregated_evidence"`
}

func prepareIngestionAgent(
	ctx context.Context,
	request ingestionAgentPreparationRequest,
) (ingestionAgentPreparation, error) {
	registry := agenttools.NewToolRegistry()
	evidence, err := analyzeFullIngestionDocument(ctx, request.Model, request.Request)
	if err != nil {
		return ingestionAgentPreparation{}, err
	}
	query, err := buildIngestionAgentQuery(request.Session.statistics, evidence)
	if err != nil {
		return ingestionAgentPreparation{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "构建文档全文聚合证据请求失败",
		)
	}
	registerIngestionDecisionTools(registry, request.Session)
	return ingestionAgentPreparation{
		Query: query, SystemPrompt: ingestionAgentSystemPrompt, Registry: registry,
	}, nil
}

func analyzeFullIngestionDocument(
	ctx context.Context,
	model chat.Chat,
	request types.IngestionAdvisorRequest,
) (ingestionDocumentEvidence, error) {
	units, err := splitIngestionDocumentAnalysisUnits(request.Content)
	if err != nil {
		return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Map 分析准备失败：%s", err,
		)
	}
	mapped, err := mapIngestionDocument(ctx, ingestionDocumentMapRequest{
		Model: model, Units: units, Progress: request.AnalysisProgressFn,
	})
	if err != nil {
		return ingestionDocumentEvidence{}, err
	}
	return reduceIngestionDocument(ctx, ingestionDocumentReduceRequest{
		Model: model, Evidence: mapped, CoveredCharacters: len([]rune(request.Content)),
		Progress: request.AnalysisProgressFn,
	})
}

func buildIngestionAgentQuery(
	statistics types.DocumentStructureStats,
	evidence ingestionDocumentEvidence,
) (string, error) {
	payload, err := json.Marshal(ingestionAgentContext{
		Statistics: statistics, AggregatedEvidence: evidence,
	})
	if err != nil {
		return "", err
	}
	return "请根据以下全文统计与 Map-Reduce 聚合证据，预览候选并提交入库切分决策：\n" + string(payload), nil
}

const ingestionAgentSystemPrompt = `你是智能文档入库 Agent。全文正文已经由严格的 Map-Reduce 分析完成；你的唯一目标是根据全文统计、聚合证据和真实切分预览选择候选。

必须遵循：
1. 你不会获得正文读取工具；不得索要或猜测抽样正文。查询中的 aggregated_evidence 覆盖完整提取正文。
2. 必须调用 preview_ingestion_chunking 生成并比较候选；最多可保存 3 个不同候选，重复配置会复用结果。工具成功输出中的 candidate_id 是提交决策所需的唯一标识。
3. 可并行预览候选。观察真实 diagnostics、块长度、结构保持与五维评分后再修正。saved_candidate_count 达到 candidate_limit 后严禁继续预览，下一轮必须提交。
4. 最终必须调用 submit_ingestion_decision，并且 candidate_id 必须来自成功预览且通过硬校验的候选。已有有效候选时不必凑满 3 个；完成必要比较后立即提交。
5. 可以选择非最高分候选，但 reason_codes 和 summary 必须明确解释与全文证据相关的取舍。
6. 不要输出聊天式最终答案；成功提交工具会立即结束运行。
7. Web 或 MCP 工具均为外部系统。只有在工具列表中出现时才表示用户已允许向其传输你提供的查询内容。`
