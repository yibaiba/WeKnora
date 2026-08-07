package service

import (
	"context"
	"encoding/json"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type ingestionAgentPreparation struct {
	Query            string
	SystemPrompt     string
	Registry         *agenttools.ToolRegistry
	TerminationTools []string
}

type ingestionAgentPreparationRequest struct {
	Model   chat.Chat
	Request types.IngestionAdvisorRequest
	Session *ingestionAgentSession
}

type ingestionAgentContext struct {
	Statistics         types.DocumentStructureStats  `json:"statistics"`
	AggregatedEvidence ingestionDocumentEvidence     `json:"aggregated_evidence"`
	PackingPolicy      types.SemanticPackingPolicy   `json:"packing_policy"`
	Candidates         []ingestionAgentCandidateView `json:"candidates"`
	DefaultCandidateID string                        `json:"default_candidate_id,omitempty"`
}

type ingestionAgentCandidateView struct {
	ID                   string                                  `json:"id"`
	Archetype            string                                  `json:"archetype"`
	PackingPolicyVersion string                                  `json:"packing_policy_version"`
	Config               types.IngestionChunkingRecommendation   `json:"config"`
	ChunkCount           int                                     `json:"chunk_count"`
	ParentChunkCount     int                                     `json:"parent_chunk_count"`
	Lengths              types.IngestionLengthDistribution       `json:"lengths"`
	Structure            types.IngestionStructureMetrics         `json:"structure"`
	StructureQuality     types.IngestionStructureQuality         `json:"structure_quality"`
	Diagnostics          types.IngestionChunkerDiagnostics       `json:"diagnostics"`
	Score                types.IngestionCandidateScore           `json:"score"`
	HardValid            bool                                    `json:"hard_valid"`
	Violations           []string                                `json:"violations"`
	ComparisonFacts      types.IngestionCandidateComparisonFacts `json:"comparison_facts"`
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
	if err := request.Session.generateCandidates(evidence); err != nil {
		return ingestionAgentPreparation{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorCandidateGeneration, "生成确定性分块候选失败：%s", err,
		)
	}
	query, err := buildIngestionAgentQuery(request.Session, evidence)
	if err != nil {
		return ingestionAgentPreparation{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "构建文档全文聚合证据请求失败",
		)
	}
	registerIngestionDecisionTools(registry, request.Session)
	terminationTools := []string{submitIngestionDecisionTool}
	if request.Session.fallbackReady() {
		terminationTools = append(terminationTools, submitIngestionFallbackTool)
	}
	return ingestionAgentPreparation{
		Query: query, SystemPrompt: ingestionAgentSystemPrompt, Registry: registry,
		TerminationTools: terminationTools,
	}, nil
}

func analyzeFullIngestionDocument(
	ctx context.Context,
	model chat.Chat,
	request types.IngestionAdvisorRequest,
) (ingestionDocumentEvidence, error) {
	structuredOutputPrompt, err := chat.ResolveStructuredOutputPrompt(
		model, ingestionDocumentEvidenceSchema,
	)
	if err != nil {
		return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Map 分析准备失败：结构化输出 Schema 无效",
		)
	}
	budget, err := calculateIngestionDocumentAnalysisTokenBudgetWithPrompt(
		chat.ResolveContextWindowTokens(model), request.Content, structuredOutputPrompt,
	)
	if err != nil {
		return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Map 分析准备失败：%s", err,
		)
	}
	units, err := splitIngestionDocumentAnalysisUnits(request.Content, budget.ContentTokens)
	if err != nil {
		return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Map 分析准备失败：%s", err,
		)
	}
	mapped, err := mapIngestionDocument(ctx, ingestionDocumentMapRequest{
		Model: model, Units: units, Progress: request.AnalysisProgressFn, Budget: budget,
	})
	if err != nil {
		return ingestionDocumentEvidence{}, err
	}
	if err := validateIngestionDocumentAnalysisCoverage(
		request.Content, units, budget.ContentTokens,
	); err != nil {
		return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Map 分析结果覆盖校验失败：%s", err,
		)
	}
	return reduceIngestionDocument(ctx, ingestionDocumentReduceRequest{
		Model: model, Evidence: mapped, CoveredCharacters: len([]rune(request.Content)),
		Progress: request.AnalysisProgressFn, Budget: budget,
	})
}

func buildIngestionAgentQuery(
	session *ingestionAgentSession,
	evidence ingestionDocumentEvidence,
) (string, error) {
	payload, err := json.Marshal(ingestionAgentContext{
		Statistics: session.statistics, AggregatedEvidence: evidence,
		PackingPolicy:      cloneSemanticPackingPolicy(session.policy),
		Candidates:         ingestionAgentCandidateViews(session.candidateSnapshot()),
		DefaultCandidateID: session.defaultCandidateID(),
	})
	if err != nil {
		return "", err
	}
	return "请根据以下全文统计、Map-Reduce 聚合证据和后端候选比较事实提交入库切分决策：\n" + string(payload), nil
}

func ingestionAgentCandidateViews(
	candidates []types.IngestionChunkingCandidate,
) []ingestionAgentCandidateView {
	views := make([]ingestionAgentCandidateView, len(candidates))
	for index, candidate := range candidates {
		views[index] = ingestionAgentCandidateView{
			ID: candidate.ID, Archetype: candidate.Archetype,
			PackingPolicyVersion: candidate.PackingPolicyVersion,
			Config:               cloneChunkingRecommendation(candidate.Config),
			ChunkCount:           candidate.ChunkCount, ParentChunkCount: candidate.ParentChunkCount,
			Lengths: candidate.Lengths, Structure: candidate.Structure,
			StructureQuality: candidate.StructureQuality, Diagnostics: candidate.Diagnostics,
			Score: candidate.Score, HardValid: candidate.HardValid,
			Violations:      append([]string(nil), candidate.Violations...),
			ComparisonFacts: candidate.ComparisonFacts,
		}
	}
	return views
}

const ingestionAgentSystemPrompt = `你是智能文档入库 Agent。全文正文已经由严格的 Map-Reduce 分析完成；你的唯一目标是根据全文统计、聚合证据和后端候选比较事实选择候选。

必须遵循：
1. 你不会获得正文读取工具；不得索要或猜测抽样正文。查询中的 aggregated_evidence 覆盖完整提取正文。
2. 后端已生成并完整评估三个候选。不得创建、修改或搜索分块参数，只能提交查询中已存在且 comparison_facts.selection_eligible=true 的 candidate_id。
3. 默认提交 default_candidate_id。只有 comparison_facts 已明确给出 evidence_advantages 和后端 reason_codes 时，才可选择其他候选；不得自行解释为可选。
4. 有硬校验有效候选时必须调用 submit_ingestion_decision。只有工具列表实际出现 submit_ingestion_fallback 时，才表示三个候选全部 HardValid=false；不得因模型、Schema、工具、取消或超时错误请求回退。
5. reason_codes 和 summary 只描述文档画像；候选选择原因由后端比较事实生成。
6. 不要输出聊天式最终答案；成功提交工具会立即结束运行。
7. Web 或 MCP 工具均为外部系统。只有在工具列表中出现时才表示用户已允许向其传输你提供的查询内容。`
