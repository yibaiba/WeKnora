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
	ingestionViolationContextBudget      = "context_budget_exceeded"
	ingestionViolationRequiredContext    = "required_context_budget_exceeded"
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
	violations      []string
	quality         types.IngestionStructureQuality
	descriptions    []types.IngestionChunkStructureDescription
	atomicEligible  int
	atomicRetained  int
	contextValid    bool
	sourceTokens    []int
	contextTokens   int
	embeddingTokens int
	tokenCountMode  string
	tokenizerID     string
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
) (ingestionCandidateValidationResult, error) {
	violations := make(ingestionViolationSet)
	validateCandidateRanges(request, violations)
	result, err := validateCandidateStructure(request, violations)
	if err != nil {
		return ingestionCandidateValidationResult{}, err
	}
	if err := validateCandidateLimits(request, &result, violations); err != nil {
		return ingestionCandidateValidationResult{}, err
	}
	result.violations = sortedIngestionViolations(violations)
	result.descriptions = describeIngestionChunks(request)
	return result, nil
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
	result *ingestionCandidateValidationResult,
	violations ingestionViolationSet,
) error {
	counter := request.constraints.TokenCounter
	if counter == nil {
		var err error
		counter, err = chunker.NewTokenCounter(chunker.TokenCounterConfig{
			Encoding: chunker.TokenizerEncodingByteUpperBound,
		})
		if err != nil {
			return err
		}
	}
	for _, current := range request.chunks {
		if current.End-current.Start > request.scoreConfig.ChunkSize {
			violations.add(ingestionViolationChunkSize)
		}
		embedding, err := counter.Count(chunker.PrependEmbeddingPrefix(
			request.constraints.EmbeddingPrefix, current.EmbeddingContent(),
		))
		if err != nil {
			return err
		}
		source, err := counter.Count(current.Content)
		if err != nil {
			return err
		}
		context, err := counter.Count(current.ContextHeader)
		if err != nil {
			return err
		}
		result.sourceTokens = append(result.sourceTokens, source.Count)
		result.contextTokens += context.Count
		result.embeddingTokens += embedding.Count
		result.tokenCountMode = embedding.Mode
		result.tokenizerID = embedding.TokenizerID
		if request.constraints.TokenLimit > 0 && embedding.Count > request.constraints.TokenLimit {
			violations.add(ingestionViolationTokenLimit)
		}
		if request.constraints.TokenLimit > 0 && context.Count > min(128, request.constraints.TokenLimit/5) {
			violations.add(ingestionViolationContextBudget)
		}
		for _, reason := range current.ContextReasonCodes {
			if reason == chunker.SemanticReasonRequiredContextExceeds {
				violations.add(ingestionViolationRequiredContext)
			}
		}
	}
	return nil
}

func validateCandidateStructure(
	request ingestionCandidateValidationRequest,
	violations ingestionViolationSet,
) (ingestionCandidateValidationResult, error) {
	validator := ingestionStructureValidator{
		request: request, index: newIngestionSemanticIndex(request.content, request.document),
		result: ingestionCandidateValidationResult{contextValid: true}, violations: violations,
	}
	if err := validator.validateAtomicBlocks(); err != nil {
		return ingestionCandidateValidationResult{}, err
	}
	validator.validateTableContinuations()
	validator.validateCodeContinuations()
	validator.validateChunkContexts()
	validator.result.quality.MixedSections = countMixedSectionChunks(request.chunks, validator.index)
	return validator.result, nil
}

func (validator *ingestionStructureValidator) validateAtomicBlocks() error {
	for _, block := range validator.request.document.Blocks {
		if !block.Atomic || block.Confidence != chunker.SemanticConfidenceHigh {
			continue
		}
		fits, err := validator.semanticBlockFitsBudget(block)
		if err != nil {
			return err
		}
		if !fits {
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
	return nil
}

func (validator ingestionStructureValidator) semanticBlockFitsBudget(
	block chunker.SemanticBlock,
) (bool, error) {
	if block.End-block.Start > validator.request.scoreConfig.ChunkSize {
		return false, nil
	}
	if validator.request.constraints.TokenLimit <= 0 {
		return true, nil
	}
	body := validator.index.blockText(block)
	context, err := chunker.BuildSemanticContext(chunker.SemanticContextRequest{
		Content: validator.request.content, Document: validator.request.document,
		Block: block, TokenLimit: validator.request.constraints.TokenLimit,
		TokenCounter:    validator.request.constraints.TokenCounter,
		EmbeddingPrefix: validator.request.constraints.EmbeddingPrefix,
	})
	if err != nil {
		return false, err
	}
	if context.Header != "" {
		body = context.Header + "\n\n" + body
	}
	body = chunker.PrependEmbeddingPrefix(validator.request.constraints.EmbeddingPrefix, body)
	counter := validator.request.constraints.TokenCounter
	if counter == nil {
		var err error
		counter, err = chunker.NewTokenCounter(chunker.TokenCounterConfig{
			Encoding: chunker.TokenizerEncodingByteUpperBound,
		})
		if err != nil {
			return false, err
		}
	}
	count, err := counter.Count(body)
	if err != nil {
		return false, err
	}
	return count.Count <= validator.request.constraints.TokenLimit, nil
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
