package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	previewIngestionChunkingTool = "preview_ingestion_chunking"
	submitIngestionDecisionTool  = "submit_ingestion_decision"
	submitIngestionFallbackTool  = "submit_ingestion_fallback"
	maxIngestionCandidates       = 3
)

type ingestionAgentSession struct {
	content     string
	statistics  types.DocumentStructureStats
	constraints types.IngestionChunkingConstraints
	document    chunker.SemanticDocument
	documentErr error
	fallback    types.IngestionChunkingRecommendation
	policy      types.SemanticPackingPolicy

	mu                   sync.RWMutex
	candidates           map[string]types.IngestionChunkingCandidate
	buildCandidate       ingestionCandidateBuilder
	decision             *types.IngestionAnalysis
	selectedID           string
	selectionReasonCodes []string
}

type ingestionCandidateBuilder func(ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error)

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
	result.FallbackReasonCodes = append([]string(nil), s.decision.FallbackReasonCodes...)
	result.RecommendedChunking = cloneChunkingRecommendation(s.decision.RecommendedChunking)
	result.PackingPolicy = cloneSemanticPackingPolicy(s.decision.PackingPolicy)
	return &result
}

func (s *ingestionAgentSession) selectedCandidateID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selectedID
}

func (s *ingestionAgentSession) selectionReasons() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.selectionReasonCodes...)
}

func (s *ingestionAgentSession) defaultCandidateID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, candidate := range s.candidates {
		facts := candidate.ComparisonFacts
		if facts.SelectionEligible && facts.ReferenceCandidateID == candidate.ID {
			return candidate.ID
		}
	}
	return ""
}

func (s *ingestionAgentSession) semanticDocumentSnapshot() *types.SemanticDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	document := chunker.CloneSemanticDocument(s.document)
	return &document
}

func (s *ingestionAgentSession) candidate(id string) (types.IngestionChunkingCandidate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidate, ok := s.candidates[id]
	return cloneIngestionCandidate(candidate), ok
}

func (s *ingestionAgentSession) submit(input submitIngestionDecisionInput) (*types.IngestionAnalysis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decision != nil {
		return nil, fmt.Errorf("入库决策已经提交")
	}
	candidate, ok := s.candidates[input.CandidateID]
	if !ok {
		return nil, fmt.Errorf("后端候选 %q 不存在", input.CandidateID)
	}
	if !candidate.HardValid {
		return nil, fmt.Errorf("候选 %q 未通过硬校验", input.CandidateID)
	}
	if !candidate.ComparisonFacts.SelectionEligible {
		return nil, fmt.Errorf("候选 %q 不满足后端选择约束", input.CandidateID)
	}
	analysis := &types.IngestionAnalysis{
		AppliedMode:            types.IngestionAppliedModeSmart,
		DocumentKind:           input.DocumentKind,
		Confidence:             input.Confidence,
		RecommendedContentMode: input.RecommendedContentMode,
		ReasonCodes:            append([]string(nil), input.ReasonCodes...),
		Summary:                input.Summary,
		RecommendedChunking:    cloneChunkingRecommendation(candidate.Config),
		PackingPolicy:          cloneSemanticPackingPolicy(s.policy),
	}
	if err := validateIngestionAnalysisWithConstraints(analysis, s.constraints); err != nil {
		return nil, err
	}
	s.decision = analysis
	s.selectedID = input.CandidateID
	s.selectionReasonCodes = append(
		[]string(nil), candidate.ComparisonFacts.ReasonCodes...,
	)
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
	value.BlockDescriptions = append(
		[]types.IngestionChunkStructureDescription(nil), value.BlockDescriptions...,
	)
	for index := range value.BlockDescriptions {
		value.BlockDescriptions[index].Kinds = append(
			[]string(nil), value.BlockDescriptions[index].Kinds...,
		)
	}
	value.Diagnostics.TierChain = append([]string(nil), value.Diagnostics.TierChain...)
	value.Diagnostics.Rejected = append([]types.IngestionTierRejection(nil), value.Diagnostics.Rejected...)
	value.Diagnostics.ContextReasonCodes = append(
		[]string(nil), value.Diagnostics.ContextReasonCodes...,
	)
	value.Violations = append([]string(nil), value.Violations...)
	value.ComparisonFacts.EvidenceAdvantages = append(
		[]string(nil), value.ComparisonFacts.EvidenceAdvantages...,
	)
	value.ComparisonFacts.ReasonCodes = append(
		[]string(nil), value.ComparisonFacts.ReasonCodes...,
	)
	return value
}

func cloneSemanticPackingPolicy(value types.SemanticPackingPolicy) types.SemanticPackingPolicy {
	value.StrongBoundaryOrder = append([]string(nil), value.StrongBoundaryOrder...)
	return value
}
