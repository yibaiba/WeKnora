package service

import (
	"context"
	"encoding/json"
	"fmt"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

type previewIngestionChunking struct {
	agenttools.BaseTool
	session *ingestionAgentSession
}

type previewIngestionChunkingOutput struct {
	CandidateID         string                           `json:"candidate_id"`
	SavedCandidateCount int                              `json:"saved_candidate_count"`
	CandidateLimit      int                              `json:"candidate_limit"`
	NextAction          string                           `json:"next_action"`
	Candidate           types.IngestionChunkingCandidate `json:"candidate"`
}

func newPreviewIngestionChunking(session *ingestionAgentSession) *previewIngestionChunking {
	return &previewIngestionChunking{
		BaseTool: agenttools.NewBaseTool(
			previewIngestionChunkingTool,
			"Run the production chunker with a validated candidate configuration. chunk_overlap must not exceed half of chunk_size, and child_chunk_size must not exceed parent_chunk_size. Success returns candidate_id, deterministic metrics, and next_action; submit immediately when next_action is submit_ingestion_decision.",
			previewIngestionChunkingSchema(),
		),
		session: session,
	}
}

func previewIngestionChunkingSchema() json.RawMessage {
	properties := map[string]any{
		"strategy": map[string]any{
			"type": "string", "enum": allowedChunkingStrategyValues[:],
		},
		"chunk_size": map[string]any{
			"type": "integer", "minimum": minimumAdvisorChunkSize, "maximum": maximumAdvisorChunkSize,
		},
		"chunk_overlap": map[string]any{
			"type": "integer", "minimum": 0, "maximum": maximumAdvisorOverlap,
			"description": "Must be at most half of chunk_size.",
		},
		"enable_parent_child": map[string]any{"type": "boolean"},
		"parent_chunk_size": map[string]any{
			"type": "integer", "minimum": minimumAdvisorParentSize, "maximum": maximumAdvisorParentSize,
		},
		"child_chunk_size": map[string]any{
			"type": "integer", "minimum": minimumAdvisorChildSize, "maximum": maximumAdvisorChildSize,
			"description": "Must not exceed parent_chunk_size.",
		},
		"separators": map[string]any{
			"type": "array", "minItems": 1,
			"items": map[string]any{"type": "string", "enum": allowedIngestionSeparatorValues[:]},
		},
	}
	schema, err := json.Marshal(map[string]any{
		"type": "object", "properties": properties,
		"required": []string{
			"strategy", "chunk_size", "chunk_overlap", "enable_parent_child",
			"parent_chunk_size", "child_chunk_size", "separators",
		},
		"additionalProperties": false,
	})
	if err != nil {
		panic(fmt.Sprintf("marshal ingestion preview schema: %v", err))
	}
	return schema
}

func (t *previewIngestionChunking) Execute(
	_ context.Context,
	raw json.RawMessage,
) (*types.ToolResult, error) {
	var input types.IngestionChunkingRecommendation
	if err := decodeIngestionToolInput(raw, &input); err != nil {
		return ingestionToolFailure(wrapIngestionToolError(
			err, ingestionFailureArgumentsInvalid, "", "json_schema", "候选参数无效",
		))
	}
	candidate, err := t.session.preview(input)
	if err != nil {
		return ingestionToolFailure(err)
	}
	candidateCount := t.session.candidateCount()
	return ingestionToolJSON(previewIngestionChunkingOutput{
		CandidateID:         candidate.ID,
		SavedCandidateCount: candidateCount,
		CandidateLimit:      maxIngestionCandidates,
		NextAction:          ingestionPreviewNextAction(candidateCount),
		Candidate:           candidate,
	}, map[string]interface{}{
		"candidate_id": candidate.ID,
		"score":        candidate.Score.Total,
	})
}

func ingestionPreviewNextAction(candidateCount int) string {
	if candidateCount >= maxIngestionCandidates {
		return submitIngestionDecisionTool
	}
	return "preview_or_submit"
}

type submitIngestionDecisionInput struct {
	CandidateID            string   `json:"candidate_id"`
	DocumentKind           string   `json:"document_kind"`
	Confidence             float64  `json:"confidence"`
	RecommendedContentMode string   `json:"recommended_content_mode"`
	ReasonCodes            []string `json:"reason_codes"`
	Summary                string   `json:"summary"`
}

type submitIngestionDecision struct {
	agenttools.BaseTool
	session *ingestionAgentSession
}

func newSubmitIngestionDecision(session *ingestionAgentSession) *submitIngestionDecision {
	return &submitIngestionDecision{
		BaseTool: agenttools.NewBaseTool(submitIngestionDecisionTool, "Submit one already-previewed hard-valid candidate with the final document profile and structured selection reasons. The first successful call ends the run.", json.RawMessage(`{
  "type":"object",
  "properties":{
    "candidate_id":{"type":"string","minLength":1},
    "document_kind":{"type":"string","enum":["policy_manual","faq","tabular_data","report","meeting_notes","presentation","short_article","mixed_document"]},
    "confidence":{"type":"number","minimum":0,"maximum":1},
    "recommended_content_mode":{"type":"string","enum":["document","faq_candidate","wiki_candidate"]},
    "reason_codes":{"type":"array","minItems":1,"items":{"type":"string","minLength":1}},
    "summary":{"type":"string","minLength":1}
  },
  "required":["candidate_id","document_kind","confidence","recommended_content_mode","reason_codes","summary"],
  "additionalProperties":false
}`)),
		session: session,
	}
}

func (t *submitIngestionDecision) Execute(
	_ context.Context,
	raw json.RawMessage,
) (*types.ToolResult, error) {
	var input submitIngestionDecisionInput
	if err := decodeIngestionToolInput(raw, &input); err != nil {
		return ingestionToolFailure(wrapIngestionToolError(
			err, ingestionFailureArgumentsInvalid, "", "json_schema", "提交决策参数无效",
		))
	}
	analysis, err := t.session.submit(input)
	if err != nil {
		return ingestionToolFailure(wrapIngestionToolError(
			err, ingestionFailureDecisionInvalid, "candidate_id", "previewed_hard_valid_candidate",
			"提交的候选决策无效",
		))
	}
	candidate, ok := t.session.candidate(input.CandidateID)
	if !ok {
		return ingestionToolFailure(newIngestionToolError(
			ingestionFailureDecisionInvalid, "candidate_id", "persisted_candidate",
			fmt.Sprintf("已提交候选 %q 丢失", input.CandidateID),
		))
	}
	return ingestionToolJSON(map[string]interface{}{
		"candidate_id":  input.CandidateID,
		"score":         candidate.Score.Total,
		"accepted":      true,
		"document_kind": analysis.DocumentKind,
	}, map[string]interface{}{"candidate_id": input.CandidateID, "score": candidate.Score.Total})
}
