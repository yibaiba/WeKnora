package service

import (
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

func (s *ingestionAgentSession) fallbackReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fallbackReadyLocked()
}

func (s *ingestionAgentSession) fallbackReadyLocked() bool {
	if s.decision != nil || len(s.candidates) != maxIngestionCandidates {
		return false
	}
	for _, candidate := range s.candidates {
		if candidate.HardValid {
			return false
		}
	}
	return true
}

func (s *ingestionAgentSession) submitFallback(
	input submitIngestionFallbackInput,
) (*types.IngestionAnalysis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decision != nil {
		return nil, fmt.Errorf("入库决策已经提交")
	}
	if !s.fallbackReadyLocked() {
		return nil, fmt.Errorf("仅三个已保存候选全部结构无效时允许回退")
	}
	if err := validateOrdinaryChunkingRecommendation(s.fallback); err != nil {
		return nil, fmt.Errorf("知识库原始分块配置不可用: %w", err)
	}
	analysis := s.newFallbackAnalysis(input)
	if err := validateIngestionAnalysisWithConstraints(analysis, s.constraints); err != nil {
		return nil, err
	}
	s.decision = analysis
	s.selectedID = ""
	return analysis, nil
}

func validateOrdinaryChunkingRecommendation(
	value types.IngestionChunkingRecommendation,
) error {
	if value.ChunkSize <= 0 {
		return fmt.Errorf("chunk_size 必须大于 0")
	}
	if value.ChunkOverlap < 0 || value.ChunkOverlap > value.ChunkSize/2 {
		return fmt.Errorf("chunk_overlap 必须在 0 到 chunk_size 的一半之间")
	}
	if value.EnableParentChild &&
		(value.ParentChunkSize <= 0 || value.ChildChunkSize <= 0) {
		return fmt.Errorf("父子分块大小必须大于 0")
	}
	if len(value.Separators) == 0 {
		return fmt.Errorf("separators 不能为空")
	}
	return nil
}

func (s *ingestionAgentSession) newFallbackAnalysis(
	input submitIngestionFallbackInput,
) *types.IngestionAnalysis {
	return &types.IngestionAnalysis{
		AppliedMode:            types.IngestionAppliedModeFallback,
		FallbackReasonCodes:    ingestionFallbackReasonCodes(s.candidateValuesLocked()),
		DocumentKind:           input.DocumentKind,
		Confidence:             input.Confidence,
		RecommendedContentMode: input.RecommendedContentMode,
		ReasonCodes:            append([]string(nil), input.ReasonCodes...),
		Summary:                input.Summary,
		RecommendedChunking:    cloneChunkingRecommendation(s.fallback),
	}
}

func (s *ingestionAgentSession) candidateValuesLocked() []types.IngestionChunkingCandidate {
	result := make([]types.IngestionChunkingCandidate, 0, len(s.candidates))
	for _, candidate := range s.candidates {
		result = append(result, candidate)
	}
	return result
}

func ingestionFallbackReasonCodes(candidates []types.IngestionChunkingCandidate) []string {
	unique := make(map[string]struct{})
	for _, candidate := range candidates {
		for _, violation := range candidate.Violations {
			if violation != "" {
				unique[violation] = struct{}{}
			}
		}
	}
	codes := make([]string, 0, len(unique)+1)
	for violation := range unique {
		codes = append(codes, violation)
	}
	sort.Strings(codes)
	return append([]string{"all_candidates_structurally_invalid"}, codes...)
}
