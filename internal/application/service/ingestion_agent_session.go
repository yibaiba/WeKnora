package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	inspectIngestionDocumentTool = "inspect_ingestion_document"
	previewIngestionChunkingTool = "preview_ingestion_chunking"
	submitIngestionDecisionTool  = "submit_ingestion_decision"
	maxIngestionInspectRunes     = 8000
	maxIngestionCandidates       = 3
)

type ingestionAgentSession struct {
	content string
	profile types.IngestionDocumentProfile

	mu             sync.RWMutex
	candidates     map[string]types.IngestionChunkingCandidate
	inFlight       map[string]*ingestionCandidateFlight
	buildCandidate ingestionCandidateBuilder
	decision       *types.IngestionAnalysis
	selectedID     string
}

type ingestionCandidateBuilder func(
	content string,
	config types.IngestionChunkingRecommendation,
	id string,
) (types.IngestionChunkingCandidate, error)

type ingestionCandidateFlight struct {
	done      chan struct{}
	candidate types.IngestionChunkingCandidate
	err       error
}

type ingestionCandidateBuildResult struct {
	candidate types.IngestionChunkingCandidate
	err       error
}

func newIngestionAgentSession(content string) *ingestionAgentSession {
	return &ingestionAgentSession{
		content:        content,
		profile:        BuildIngestionDocumentProfile(content),
		candidates:     make(map[string]types.IngestionChunkingCandidate),
		inFlight:       make(map[string]*ingestionCandidateFlight),
		buildCandidate: buildIngestionCandidate,
	}
}

func (s *ingestionAgentSession) candidateSnapshot() []types.IngestionChunkingCandidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.candidates))
	for id := range s.candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]types.IngestionChunkingCandidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneIngestionCandidate(s.candidates[id]))
	}
	return result
}

func (s *ingestionAgentSession) decisionSnapshot() *types.IngestionAnalysis {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.decision == nil {
		return nil
	}
	result := *s.decision
	result.ReasonCodes = append([]string(nil), s.decision.ReasonCodes...)
	result.RecommendedChunking = cloneChunkingRecommendation(s.decision.RecommendedChunking)
	return &result
}

func (s *ingestionAgentSession) selectedCandidateID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selectedID
}

func (s *ingestionAgentSession) candidate(id string) (types.IngestionChunkingCandidate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidate, ok := s.candidates[id]
	return cloneIngestionCandidate(candidate), ok
}

func (s *ingestionAgentSession) preview(
	config types.IngestionChunkingRecommendation,
) (types.IngestionChunkingCandidate, error) {
	normalized, err := normalizeIngestionPreviewConfig(config)
	if err != nil {
		return types.IngestionChunkingCandidate{}, err
	}
	id, err := ingestionCandidateID(normalized)
	if err != nil {
		return types.IngestionChunkingCandidate{}, err
	}

	candidate, flight, owner, err := s.reservePreview(id)
	if err != nil {
		return types.IngestionChunkingCandidate{}, err
	}
	if candidate.ID != "" {
		return candidate, nil
	}
	if !owner {
		<-flight.done
		return cloneIngestionCandidate(flight.candidate), flight.err
	}
	candidate, err = s.buildCandidate(s.content, normalized, id)
	s.completePreview(id, flight, ingestionCandidateBuildResult{candidate: candidate, err: err})
	return cloneIngestionCandidate(candidate), err
}

func (s *ingestionAgentSession) reservePreview(
	id string,
) (types.IngestionChunkingCandidate, *ingestionCandidateFlight, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if candidate, ok := s.candidates[id]; ok {
		return cloneIngestionCandidate(candidate), nil, false, nil
	}
	if flight, ok := s.inFlight[id]; ok {
		return types.IngestionChunkingCandidate{}, flight, false, nil
	}
	if len(s.candidates)+len(s.inFlight) >= maxIngestionCandidates {
		return types.IngestionChunkingCandidate{}, nil, false,
			fmt.Errorf("每个文档最多预览 %d 个不同候选", maxIngestionCandidates)
	}
	flight := &ingestionCandidateFlight{done: make(chan struct{})}
	s.inFlight[id] = flight
	return types.IngestionChunkingCandidate{}, flight, true, nil
}

func (s *ingestionAgentSession) completePreview(
	id string,
	flight *ingestionCandidateFlight,
	result ingestionCandidateBuildResult,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
	if result.err == nil {
		s.candidates[id] = result.candidate
	}
	flight.candidate = result.candidate
	flight.err = result.err
	close(flight.done)
}

func (s *ingestionAgentSession) submit(input submitIngestionDecisionInput) (*types.IngestionAnalysis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decision != nil {
		return nil, fmt.Errorf("入库决策已经提交")
	}
	candidate, ok := s.candidates[input.CandidateID]
	if !ok {
		return nil, fmt.Errorf("候选 %q 未预览或不存在", input.CandidateID)
	}
	if !candidate.HardValid {
		return nil, fmt.Errorf("候选 %q 未通过硬校验", input.CandidateID)
	}
	analysis := &types.IngestionAnalysis{
		DocumentKind:           input.DocumentKind,
		Confidence:             input.Confidence,
		RecommendedContentMode: input.RecommendedContentMode,
		ReasonCodes:            append([]string(nil), input.ReasonCodes...),
		Summary:                input.Summary,
		RecommendedChunking:    cloneChunkingRecommendation(candidate.Config),
	}
	if err := ValidateIngestionAnalysis(analysis); err != nil {
		return nil, err
	}
	s.decision = analysis
	s.selectedID = input.CandidateID
	return analysis, nil
}

func normalizeIngestionPreviewConfig(
	value types.IngestionChunkingRecommendation,
) (types.IngestionChunkingRecommendation, error) {
	value = cloneChunkingRecommendation(value)
	if err := ValidateIngestionChunkingRecommendation(value); err != nil {
		return types.IngestionChunkingRecommendation{}, err
	}
	base := chunker.NormalizeConfig(chunker.SplitterConfig{
		ChunkSize:        value.ChunkSize,
		ChunkOverlap:     value.ChunkOverlap,
		AllowZeroOverlap: true,
		Separators:       append([]string(nil), value.Separators...),
		Strategy:         value.Strategy,
	})
	value.ChunkSize = base.ChunkSize
	value.ChunkOverlap = base.ChunkOverlap
	value.Separators = append([]string(nil), base.Separators...)
	if err := ValidateIngestionChunkingRecommendation(value); err != nil {
		return types.IngestionChunkingRecommendation{}, err
	}
	return value, nil
}

func ingestionCandidateID(value types.IngestionChunkingRecommendation) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("序列化候选配置失败: %w", err)
	}
	digest := sha256.Sum256(payload)
	return "cand_" + hex.EncodeToString(digest[:6]), nil
}

func cloneIngestionCandidate(value types.IngestionChunkingCandidate) types.IngestionChunkingCandidate {
	value.Config = cloneChunkingRecommendation(value.Config)
	value.Structure.PresentTypes = append([]string(nil), value.Structure.PresentTypes...)
	value.Diagnostics.TierChain = append([]string(nil), value.Diagnostics.TierChain...)
	value.Diagnostics.Rejected = append([]types.IngestionTierRejection(nil), value.Diagnostics.Rejected...)
	value.Violations = append([]string(nil), value.Violations...)
	return value
}

func decodeIngestionToolInput(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("包含额外 JSON 值")
		}
		return err
	}
	return nil
}

func ingestionToolFailure(err error) (*types.ToolResult, error) {
	return &types.ToolResult{Success: false, Error: err.Error()}, nil
}

func ingestionToolJSON(value any, data map[string]interface{}) (*types.ToolResult, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &types.ToolResult{Success: true, Output: string(payload), Data: data}, nil
}

type inspectIngestionDocumentInput struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type inspectIngestionDocumentOutput struct {
	Offset          int                          `json:"offset"`
	NextOffset      int                          `json:"next_offset"`
	TotalCharacters int                          `json:"total_characters"`
	HasMore         bool                         `json:"has_more"`
	Content         string                       `json:"content"`
	Statistics      types.DocumentStructureStats `json:"statistics"`
}

type inspectIngestionDocument struct {
	agenttools.BaseTool
	session *ingestionAgentSession
}

func newInspectIngestionDocument(session *ingestionAgentSession) *inspectIngestionDocument {
	return &inspectIngestionDocument{
		BaseTool: agenttools.NewBaseTool(inspectIngestionDocumentTool, "Read the current ingestion document by rune offset and inspect its full-document statistics.", json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":8000}},"required":["offset","limit"],"additionalProperties":false}`)),
		session:  session,
	}
}

func (t *inspectIngestionDocument) Execute(
	_ context.Context,
	raw json.RawMessage,
) (*types.ToolResult, error) {
	var input inspectIngestionDocumentInput
	if err := decodeIngestionToolInput(raw, &input); err != nil {
		return ingestionToolFailure(fmt.Errorf("读取文档参数无效: %w", err))
	}
	runes := []rune(t.session.content)
	if input.Offset < 0 || input.Offset > len(runes) {
		return ingestionToolFailure(fmt.Errorf("offset 必须在 0 到 %d 之间", len(runes)))
	}
	if input.Limit < 1 || input.Limit > maxIngestionInspectRunes {
		return ingestionToolFailure(fmt.Errorf("limit 必须在 1 到 %d 之间", maxIngestionInspectRunes))
	}
	end := min(input.Offset+input.Limit, len(runes))
	return ingestionToolJSON(inspectIngestionDocumentOutput{
		Offset:          input.Offset,
		NextOffset:      end,
		TotalCharacters: len(runes),
		HasMore:         end < len(runes),
		Content:         string(runes[input.Offset:end]),
		Statistics:      t.session.profile.Statistics,
	}, map[string]interface{}{"offset": input.Offset, "next_offset": end, "has_more": end < len(runes)})
}
