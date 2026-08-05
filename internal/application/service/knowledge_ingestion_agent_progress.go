package service

import (
	"context"
	"fmt"
	"sync"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

const ingestionToolSpanErrorCode = "INGESTION_AGENT_TOOL_FAILED"

type ingestionAgentSpanProgress struct {
	ctx     context.Context
	tracker SpanTracker
	parent  *Span

	mu     sync.Mutex
	next   int
	active map[string][]*Span
}

func newIngestionAgentSpanProgress(
	ctx context.Context,
	tracker SpanTracker,
	parent *Span,
) *ingestionAgentSpanProgress {
	return &ingestionAgentSpanProgress{
		ctx: ctx, tracker: tracker, parent: parent, active: make(map[string][]*Span),
	}
}

func (p *ingestionAgentSpanProgress) RecordProfile(characterCount int) {
	if p == nil || p.parent == nil {
		return
	}
	span := p.tracker.BeginSubSpan(
		p.ctx, p.parent, p.nextSpanName("analyze_document"), types.SpanKindSubSpan,
		types.JSONMap{"phase": "analyze_document", "character_count": characterCount},
	)
	p.tracker.EndSpan(p.ctx, span, types.JSONMap{"status": "profiled"})
}

func (p *ingestionAgentSpanProgress) Handle(step types.IngestionAgentStep) {
	if p == nil || p.parent == nil || step.ToolName == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := ingestionProgressKey(step)
	if step.Status == "running" {
		p.startToolSpan(key, step)
		return
	}
	step = sanitizeIngestionProgressStep(step)
	p.finishToolSpan(key, step)
}

func (p *ingestionAgentSpanProgress) HandleAnalysis(event types.IngestionDocumentAnalysisProgress) {
	if p == nil || p.parent == nil || !isIngestionAnalysisPhase(event.Phase) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := ingestionAnalysisProgressKey(event)
	if event.Status == ingestionAnalysisProgressRunning {
		p.startAnalysisSpan(key, event)
		return
	}
	if len(p.active[key]) == 0 {
		p.startAnalysisSpan(key, event)
	}
	p.finishAnalysisSpan(key, event)
}

func (p *ingestionAgentSpanProgress) startAnalysisSpan(
	key string,
	event types.IngestionDocumentAnalysisProgress,
) {
	span := p.tracker.BeginSubSpan(
		p.ctx, p.parent, p.nextSpanName(event.Phase), types.SpanKindSubSpan,
		ingestionAnalysisStartPayload(event),
	)
	if span != nil {
		p.active[key] = append(p.active[key], span)
	}
}

func (p *ingestionAgentSpanProgress) finishAnalysisSpan(
	key string,
	event types.IngestionDocumentAnalysisProgress,
) {
	span := p.popActiveSpan(key)
	if span == nil {
		return
	}
	if event.Failed || event.Status == ingestionAnalysisProgressFailed {
		p.tracker.FailSpan(
			p.ctx, span, ingestionAdvisorErrorDocumentAnalysis,
			ingestionAnalysisFailureMessage(event), nil,
		)
		return
	}
	p.tracker.EndSpan(p.ctx, span, ingestionAnalysisResultPayload(event))
}

func sanitizeIngestionProgressStep(step types.IngestionAgentStep) types.IngestionAgentStep {
	if step.FailureCode == "" && step.FailureField == "" && step.FailureConstraint == "" {
		return step
	}
	failure := sanitizeIngestionToolFailure(&types.ToolFailure{
		Code: step.FailureCode, Field: step.FailureField, Constraint: step.FailureConstraint,
	})
	step.FailureCode = failure.Code
	step.FailureField = failure.Field
	step.FailureConstraint = failure.Constraint
	return step
}

func (p *ingestionAgentSpanProgress) RecordEvaluation(result *types.IngestionAdvisorResult) {
	if p == nil || p.parent == nil || result == nil {
		return
	}
	input := types.JSONMap{
		"phase": "evaluate_and_refine", "candidate_count": len(result.Candidates),
	}
	span := p.tracker.BeginSubSpan(
		p.ctx, p.parent, p.nextSpanName("evaluate_and_refine"), types.SpanKindSubSpan, input,
	)
	p.tracker.EndSpan(p.ctx, span, types.JSONMap{
		"selected_candidate_id": result.SelectedCandidateID,
	})
}

func (p *ingestionAgentSpanProgress) startToolSpan(key string, step types.IngestionAgentStep) {
	phase := ingestionPhaseForTool(step.ToolName)
	span := p.tracker.BeginSubSpan(
		p.ctx, p.parent, p.nextSpanName(phase), types.SpanKindSubSpan,
		types.JSONMap{"phase": phase, "round": step.Round, "tool_name": step.ToolName},
	)
	if span != nil {
		p.active[key] = append(p.active[key], span)
	}
}

func (p *ingestionAgentSpanProgress) finishToolSpan(key string, step types.IngestionAgentStep) {
	span := p.popActiveSpan(key)
	if span == nil {
		return
	}
	if step.Status == "failed" {
		code := step.FailureCode
		if code == "" {
			code = ingestionToolSpanErrorCode
		}
		p.tracker.FailSpan(
			p.ctx, span, code, ingestionToolSpanFailureMessage(step), nil,
		)
		return
	}
	p.tracker.EndSpan(p.ctx, span, types.JSONMap{
		"status": step.Status, "duration_ms": step.DurationMS,
	})
}

func (p *ingestionAgentSpanProgress) popActiveSpan(key string) *Span {
	queue := p.active[key]
	if len(queue) == 0 {
		return nil
	}
	span := queue[0]
	if len(queue) == 1 {
		delete(p.active, key)
	} else {
		p.active[key] = queue[1:]
	}
	return span
}

func ingestionToolSpanFailureMessage(step types.IngestionAgentStep) string {
	if step.FailureCode == "" {
		return fmt.Sprintf("入库工具 %s 执行失败，详情已脱敏", step.ToolName)
	}
	return fmt.Sprintf(
		"入库工具 %s 执行失败（错误码 %s，字段 %s，约束 %s）",
		step.ToolName, step.FailureCode,
		safeFailureValue(step.FailureField), safeFailureValue(step.FailureConstraint),
	)
}

func (p *ingestionAgentSpanProgress) nextSpanName(phase string) string {
	p.next++
	return fmt.Sprintf("document_analysis.%s[%d]", phase, p.next)
}

func ingestionProgressKey(step types.IngestionAgentStep) string {
	if step.ToolCallID != "" {
		return "tool_call:" + step.ToolCallID
	}
	return fmt.Sprintf("%d:%s", step.Round, step.ToolName)
}

func ingestionAnalysisProgressKey(event types.IngestionDocumentAnalysisProgress) string {
	return fmt.Sprintf("analysis:%s:%d", event.Phase, event.Level)
}

func ingestionPhaseForTool(toolName string) string {
	switch toolName {
	case previewIngestionChunkingTool:
		return "preview_candidates"
	case submitIngestionDecisionTool:
		return "submit_decision"
	case agenttools.ToolThinking:
		return "evaluate_and_refine"
	default:
		return "readonly_tools"
	}
}

func isIngestionAnalysisPhase(phase string) bool {
	return phase == "map_document" || phase == "reduce_document"
}

func ingestionAnalysisStartPayload(event types.IngestionDocumentAnalysisProgress) types.JSONMap {
	return types.JSONMap{
		"unit_count":              event.UnitCount,
		"level":                   event.Level,
		"covered_characters":      event.CoveredCharacters,
		"context_window_tokens":   event.ContextWindowTokens,
		"completion_token_budget": event.CompletionTokenBudget,
		"prompt_schema_tokens":    event.PromptSchemaTokens,
		"safety_tokens":           event.SafetyTokens,
		"content_token_budget":    event.ContentTokenBudget,
		"estimated_source_tokens": event.EstimatedSourceTokens,
	}
}

func ingestionAnalysisResultPayload(event types.IngestionDocumentAnalysisProgress) types.JSONMap {
	return types.JSONMap{
		"completed":          event.Completed,
		"duration_ms":        event.DurationMS,
		"covered_characters": event.CoveredCharacters,
		"retry_count":        event.RetryCount,
	}
}

func ingestionAnalysisFailureMessage(event types.IngestionDocumentAnalysisProgress) string {
	message := fmt.Sprintf(
		"文档全文 %s 阶段失败（完成 %d/%d，耗时 %dms，覆盖字符 %d，重试 %d 次）",
		event.Phase, event.Completed, event.UnitCount, event.DurationMS,
		event.CoveredCharacters, event.RetryCount,
	)
	if event.FailedUnit <= 0 {
		return message
	}
	message = fmt.Sprintf(
		"%s；失败单元 %d（尝试 %d 次）：%s",
		message, event.FailedUnit, event.FailedUnitAttempts,
		ingestionDocumentAnalysisFailureLabel(event.FailureKind),
	)
	if event.ProviderFailureKind == "" && event.HTTPStatus <= 0 && event.FailureParameter == "" {
		return message
	}
	return message + ingestionDocumentAnalysisFailureSuffix(ingestionDocumentAnalysisFailureMetadata{
		ProviderKind: event.ProviderFailureKind,
		HTTPStatus:   event.HTTPStatus, Parameter: event.FailureParameter,
	})
}
