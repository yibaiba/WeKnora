package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

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
	content     string
	profile     types.IngestionDocumentProfile
	constraints types.IngestionChunkingConstraints

	mu             sync.RWMutex
	candidates     map[string]types.IngestionChunkingCandidate
	inFlight       map[string]*ingestionCandidateFlight
	buildCandidate ingestionCandidateBuilder
	decision       *types.IngestionAnalysis
	selectedID     string
}

type ingestionCandidateBuilder func(ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error)

type ingestionCandidateFlight struct {
	done      chan struct{}
	candidate types.IngestionChunkingCandidate
	err       error
}

type ingestionCandidateBuildResult struct {
	candidate types.IngestionChunkingCandidate
	err       error
}

func newIngestionAgentSession(
	content string,
	constraints types.IngestionChunkingConstraints,
) *ingestionAgentSession {
	return newIngestionAgentSessionWithProfile(
		content, constraints, BuildIngestionDocumentProfile(content),
	)
}

func newIngestionAgentSessionForPromptVersion(
	content string,
	constraints types.IngestionChunkingConstraints,
	promptVersion string,
) *ingestionAgentSession {
	if promptVersion == types.IngestionPromptVersionV1 {
		return newIngestionAgentSession(content, constraints)
	}
	return newIngestionAgentSessionWithProfile(content, constraints, types.IngestionDocumentProfile{
		Statistics: BuildIngestionDocumentStatistics(content),
	})
}

func newIngestionAgentSessionWithProfile(
	content string,
	constraints types.IngestionChunkingConstraints,
	profile types.IngestionDocumentProfile,
) *ingestionAgentSession {
	return &ingestionAgentSession{
		content: content,
		profile: profile,
		constraints: types.IngestionChunkingConstraints{
			TokenLimit: constraints.TokenLimit,
			Languages:  append([]string(nil), constraints.Languages...),
		},
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

func (s *ingestionAgentSession) candidateCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.candidates)
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
	normalized, err := normalizeIngestionPreviewConfig(config, s.constraints)
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
	candidate, err = s.buildCandidate(ingestionCandidateBuildRequest{
		content: s.content, config: normalized, constraints: s.constraints, id: id,
	})
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
	if err := validateIngestionAnalysisWithConstraints(analysis, s.constraints); err != nil {
		return nil, err
	}
	s.decision = analysis
	s.selectedID = input.CandidateID
	return analysis, nil
}

func normalizeIngestionPreviewConfig(
	value types.IngestionChunkingRecommendation,
	constraints types.IngestionChunkingConstraints,
) (types.IngestionChunkingRecommendation, error) {
	value = cloneChunkingRecommendation(value)
	if err := ValidateIngestionChunkingRecommendation(value); err != nil {
		return types.IngestionChunkingRecommendation{}, err
	}
	base := normalizeSplitterConfig(ingestionChunkingConfig(value, constraints), true)
	value.ChunkSize = base.ChunkSize
	value.ChunkOverlap = base.ChunkOverlap
	value.Separators = append([]string(nil), base.Separators...)
	if err := validateIngestionChunkingRecommendation(value, constraints); err != nil {
		return types.IngestionChunkingRecommendation{}, err
	}
	return value, nil
}

func ingestionChunkingConfig(
	value types.IngestionChunkingRecommendation,
	constraints types.IngestionChunkingConstraints,
) types.ChunkingConfig {
	return types.ChunkingConfig{
		Strategy:          value.Strategy,
		ChunkSize:         value.ChunkSize,
		ChunkOverlap:      value.ChunkOverlap,
		EnableParentChild: value.EnableParentChild,
		ParentChunkSize:   value.ParentChunkSize,
		ChildChunkSize:    value.ChildChunkSize,
		Separators:        append([]string(nil), value.Separators...),
		TokenLimit:        constraints.TokenLimit,
		Languages:         append([]string(nil), constraints.Languages...),
	}
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
