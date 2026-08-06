package service

import (
	"sort"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ingestionViolationSourcePosition     = "source_position_invalid"
	ingestionViolationSourceOrder        = "source_range_order_invalid"
	ingestionViolationSourceCoverage     = "source_coverage_gap"
	ingestionViolationOverlap            = "overlap_exceeds_config"
	ingestionViolationParentMapping      = "parent_child_mapping_invalid"
	ingestionViolationChunkSize          = "chunk_size_exceeded"
	ingestionViolationTokenLimit         = "token_limit_exceeded"
	ingestionViolationAtomicSplit        = "atomic_block_split"
	ingestionViolationTableHeaderMissing = "table_continuation_header_missing"
	ingestionViolationTableHeaderInvalid = "table_continuation_header_invalid"
	ingestionViolationCodeContextMissing = "code_continuation_context_missing"
	ingestionViolationContextSource      = "context_source_invalid"
)

type ingestionCandidateValidationRequest struct {
	content       string
	document      chunker.SemanticDocument
	chunks        []chunker.Chunk
	parents       []chunker.Chunk
	parentIndexes []int
	config        types.IngestionChunkingRecommendation
	scoreConfig   chunker.SplitterConfig
	constraints   types.IngestionChunkingConstraints
}

type ingestionCandidateValidationResult struct {
	violations     []string
	quality        types.IngestionStructureQuality
	descriptions   []types.IngestionChunkStructureDescription
	atomicEligible int
	atomicRetained int
	contextValid   bool
}

type ingestionViolationSet map[string]struct{}

type ingestionCoverageOptions struct {
	totalLength    int
	allowedOverlap int
}

type ingestionStructureValidator struct {
	request    ingestionCandidateValidationRequest
	index      ingestionSemanticIndex
	result     ingestionCandidateValidationResult
	violations ingestionViolationSet
}

func validateIngestionCandidate(
	request ingestionCandidateValidationRequest,
) ingestionCandidateValidationResult {
	violations := make(ingestionViolationSet)
	validateCandidateRanges(request, violations)
	validateCandidateLimits(request, violations)
	result := validateCandidateStructure(request, violations)
	result.violations = sortedIngestionViolations(violations)
	result.descriptions = describeIngestionChunks(request)
	return result
}

func validateCandidateRanges(
	request ingestionCandidateValidationRequest,
	violations ingestionViolationSet,
) {
	if validateIngestionChunkPositions(request.content, request.chunks) != nil {
		violations.add(ingestionViolationSourcePosition)
	}
	if validateIngestionChunkOrder(request.chunks) != nil {
		violations.add(ingestionViolationSourceOrder)
	}
	validateChunkCoverage(request.chunks, ingestionCoverageOptions{
		totalLength:    len([]rune(request.content)),
		allowedOverlap: max(request.config.ChunkOverlap, request.scoreConfig.ChunkOverlap),
	}, violations)
	if len(request.parents) > 0 {
		validateParentRanges(request, violations)
	}
	if request.config.EnableParentChild &&
		validateParentChildPreview(request.chunks, request.parents, request.parentIndexes) != nil {
		violations.add(ingestionViolationParentMapping)
	}
}

func validateParentRanges(
	request ingestionCandidateValidationRequest,
	violations ingestionViolationSet,
) {
	if validateIngestionChunkPositions(request.content, request.parents) != nil ||
		validateIngestionChunkOrder(request.parents) != nil {
		violations.add(ingestionViolationParentMapping)
	}
	for _, parent := range request.parents {
		if parent.End-parent.Start > request.config.ParentChunkSize {
			violations.add(ingestionViolationChunkSize)
		}
	}
}

func validateChunkCoverage(
	chunks []chunker.Chunk,
	options ingestionCoverageOptions,
	violations ingestionViolationSet,
) {
	if len(chunks) == 0 {
		violations.add(ingestionViolationSourcePosition)
		violations.add(ingestionViolationSourceCoverage)
		return
	}
	ordered := append([]chunker.Chunk(nil), chunks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	coveredEnd := 0
	for _, current := range ordered {
		if current.Start > coveredEnd {
			violations.add(ingestionViolationSourceCoverage)
		}
		if current.Start < coveredEnd && coveredEnd-current.Start > options.allowedOverlap {
			violations.add(ingestionViolationOverlap)
		}
		coveredEnd = max(coveredEnd, current.End)
	}
	if ordered[0].Start != 0 || coveredEnd != options.totalLength {
		violations.add(ingestionViolationSourceCoverage)
	}
}

func validateCandidateLimits(
	request ingestionCandidateValidationRequest,
	violations ingestionViolationSet,
) {
	language := ingestionValidationLanguage(request.constraints, request.content)
	for _, current := range request.chunks {
		if current.End-current.Start > request.scoreConfig.ChunkSize {
			violations.add(ingestionViolationChunkSize)
		}
		if request.constraints.TokenLimit > 0 &&
			chunker.ApproxTokenCount(current.EmbeddingContent(), language) > request.constraints.TokenLimit {
			violations.add(ingestionViolationTokenLimit)
		}
	}
}

func ingestionValidationLanguage(
	constraints types.IngestionChunkingConstraints,
	content string,
) string {
	if len(constraints.Languages) > 0 {
		return constraints.Languages[0]
	}
	return chunker.DetectLanguage(content)
}

func validateCandidateStructure(
	request ingestionCandidateValidationRequest,
	violations ingestionViolationSet,
) ingestionCandidateValidationResult {
	validator := ingestionStructureValidator{
		request: request, index: newIngestionSemanticIndex(request.content, request.document),
		result: ingestionCandidateValidationResult{contextValid: true}, violations: violations,
	}
	validator.validateAtomicBlocks()
	validator.validateTableContinuations()
	validator.validateCodeContinuations()
	validator.validateChunkContexts()
	validator.result.quality.MixedSections = countMixedSectionChunks(request.chunks, validator.index)
	return validator.result
}

func (validator *ingestionStructureValidator) validateAtomicBlocks() {
	language := ingestionValidationLanguage(validator.request.constraints, validator.request.content)
	for _, block := range validator.request.document.Blocks {
		if !block.Atomic || block.Confidence != chunker.SemanticConfidenceHigh {
			continue
		}
		if !validator.semanticBlockFitsBudget(block, language) {
			validator.result.quality.OversizeAtomicBlocks++
			continue
		}
		validator.result.atomicEligible++
		if semanticBlockContained(block, validator.request.chunks) {
			validator.result.atomicRetained++
			continue
		}
		validator.result.quality.SplitAtomicBlocks++
		validator.violations.add(ingestionViolationAtomicSplit)
	}
}

func (validator ingestionStructureValidator) semanticBlockFitsBudget(
	block chunker.SemanticBlock,
	language string,
) bool {
	if block.End-block.Start > validator.request.scoreConfig.ChunkSize {
		return false
	}
	if validator.request.constraints.TokenLimit <= 0 {
		return true
	}
	body := validator.index.blockText(block)
	header := validator.index.atomicBudgetContext(block)
	if header != "" {
		body = header + "\n\n" + body
	}
	return chunker.ApproxTokenCount(body, language) <= validator.request.constraints.TokenLimit
}

func semanticBlockContained(block chunker.SemanticBlock, chunks []chunker.Chunk) bool {
	for _, current := range chunks {
		if current.Start <= block.Start && current.End >= block.End {
			return true
		}
	}
	return false
}

func sortedIngestionViolations(values ingestionViolationSet) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (values ingestionViolationSet) add(value string) {
	values[value] = struct{}{}
}
