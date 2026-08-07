package service

import (
	"context"
	"encoding/json"
	"fmt"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

type submitIngestionFallbackInput struct {
	DocumentKind           string   `json:"document_kind"`
	Confidence             float64  `json:"confidence"`
	RecommendedContentMode string   `json:"recommended_content_mode"`
	ReasonCodes            []string `json:"reason_codes"`
	Summary                string   `json:"summary"`
}

type submitIngestionFallback struct {
	agenttools.BaseTool
	session *ingestionAgentSession
}

func newSubmitIngestionFallback(session *ingestionAgentSession) *submitIngestionFallback {
	return &submitIngestionFallback{
		BaseTool: agenttools.NewBaseTool(
			submitIngestionFallbackTool,
			"Submit the document profile and use the knowledge base's original ordinary chunking configuration. This tool is available only when all three backend-generated candidates are structurally invalid.",
			json.RawMessage(`{
  "type":"object",
  "properties":{
    "document_kind":{"type":"string","enum":["policy_manual","faq","tabular_data","report","meeting_notes","presentation","short_article","mixed_document"]},
    "confidence":{"type":"number","minimum":0,"maximum":1},
    "recommended_content_mode":{"type":"string","enum":["document","faq_candidate","wiki_candidate"]},
    "reason_codes":{"type":"array","minItems":1,"items":{"type":"string","minLength":1}},
    "summary":{"type":"string","minLength":1}
  },
  "required":["document_kind","confidence","recommended_content_mode","reason_codes","summary"],
  "additionalProperties":false
}`),
		),
		session: session,
	}
}

func (t *submitIngestionFallback) Execute(
	_ context.Context,
	raw json.RawMessage,
) (*types.ToolResult, error) {
	var input submitIngestionFallbackInput
	if err := decodeIngestionToolInput(raw, &input); err != nil {
		return ingestionToolFailure(wrapIngestionToolError(
			err, ingestionFailureArgumentsInvalid, "", "json_schema", "回退决策参数无效",
		))
	}
	analysis, err := t.session.submitFallback(input)
	if err != nil {
		return ingestionToolFailure(wrapIngestionToolError(
			err, ingestionFailureDecisionInvalid, "", "all_candidates_structurally_invalid",
			"提交回退决策无效",
		))
	}
	return ingestionToolJSON(map[string]interface{}{
		"accepted": true, "applied_mode": analysis.AppliedMode,
		"fallback_reason_codes": analysis.FallbackReasonCodes,
	}, map[string]interface{}{"applied_mode": analysis.AppliedMode})
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
		BaseTool: agenttools.NewBaseTool(submitIngestionDecisionTool, "Submit one backend-generated candidate whose comparison_facts.selection_eligible is true, together with the final document profile. The first successful call ends the run.", json.RawMessage(`{
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
			err, ingestionFailureDecisionInvalid, "candidate_id", "backend_selection_eligible_candidate",
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
