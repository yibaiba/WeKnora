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

func newPreviewIngestionChunking(session *ingestionAgentSession) *previewIngestionChunking {
	return &previewIngestionChunking{
		BaseTool: agenttools.NewBaseTool(previewIngestionChunkingTool, "Run the production chunker with a normalized candidate configuration and return deterministic metrics and a 0-100 score.", json.RawMessage(`{
  "type":"object",
  "properties":{
    "strategy":{"type":"string","enum":["auto","heading","heuristic","legacy","recursive"]},
    "chunk_size":{"type":"integer","minimum":100,"maximum":4000},
    "chunk_overlap":{"type":"integer","minimum":0,"maximum":500},
    "enable_parent_child":{"type":"boolean"},
    "parent_chunk_size":{"type":"integer","minimum":512,"maximum":8192},
    "child_chunk_size":{"type":"integer","minimum":64,"maximum":2048},
    "separators":{"type":"array","minItems":1,"items":{"type":"string","enum":["\n\n","\n","。","！","？","；",";"," "]}}
  },
  "required":["strategy","chunk_size","chunk_overlap","enable_parent_child","parent_chunk_size","child_chunk_size","separators"],
  "additionalProperties":false
}`)),
		session: session,
	}
}

func (t *previewIngestionChunking) Execute(
	_ context.Context,
	raw json.RawMessage,
) (*types.ToolResult, error) {
	var input types.IngestionChunkingRecommendation
	if err := decodeIngestionToolInput(raw, &input); err != nil {
		return ingestionToolFailure(fmt.Errorf("候选参数无效: %w", err))
	}
	candidate, err := t.session.preview(input)
	if err != nil {
		return ingestionToolFailure(err)
	}
	return ingestionToolJSON(candidate, map[string]interface{}{
		"candidate_id": candidate.ID,
		"score":        candidate.Score.Total,
	})
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
		return ingestionToolFailure(fmt.Errorf("提交决策参数无效: %w", err))
	}
	analysis, err := t.session.submit(input)
	if err != nil {
		return ingestionToolFailure(err)
	}
	candidate, ok := t.session.candidate(input.CandidateID)
	if !ok {
		return ingestionToolFailure(fmt.Errorf("已提交候选 %q 丢失", input.CandidateID))
	}
	return ingestionToolJSON(map[string]interface{}{
		"candidate_id":  input.CandidateID,
		"score":         candidate.Score.Total,
		"accepted":      true,
		"document_kind": analysis.DocumentKind,
	}, map[string]interface{}{"candidate_id": input.CandidateID, "score": candidate.Score.Total})
}
