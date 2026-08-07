package service

import (
	"fmt"
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ingestionCandidateArchetypeRetrievalDense      = "retrieval_dense"
	ingestionCandidateArchetypeBalanced            = "balanced"
	ingestionCandidateArchetypeStructurePreserving = "structure_preserving"
	ingestionPackingPolicyVersion                  = "semantic-packing-v2"
	ingestionContextTokenPercent                   = 20
	ingestionContextTokenLimit                     = 128
)

var ingestionCandidateGradient = [...]float64{0.55, 0.85, 1.15, 1.45, 1.70}

type ingestionCandidateSpec struct {
	archetype string
	config    types.IngestionChunkingRecommendation
}

func (s *ingestionAgentSession) generateCandidates(evidence ingestionDocumentEvidence) error {
	if s.documentErr != nil {
		return fmt.Errorf("文档结构分析不可用: %w", s.documentErr)
	}
	if err := chunker.ValidateSemanticDocument(s.document); err != nil {
		return fmt.Errorf("文档结构校验失败: %w", err)
	}
	policy := semanticPackingPolicyFromEvidence(evidence)
	specs, err := generateIngestionCandidateSpecs(s)
	if err != nil {
		return err
	}
	candidates := make([]types.IngestionChunkingCandidate, 0, len(specs))
	for _, spec := range specs {
		id, idErr := ingestionCandidateID(spec.config)
		if idErr != nil {
			return idErr
		}
		candidate, buildErr := s.buildCandidate(ingestionCandidateBuildRequest{
			content: s.content, config: spec.config, constraints: s.constraints,
			id: id, document: s.document, archetype: spec.archetype, policy: policy,
		})
		if buildErr != nil {
			return fmt.Errorf("候选 %s 评估失败: %w", spec.archetype, buildErr)
		}
		candidates = append(candidates, candidate)
	}
	dimensions := ingestionEvidenceScoreDimensions(evidence)
	attachIngestionComparisonFacts(candidates, dimensions)
	return s.installGeneratedCandidates(policy, candidates)
}

func (s *ingestionAgentSession) installGeneratedCandidates(
	policy types.SemanticPackingPolicy,
	candidates []types.IngestionChunkingCandidate,
) error {
	if len(candidates) != maxIngestionCandidates {
		return fmt.Errorf("候选数量为 %d，期望 %d", len(candidates), maxIngestionCandidates)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.candidates) > 0 {
		return fmt.Errorf("候选会话已经包含预览结果")
	}
	for _, candidate := range candidates {
		if _, duplicate := s.candidates[candidate.ID]; duplicate {
			return fmt.Errorf("候选生成结果包含重复配置")
		}
		s.candidates[candidate.ID] = cloneIngestionCandidate(candidate)
	}
	s.policy = cloneSemanticPackingPolicy(policy)
	return nil
}

func semanticPackingPolicyFromEvidence(
	evidence ingestionDocumentEvidence,
) types.SemanticPackingPolicy {
	return types.SemanticPackingPolicy{
		Version:                     ingestionPackingPolicyVersion,
		TrustSoftHeadings:           !containsString(evidence.RiskSignals, "unreliable_headings"),
		StrongBoundaryOrder:         append([]string(nil), evidence.BoundaryPriorities...),
		SeparateRecords:             containsString(evidence.DominantStructures, "repeated_records"),
		PreserveRepeatedPageRegions: containsString(evidence.RiskSignals, "repeated_headers_footers"),
		ContextTokenPercent:         ingestionContextTokenPercent,
		ContextTokenLimit:           ingestionContextTokenLimit,
	}
}

func generateIngestionCandidateSpecs(
	session *ingestionAgentSession,
) ([]ingestionCandidateSpec, error) {
	base, err := normalizedCandidateBase(session.fallback, session.constraints)
	if err != nil {
		return nil, err
	}
	p90, err := highConfidenceAtomicP90(session)
	if err != nil {
		return nil, err
	}
	targets := []struct {
		archetype string
		size      int
	}{
		{ingestionCandidateArchetypeRetrievalDense, scaledSize(base.ChunkSize, 0.70)},
		{ingestionCandidateArchetypeBalanced, max(base.ChunkSize, p90)},
		{ingestionCandidateArchetypeStructurePreserving, scaledSize(base.ChunkSize, 1.30)},
	}
	specs := make([]ingestionCandidateSpec, 0, len(targets))
	used := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		config, configErr := candidateConfigForArchetype(base, target.archetype, target.size, session.constraints)
		if configErr != nil {
			return nil, configErr
		}
		config, configErr = makeCandidateConfigDistinct(
			config, target.archetype, base.ChunkSize, session.constraints, used,
		)
		if configErr != nil {
			return nil, configErr
		}
		id, _ := ingestionCandidateID(config)
		used[id] = struct{}{}
		specs = append(specs, ingestionCandidateSpec{archetype: target.archetype, config: config})
	}
	return specs, nil
}

func normalizedCandidateBase(
	fallback types.IngestionChunkingRecommendation,
	constraints types.IngestionChunkingConstraints,
) (types.IngestionChunkingRecommendation, error) {
	config := cloneChunkingRecommendation(fallback)
	config.Strategy = chunker.StrategyAuto
	config.ChunkSize = clampInt(defaultPositive(config.ChunkSize, chunker.DefaultChunkSize), minimumAdvisorChunkSize, maximumAdvisorChunkSize)
	config.ParentChunkSize = clampInt(defaultPositive(config.ParentChunkSize, 4096), minimumAdvisorParentSize, maximumAdvisorParentSize)
	config.ChildChunkSize = clampInt(defaultPositive(config.ChildChunkSize, 384), minimumAdvisorChildSize, maximumAdvisorChildSize)
	config.ChildChunkSize = min(config.ChildChunkSize, config.ParentChunkSize)
	config.ChunkOverlap = clampInt(config.ChunkOverlap, 0, min(maximumAdvisorOverlap, config.ChunkSize/2))
	if len(config.Separators) == 0 {
		config.Separators = append([]string(nil), chunker.DefaultConfig().Separators...)
	}
	return normalizeIngestionPreviewConfig(config, constraints)
}

func candidateConfigForArchetype(
	base types.IngestionChunkingRecommendation,
	archetype string,
	targetSize int,
	constraints types.IngestionChunkingConstraints,
) (types.IngestionChunkingRecommendation, error) {
	config := cloneChunkingRecommendation(base)
	config.ChunkSize = clampInt(targetSize, minimumAdvisorChunkSize, maximumAdvisorChunkSize)
	config.ChunkOverlap = min(config.ChunkOverlap, config.ChunkSize/2)
	switch archetype {
	case ingestionCandidateArchetypeRetrievalDense:
		config.EnableParentChild = true
		config.ChildChunkSize = clampInt(config.ChunkSize, minimumAdvisorChildSize, maximumAdvisorChildSize)
		config.ParentChunkSize = clampInt(config.ChildChunkSize*4, minimumAdvisorParentSize, maximumAdvisorParentSize)
	case ingestionCandidateArchetypeBalanced:
	case ingestionCandidateArchetypeStructurePreserving:
		config.ChunkOverlap = 0
	default:
		return types.IngestionChunkingRecommendation{}, fmt.Errorf("未知候选原型 %q", archetype)
	}
	return normalizeIngestionPreviewConfig(config, constraints)
}

func makeCandidateConfigDistinct(
	config types.IngestionChunkingRecommendation,
	archetype string,
	baseSize int,
	constraints types.IngestionChunkingConstraints,
	used map[string]struct{},
) (types.IngestionChunkingRecommendation, error) {
	id, err := ingestionCandidateID(config)
	if err != nil {
		return types.IngestionChunkingRecommendation{}, err
	}
	if _, duplicate := used[id]; !duplicate {
		return config, nil
	}
	for _, ratio := range ingestionCandidateGradient {
		candidate, candidateErr := candidateConfigForArchetype(
			config, archetype, scaledSize(baseSize, ratio), constraints,
		)
		if candidateErr != nil {
			return types.IngestionChunkingRecommendation{}, candidateErr
		}
		candidateID, candidateIDErr := ingestionCandidateID(candidate)
		if candidateIDErr != nil {
			return types.IngestionChunkingRecommendation{}, candidateIDErr
		}
		if _, duplicate := used[candidateID]; !duplicate {
			return candidate, nil
		}
	}
	return types.IngestionChunkingRecommendation{}, fmt.Errorf("无法为 %s 生成第三个不同候选", archetype)
}

func highConfidenceAtomicP90(session *ingestionAgentSession) (int, error) {
	runes := []rune(session.content)
	lengths := make([]int, 0, len(session.document.Blocks))
	counter := session.constraints.TokenCounter
	if counter == nil {
		var err error
		counter, err = chunker.NewTokenCounter(chunker.TokenCounterConfig{
			Encoding: chunker.TokenizerEncodingByteUpperBound,
		})
		if err != nil {
			return 0, fmt.Errorf("创建保守 token counter 失败: %w", err)
		}
	}
	for _, block := range session.document.Blocks {
		if !block.Atomic || block.Confidence != chunker.SemanticConfidenceHigh {
			continue
		}
		count, err := counter.Count(string(runes[block.Start:block.End]))
		if err != nil {
			return 0, fmt.Errorf("统计高置信原子 token 失败: %w", err)
		}
		if session.constraints.TokenLimit > 0 && count.Count > session.constraints.TokenLimit {
			continue
		}
		lengths = append(lengths, block.End-block.Start)
	}
	if len(lengths) == 0 {
		return 0, nil
	}
	sort.Ints(lengths)
	index := int(math.Ceil(float64(len(lengths))*0.90)) - 1
	return lengths[max(0, index)], nil
}

func scaledSize(value int, ratio float64) int {
	return int(math.Round(float64(value) * ratio))
}

func defaultPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func clampInt(value, minimum, maximum int) int {
	return min(maximum, max(minimum, value))
}
