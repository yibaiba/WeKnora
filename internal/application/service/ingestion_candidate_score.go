package service

import (
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	semanticIntegrityWeight = 50.0
	boundaryQualityWeight   = 20.0
	sizeFitWeight           = 15.0
	contextEfficiencyWeight = 10.0
	parentChildWeight       = 5.0
)

type ingestionCandidateMetrics struct {
	lengths   types.IngestionLengthDistribution
	structure types.IngestionStructureMetrics
	score     types.IngestionCandidateScore
}

type ingestionCandidateMetricsRequest struct {
	content       string
	document      chunker.SemanticDocument
	chunks        []chunker.Chunk
	parents       []chunker.Chunk
	parentIndexes []int
	config        types.IngestionChunkingRecommendation
	scoreConfig   chunker.SplitterConfig
	validation    ingestionCandidateValidationResult
	tokenLimit    int
	policy        types.SemanticPackingPolicy
}

func ingestionPreviewMetrics(request ingestionCandidateMetricsRequest) ingestionCandidateMetrics {
	structure := scoreSemanticStructure(request.document, request.chunks)
	score := types.IngestionCandidateScore{
		SemanticIntegrity: roundScore(
			scoreSemanticIntegrity(request.validation) * semanticIntegrityWeight,
		),
		BoundaryQuality: roundScore(
			scoreBoundaryQuality(request) * boundaryQualityWeight,
		),
		SizeFit: roundScore(
			scoreTokenSizeFit(request) * sizeFitWeight,
		),
		ContextEfficiency: roundScore(
			scoreTokenContextEfficiency(request.validation) * contextEfficiencyWeight,
		),
		ParentChild: roundScore(scoreParentChild(parentChildScoreRequest{
			children: request.chunks, parents: request.parents,
			parentIndexes: request.parentIndexes, enabled: request.config.EnableParentChild,
		}) * parentChildWeight),
	}
	score.Total = roundScore(score.SemanticIntegrity + score.BoundaryQuality +
		score.SizeFit + score.ContextEfficiency + score.ParentChild)
	return ingestionCandidateMetrics{
		lengths:   ingestionTokenLengthDistribution(request.validation.sourceTokens),
		structure: structure, score: score,
	}
}

func scoreSemanticIntegrity(validation ingestionCandidateValidationResult) float64 {
	ratio := 1.0
	if validation.atomicEligible == 0 {
		ratio = 1
	} else {
		ratio = float64(validation.atomicRetained) / float64(validation.atomicEligible)
	}
	if validation.quality.HeaderlessContinuations > 0 ||
		containsIngestionViolation(validation.violations, ingestionViolationCodeContextMissing) {
		ratio *= 0.5
	}
	return ratio
}

func scoreSemanticStructure(
	document chunker.SemanticDocument,
	chunks []chunker.Chunk,
) types.IngestionStructureMetrics {
	metrics := types.IngestionStructureMetrics{
		HeadingRetention: 1, FAQRetention: 1, TableRetention: 1,
	}
	groups := []struct {
		name  string
		kinds map[string]struct{}
		set   func(float64)
	}{
		{name: "heading", kinds: semanticKindSet(chunker.SemanticKindHeading), set: func(value float64) { metrics.HeadingRetention = value }},
		{name: "faq", kinds: semanticKindSet(chunker.SemanticKindFAQ), set: func(value float64) { metrics.FAQRetention = value }},
		{name: "table", kinds: semanticKindSet(chunker.SemanticKindTableHeader, chunker.SemanticKindTableRow), set: func(value float64) { metrics.TableRetention = value }},
	}
	for _, group := range groups {
		total, retained := countRetainedSemanticBlocks(document.Blocks, chunks, group.kinds)
		if total == 0 {
			continue
		}
		metrics.PresentTypes = append(metrics.PresentTypes, group.name)
		group.set(roundScore(float64(retained) / float64(total)))
	}
	return metrics
}

func semanticKindSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func countRetainedSemanticBlocks(
	blocks []chunker.SemanticBlock,
	chunks []chunker.Chunk,
	kinds map[string]struct{},
) (int, int) {
	total, retained := 0, 0
	for _, block := range blocks {
		if _, ok := kinds[block.Kind]; !ok {
			continue
		}
		total++
		if semanticBlockContained(block, chunks) {
			retained++
		}
	}
	return total, retained
}

func scoreChunkSizeBalance(chunks []chunker.Chunk, target int) float64 {
	if len(chunks) == 0 || target <= 0 {
		return 0
	}
	checked := chunks
	if len(chunks) > 1 {
		checked = chunks[:len(chunks)-1]
	}
	total := 0.0
	for _, current := range checked {
		length := float64(len([]rune(current.Content)))
		if length > float64(target) {
			return 0
		}
		total += length
	}
	mean := total / float64(len(checked))
	deviation := 0.0
	for _, current := range checked {
		deviation += math.Abs(float64(len([]rune(current.Content))) - mean)
	}
	penalty := deviation / float64(len(checked)*target)
	return math.Max(0.75, 1-penalty)
}

func scoreSizeFit(chunks []chunker.Chunk, target int, violations []string) float64 {
	if containsIngestionViolation(violations, ingestionViolationChunkSize) ||
		containsIngestionViolation(violations, ingestionViolationTokenLimit) {
		return 0
	}
	return scoreChunkSizeBalance(chunks, target)
}

func scoreTokenSizeFit(request ingestionCandidateMetricsRequest) float64 {
	violations := request.validation.violations
	if containsIngestionViolation(violations, ingestionViolationChunkSize) ||
		containsIngestionViolation(violations, ingestionViolationTokenLimit) {
		return 0
	}
	target := targetTokenCount(request)
	return scoreTokenSizeBalance(request.validation.sourceTokens, target)
}

func targetTokenCount(request ingestionCandidateMetricsRequest) int {
	totalRunes, totalTokens := 0, 0
	for index, current := range request.chunks {
		if index >= len(request.validation.sourceTokens) {
			break
		}
		totalRunes += len([]rune(current.Content))
		totalTokens += request.validation.sourceTokens[index]
	}
	if totalRunes == 0 || totalTokens == 0 {
		return 0
	}
	target := int(math.Ceil(float64(request.scoreConfig.ChunkSize*totalTokens) / float64(totalRunes)))
	if request.tokenLimit > 0 {
		target = min(target, request.tokenLimit)
	}
	return max(1, target)
}

func scoreTokenSizeBalance(lengths []int, target int) float64 {
	if len(lengths) == 0 || target <= 0 {
		return 0
	}
	checked := lengths
	if len(lengths) > 1 {
		checked = lengths[:len(lengths)-1]
	}
	total := 0.0
	for _, length := range checked {
		if length > target {
			return 0
		}
		total += float64(length)
	}
	mean := total / float64(len(checked))
	deviation := 0.0
	for _, length := range checked {
		deviation += math.Abs(float64(length) - mean)
	}
	return math.Max(0.75, 1-deviation/float64(len(checked)*target))
}

func containsIngestionViolation(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func scoreBoundaryQuality(request ingestionCandidateMetricsRequest) float64 {
	if len(request.chunks) <= 1 {
		return 1
	}
	boundaries := ingestionBoundaryWeights(request.document.Blocks, request.policy)
	runes := []rune(request.content)
	hits, maximum := 0, max(1, len(request.policy.StrongBoundaryOrder))
	for _, current := range request.chunks[:len(request.chunks)-1] {
		if weight := boundaries[current.End]; weight > 0 {
			hits += weight
			continue
		}
		if separatorEndsAt(runes, current.End, request.config.Separators) {
			hits++
		}
	}
	return float64(hits) / float64((len(request.chunks)-1)*maximum)
}

func ingestionBoundaryWeights(
	blocks []chunker.SemanticBlock,
	policy types.SemanticPackingPolicy,
) map[int]int {
	maximum := max(1, len(policy.StrongBoundaryOrder))
	priorities := make(map[string]int, len(policy.StrongBoundaryOrder))
	for index, kind := range policy.StrongBoundaryOrder {
		priorities[kind] = maximum - index
	}
	result := make(map[int]int, len(blocks)*2)
	for _, block := range blocks {
		weight := maximum
		if len(priorities) > 0 {
			weight = priorities[ingestionBoundaryKind(block.Kind)]
		}
		if weight > result[block.End] {
			result[block.End] = weight
		}
	}
	return result
}

func ingestionBoundaryKind(kind string) string {
	switch kind {
	case chunker.SemanticKindHeading:
		return "section"
	case chunker.SemanticKindRecord:
		return "record"
	case chunker.SemanticKindTableHeader, chunker.SemanticKindTableRow:
		return "table_row"
	case chunker.SemanticKindListItem:
		return "list_item"
	case chunker.SemanticKindFAQ:
		return "faq_pair"
	case chunker.SemanticKindCodeBlock:
		return "code_block"
	default:
		return "paragraph"
	}
}

func separatorEndsAt(content []rune, boundary int, separators []string) bool {
	for _, separator := range separators {
		value := []rune(separator)
		if len(value) == 0 || boundary < len(value) {
			continue
		}
		if string(content[boundary-len(value):boundary]) == separator {
			return true
		}
	}
	return false
}

func scoreContextEfficiency(chunks []chunker.Chunk, contextValid bool) float64 {
	if !contextValid || len(chunks) == 0 {
		return 0
	}
	sourceRunes, contextRunes := 0, 0
	for _, current := range chunks {
		sourceRunes += len([]rune(current.Content))
		contextRunes += len([]rune(strings.TrimSpace(current.ContextHeader)))
	}
	if contextRunes == 0 {
		return 1
	}
	return float64(sourceRunes) / float64(sourceRunes+contextRunes)
}

func scoreTokenContextEfficiency(validation ingestionCandidateValidationResult) float64 {
	if !validation.contextValid || validation.embeddingTokens <= 0 {
		return 0
	}
	sourceTokens := 0
	for _, count := range validation.sourceTokens {
		sourceTokens += count
	}
	if validation.contextTokens == 0 {
		return 1
	}
	return float64(sourceTokens) / float64(sourceTokens+validation.contextTokens)
}

type parentChildScoreRequest struct {
	children      []chunker.Chunk
	parents       []chunker.Chunk
	parentIndexes []int
	enabled       bool
}

func scoreParentChild(request parentChildScoreRequest) float64 {
	if !request.enabled {
		return 1
	}
	if len(request.children) == 0 || len(request.children) != len(request.parentIndexes) {
		return 0
	}
	consistent := 0
	for index, parentIndex := range request.parentIndexes {
		if parentIndex == -1 {
			consistent++
			continue
		}
		if parentIndex < 0 || parentIndex >= len(request.parents) {
			continue
		}
		child, parent := request.children[index], request.parents[parentIndex]
		if child.Start >= parent.Start && child.End <= parent.End {
			consistent++
		}
	}
	return float64(consistent) / float64(len(request.children))
}

func roundScore(value float64) float64 {
	return math.Round(value*100) / 100
}
