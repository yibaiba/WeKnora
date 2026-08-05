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
	p.finishToolSpan(key, step)
}

func (p *ingestionAgentSpanProgress) HandleAnalysis(event types.IngestionDocumentAnalysisProgress) {
	if p == nil || p.parent == nil || !isIngestionAnalysisPhase(event.Phase) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	span := p.tracker.BeginSubSpan(
		p.ctx, p.parent, p.nextSpanName(event.Phase), types.SpanKindSubSpan,
		ingestionAnalysisProgressPayload(event),
	)
	if event.Failed {
		p.tracker.FailSpan(
			p.ctx, span, ingestionAdvisorErrorDocumentAnalysis,
			fmt.Sprintf("文档全文 %s 阶段失败", event.Phase), nil,
		)
		return
	}
	p.tracker.EndSpan(p.ctx, span, types.JSONMap{})
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
	queue := p.active[key]
	if len(queue) == 0 {
		return
	}
	span := queue[0]
	if len(queue) == 1 {
		delete(p.active, key)
	} else {
		p.active[key] = queue[1:]
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
	return fmt.Sprintf("%d:%s", step.Round, step.ToolName)
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

func ingestionAnalysisProgressPayload(event types.IngestionDocumentAnalysisProgress) types.JSONMap {
	return types.JSONMap{
		"unit_count":         event.UnitCount,
		"completed":          event.Completed,
		"level":              event.Level,
		"duration_ms":        event.DurationMS,
		"covered_characters": event.CoveredCharacters,
	}
}
